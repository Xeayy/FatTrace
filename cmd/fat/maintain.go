//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/alerts"
	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/daily"
	"github.com/Xeayy/FatTrace/internal/health"
	"github.com/Xeayy/FatTrace/internal/logfile"
	"github.com/Xeayy/FatTrace/internal/prune"
	"github.com/Xeayy/FatTrace/internal/report"
	"github.com/Xeayy/FatTrace/internal/rules"
	"github.com/Xeayy/FatTrace/internal/snap"
	"github.com/Xeayy/FatTrace/internal/store"
)

// maintainReport is the JSON result of one scheduled maintenance run.
type maintainReport struct {
	Kind      string         `json:"kind"`
	StartedAt int64          `json:"started_at_ms"`
	Snapshots []snap.Result  `json:"snapshots"`
	Daily     daily.Report   `json:"daily"`
	Alerts    alerts.Report  `json:"alerts"`
	Health    health.Report  `json:"health"`
	Reports   []reportResult `json:"reports,omitempty"`
	Prune     *prune.Report  `json:"prune,omitempty"`
	Errors    []string       `json:"errors,omitempty"`
}

type reportResult struct {
	Root   string `json:"root"`
	FromID int64  `json:"from_snapshot_id,omitempty"`
	ToID   int64  `json:"to_snapshot_id,omitempty"`
	Path   string `json:"path,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Skip   string `json:"skipped,omitempty"`
}

func maintainCmd(args []string) {
	fs := flag.NewFlagSet("maintain", flag.ExitOnError)
	kind := fs.String("kind", "daily", "snapshot kind: daily or weekly")
	dbPath := fs.String("db", "", "database file")
	quiet := fs.Bool("quiet", false, "suppress scan progress")
	asJSON := fs.Bool("json", false, "output JSON")
	_ = fs.Parse(args)

	if *kind != "daily" && *kind != "weekly" {
		fail(fmt.Errorf("maintain: kind must be daily or weekly"))
	}
	path, err := resolveDB(*dbPath)
	if err != nil {
		fail(err)
	}

	logPath := filepath.Join(filepath.Dir(path), "logs", "maintain.log")
	lw, logErr := logfile.Open(logPath, 5<<20, 3)
	var logger *log.Logger
	if logErr != nil {
		fmt.Fprintf(os.Stderr, "warning: open maintain log: %v\n", logErr)
	} else {
		defer lw.Close()
		logger = log.New(lw, "", log.LstdFlags)
	}
	logf := func(format string, args ...any) {
		if logger != nil {
			logger.Printf(format, args...)
		}
	}
	logf("maintain start kind=%s db=%s", *kind, path)

	cfg, err := config.Load(path)
	if err != nil {
		logf("config error: %v", err)
		fail(err)
	}
	if len(cfg.Schedule.Roots) == 0 {
		err := fmt.Errorf("maintain: config schedule.roots is empty; run fat config init -roots \"D:\\\"")
		logf("config error: %v", err)
		fail(err)
	}
	// Daily and weekly catch-up runs can start at the same logon. Waiting for
	// the writer lock turns the second run into a queued run instead of a
	// failed task.
	st, err := store.OpenWait(path, 10*time.Minute)
	if err != nil {
		logf("open store error: %v", err)
		fail(err)
	}
	defer st.Close()
	set, err := rules.Load(path)
	if err != nil {
		logf("load rules error: %v", err)
		fail(err)
	}
	ctx := context.Background()
	rep := maintainReport{Kind: *kind, StartedAt: time.Now().UnixMilli()}

	for _, root := range cfg.Schedule.Roots {
		opts := snap.Options{Root: root, Kind: *kind}
		if !*quiet && !*asJSON {
			opts.ProgressInterval = 2 * time.Second
			opts.Progress = func(files, dirs int64) {
				fmt.Fprintf(os.Stderr, "scanning %s: %d files, %d dirs\n", root, files, dirs)
			}
		}
		res, err := snap.Run(ctx, st, opts)
		rep.Snapshots = append(rep.Snapshots, res)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("snapshot %s: %v", root, err))
			logf("snapshot %s error: %v", root, err)
			continue
		}
		logf("snapshot #%d %s %s files=%d dirs=%d alloc=%d errors=%d",
			res.SnapshotID, res.Status, root, res.Files, res.Dirs,
			res.Stats.UniqueAllocBytes, res.Stats.Errors)
		if !*quiet && !*asJSON {
			fmt.Fprintf(os.Stderr, "snapshot #%d %s: files=%d dirs=%d alloc=%s\n",
				res.SnapshotID, res.Status, res.Files, res.Dirs, formatBytes(res.Stats.UniqueAllocBytes))
		}
	}

	// File rows expire after seven days by default; three days is enough to
	// refresh today and repair a missed run without touching expired history.
	dailyOpts := daily.Options{Days: 3, TopN: 10}
	if !*quiet && !*asJSON {
		dailyOpts.Progress = func(day string, from, to int64) {
			fmt.Fprintf(os.Stderr, "daily %s: #%d → #%d\n", day, from, to)
		}
	}
	rep.Daily, err = daily.Compute(ctx, st, set, dailyOpts)
	if err != nil {
		rep.Errors = append(rep.Errors, "daily: "+err.Error())
		logf("daily error: %v", err)
	} else {
		logf("daily days=%d warnings=%d", len(rep.Daily.Days), len(rep.Daily.Warnings))
		for _, w := range rep.Daily.Warnings {
			logf("daily warning: %s", w)
		}
	}

	alertRep, err := alerts.Evaluate(ctx, st, cfg.Alerts, alerts.Options{Persist: true})
	if err != nil {
		rep.Errors = append(rep.Errors, "alerts: "+err.Error())
		logf("alerts error: %v", err)
	} else {
		rep.Alerts = alertRep
		fired := 0
		for _, a := range alertRep.Alerts {
			if a.Fired {
				fired++
			}
		}
		logf("alerts checked=%d fired=%d", len(alertRep.Alerts), fired)
	}

	if snaps, err := st.ListSnapshots(ctx, 1000); err != nil {
		logf("health error: %v", err)
	} else {
		hr := health.Evaluate(snaps, cfg.Schedule.Roots,
			time.Duration(cfg.Alerts.StaleHours*float64(time.Hour)), time.Now())
		rep.Health = hr
		for _, hs := range hr.Roots {
			logf("health %s level=%s failures=%d stale=%v reason=%s",
				hs.Root, hs.Level, hs.ConsecutiveFailures, hs.Stale, hs.Reason)
		}
	}

	if *kind == "weekly" {
		rep.Reports, err = writeWeeklyReports(ctx, st, path, cfg.Schedule.Roots)
		if err != nil {
			rep.Errors = append(rep.Errors, "report: "+err.Error())
			logf("report error: %v", err)
		}
		for _, rr := range rep.Reports {
			if rr.Path != "" {
				logf("report #%d->#%d %s %d bytes", rr.FromID, rr.ToID, rr.Path, rr.Bytes)
			} else {
				logf("report skipped %s: %s", rr.Root, rr.Skip)
			}
		}
	}

	if cfg.Retention.AutoPrune {
		pr, err := prune.Run(ctx, st, cfg.Retention, true, time.Now())
		if err != nil {
			rep.Errors = append(rep.Errors, "prune: "+err.Error())
			logf("prune error: %v", err)
		} else {
			rep.Prune = &pr
			logf("prune deleted snapshots=%d file_rows=%d summaries=%d events=%d",
				pr.Deleted.Snapshots, pr.Deleted.FileRows, pr.Deleted.Summaries, pr.Deleted.FileEvents)
		}
	}

	logf("maintain done snapshots=%d daily=%d reports=%d errors=%d",
		len(rep.Snapshots), len(rep.Daily.Days), len(rep.Reports), len(rep.Errors))
	for _, e := range rep.Errors {
		logf("error: %s", e)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fail(err)
		}
	} else {
		printMaintain(rep)
	}
	if len(rep.Errors) > 0 {
		for _, e := range rep.Errors {
			fmt.Fprintf(os.Stderr, "error: %s\n", e)
		}
		os.Exit(1)
	}
}

// weeklyPair picks the two newest complete weekly snapshots with the same root.
// Weekly reports need a full week between them, so closely spaced manual runs
// are skipped instead of producing a misleading "weekly" page.
func weeklyPair(ctx context.Context, st *store.Store, root string) (store.SnapshotInfo, store.SnapshotInfo, string, error) {
	list, err := st.RecentComplete(ctx, root, 200)
	if err != nil {
		return store.SnapshotInfo{}, store.SnapshotInfo{}, "", err
	}
	clean := filepath.Clean(root)
	var weeks []store.SnapshotInfo
	for _, sn := range list {
		if sn.Kind == "weekly" && strings.EqualFold(filepath.Clean(sn.Root), clean) {
			weeks = append(weeks, sn)
		}
	}
	if len(weeks) < 2 {
		return store.SnapshotInfo{}, store.SnapshotInfo{}, "need two complete weekly snapshots", nil
	}
	from, to := weeks[1], weeks[0]
	const dayMS = int64(24 * time.Hour / time.Millisecond)
	span := to.StartedAt - from.StartedAt
	if span < 5*dayMS {
		return from, to, fmt.Sprintf("weekly snapshots are only %.1f days apart",
			float64(span)/float64(dayMS)), nil
	}
	return from, to, "", nil
}

// writeWeeklyReports renders one offline HTML report per configured root. A
// missing pair or a render failure is reported as a skip so one volume cannot
// block the rest of the maintenance run.
func writeWeeklyReports(ctx context.Context, st *store.Store, dbPath string, roots []string) ([]reportResult, error) {
	dir := filepath.Join(filepath.Dir(dbPath), "reports")
	out := make([]reportResult, 0, len(roots))
	for _, root := range roots {
		rr := reportResult{Root: root}
		from, to, reason, err := weeklyPair(ctx, st, root)
		if err != nil {
			return out, err
		}
		rr.FromID, rr.ToID = from.ID, to.ID
		if reason != "" {
			rr.Skip = reason
			out = append(out, rr)
			continue
		}
		page, err := report.Run(ctx, st, report.Options{
			FromID: from.ID, ToID: to.ID, TopN: 20, Week: true,
		})
		if err != nil {
			rr.Skip = err.Error()
			out = append(out, rr)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			rr.Skip = err.Error()
			out = append(out, rr)
			continue
		}
		name := weeklyReportName(to.VolumeID, root, time.UnixMilli(to.StartedAt))
		path := filepath.Join(dir, name)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, page, 0o644); err != nil {
			_ = os.Remove(tmp)
			rr.Skip = err.Error()
			out = append(out, rr)
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			rr.Skip = err.Error()
			out = append(out, rr)
			continue
		}
		rr.Path, rr.Bytes = path, int64(len(page))
		out = append(out, rr)
	}
	return out, nil
}

func weeklyReportName(volumeID int64, root string, at time.Time) string {
	clean := strings.ToLower(filepath.Clean(root))
	trimmed := strings.TrimRight(clean, `\`)
	label := trimmed
	if i := strings.LastIndexAny(trimmed, `\:`); i >= 0 {
		label = trimmed[i+1:]
	}
	label = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, label)
	if label == "" {
		label = "root"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(clean))
	return fmt.Sprintf("weekly-%d-%s-%s-%08x.html", volumeID,
		at.Local().Format("2006-01-02"), label, h.Sum32())
}

func printMaintain(rep maintainReport) {
	fmt.Printf("kind:      %s\n", rep.Kind)
	for _, s := range rep.Snapshots {
		if s.SnapshotID == 0 {
			continue
		}
		fmt.Printf("snapshot:  #%d %s files=%d dirs=%d alloc=%s\n",
			s.SnapshotID, s.Status, s.Files, s.Dirs, formatBytes(s.Stats.UniqueAllocBytes))
	}
	fmt.Printf("daily:     %d day(s)\n", len(rep.Daily.Days))
	if len(rep.Daily.Days) > 0 {
		printDaily(rep.Daily)
	}
	if len(rep.Alerts.Alerts) > 0 {
		fmt.Println("alerts:")
		for _, a := range rep.Alerts.Alerts {
			status := "ok"
			if a.Fired {
				status = "FIRED"
			} else if a.Muted {
				status = "muted"
			}
			fmt.Printf("  %s  %s  %s\n", a.Key, formatBytes(a.ValueBytes), status)
		}
	}
	for _, rr := range rep.Reports {
		if rr.Path != "" {
			fmt.Printf("report:    #%d → #%d  %s (%s)\n",
				rr.FromID, rr.ToID, rr.Path, formatBytes(rr.Bytes))
		} else {
			fmt.Printf("report:    skipped %s: %s\n", rr.Root, rr.Skip)
		}
	}
	if rep.Prune != nil {
		fmt.Printf("prune:     deleted snapshots=%d file_rows=%d summaries=%d events=%d\n",
			rep.Prune.Deleted.Snapshots, rep.Prune.Deleted.FileRows,
			rep.Prune.Deleted.Summaries, rep.Prune.Deleted.FileEvents)
	}
	for _, w := range rep.Daily.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}
