//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"fattrace/internal/alerts"
	"fattrace/internal/config"
	"fattrace/internal/daily"
	"fattrace/internal/prune"
	"fattrace/internal/rules"
	"fattrace/internal/snap"
	"fattrace/internal/store"
)

// maintainReport is the JSON result of one scheduled maintenance run.
type maintainReport struct {
	Kind      string        `json:"kind"`
	StartedAt int64         `json:"started_at_ms"`
	Snapshots []snap.Result `json:"snapshots"`
	Daily     daily.Report  `json:"daily"`
	Alerts    alerts.Report `json:"alerts"`
	Prune     *prune.Report `json:"prune,omitempty"`
	Errors    []string      `json:"errors,omitempty"`
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
	cfg, err := config.Load(path)
	if err != nil {
		fail(err)
	}
	if len(cfg.Schedule.Roots) == 0 {
		fail(fmt.Errorf("maintain: config schedule.roots is empty; run fat config init -roots \"D:\\\""))
	}
	st, err := store.Open(path)
	if err != nil {
		fail(err)
	}
	defer st.Close()
	set, err := rules.Load(path)
	if err != nil {
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
			continue
		}
		if !*quiet && !*asJSON {
			fmt.Fprintf(os.Stderr, "snapshot #%d %s: files=%d dirs=%d alloc=%s\n",
				res.SnapshotID, res.Status, res.Files, res.Dirs, formatBytes(res.Stats.UniqueAllocBytes))
		}
	}

	dailyOpts := daily.Options{Days: 14, TopN: 10}
	if !*quiet && !*asJSON {
		dailyOpts.Progress = func(day string, from, to int64) {
			fmt.Fprintf(os.Stderr, "daily %s: #%d → #%d\n", day, from, to)
		}
	}
	rep.Daily, err = daily.Compute(ctx, st, set, dailyOpts)
	if err != nil {
		rep.Errors = append(rep.Errors, "daily: "+err.Error())
	}

	alertRep, err := alerts.Evaluate(ctx, st, cfg.Alerts, alerts.Options{Persist: true})
	if err != nil {
		rep.Errors = append(rep.Errors, "alerts: "+err.Error())
	} else {
		rep.Alerts = alertRep
	}

	if *kind == "weekly" && cfg.Retention.AutoPrune {
		pr, err := prune.Run(ctx, st, cfg.Retention, true, time.Now())
		if err != nil {
			rep.Errors = append(rep.Errors, "prune: "+err.Error())
		} else {
			rep.Prune = &pr
		}
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
	if rep.Prune != nil {
		fmt.Printf("prune:     deleted snapshots=%d summaries=%d events=%d\n",
			rep.Prune.Deleted.Snapshots, rep.Prune.Deleted.Summaries, rep.Prune.Deleted.FileEvents)
	}
	for _, w := range rep.Daily.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}
