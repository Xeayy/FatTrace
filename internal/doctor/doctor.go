//go:build windows

// Package doctor runs read-only health checks over an existing FatTrace
// installation: configuration, database integrity, snapshot freshness,
// coverage, rules, permissions and scheduled tasks. It never scans, writes the
// database, installs tasks or touches user files.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fattrace/internal/config"
	"fattrace/internal/model"
	"fattrace/internal/rules"
	"fattrace/internal/scan"
	"fattrace/internal/schedule"
	"fattrace/internal/store"
)

// Level is the outcome of one check.
type Level string

const (
	LevelOK   Level = "ok"
	LevelWarn Level = "warn"
	LevelFail Level = "fail"
	LevelSkip Level = "skip"
)

// Check is one named diagnostic result.
type Check struct {
	Name    string `json:"name"`
	Level   Level  `json:"level"`
	Summary string `json:"summary"`
	Hint    string `json:"hint,omitempty"`
}

// Report is the complete doctor output.
type Report struct {
	CheckedAt int64   `json:"checked_at_ms"`
	DBPath    string  `json:"db_path"`
	Version   string  `json:"version,omitempty"`
	Elevated  bool    `json:"elevated"`
	Checks    []Check `json:"checks"`
}

// Options controls the checks. SkipTasks is used by tests and by environments
// where Task Scheduler is not available.
type Options struct {
	DBPath    string
	Version   string
	Now       time.Time
	SkipTasks bool
}

// Add appends one check.
func (r *Report) Add(name string, level Level, summary, hint string) {
	r.Checks = append(r.Checks, Check{Name: name, Level: level, Summary: summary, Hint: hint})
}

// Count returns the number of checks at level.
func (r Report) Count(level Level) int {
	n := 0
	for _, c := range r.Checks {
		if c.Level == level {
			n++
		}
	}
	return n
}

// Failed reports whether any check has level fail.
func (r Report) Failed() bool { return r.Count(LevelFail) > 0 }

// Run performs every check that can run without writing to disk.
func Run(ctx context.Context, opts Options) Report {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	rep := Report{
		CheckedAt: opts.Now.UnixMilli(),
		DBPath:    opts.DBPath,
		Version:   opts.Version,
		Elevated:  isElevated(),
	}
	add := rep.Add

	cfg, cfgErr := config.Load(opts.DBPath)
	if cfgErr != nil {
		add("config", LevelFail, cfgErr.Error(), "fix config.yaml; every command reads it")
		cfg = config.Defaults()
	} else if !cfg.Exists {
		add("config", LevelWarn, "config.yaml not found; using built-in defaults",
			`run fat config init -roots "D:\"`)
	} else {
		add("config", LevelOK, cfg.Path, "")
	}
	if len(cfg.Schedule.Roots) == 0 {
		add("roots", LevelFail, "no scan roots configured",
			`run fat config init -roots "D:\" or edit schedule.roots`)
	} else {
		add("roots", LevelOK, strings.Join(cfg.Schedule.Roots, ", "), "")
	}

	st, err := store.OpenReader(opts.DBPath)
	if err != nil {
		hint := "run fat db init"
		if errors.Is(err, store.ErrNotInitialized) {
			hint = "run fat db init to create the schema"
		}
		add("database", LevelFail, err.Error(), hint)
		return rep
	}
	defer st.Close()

	if v, err := st.SchemaVersion(ctx); err != nil {
		add("schema", LevelFail, err.Error(), "")
	} else if v < store.CurrentSchemaVersion {
		add("schema", LevelWarn, fmt.Sprintf("schema v%d, binary expects v%d", v, store.CurrentSchemaVersion),
			"run fat db init to migrate")
	} else if v > store.CurrentSchemaVersion {
		add("schema", LevelFail, fmt.Sprintf("schema v%d is newer than this binary (v%d)", v, store.CurrentSchemaVersion),
			"upgrade fat.exe")
	} else {
		add("schema", LevelOK, fmt.Sprintf("v%d", v), "")
	}

	if result, err := st.QuickCheck(ctx); err != nil {
		add("integrity", LevelFail, err.Error(), "back up the database before repairing it")
	} else if result != "ok" {
		add("integrity", LevelFail, "quick_check: "+result, "restore from a backup or run sqlite integrity tools")
	} else {
		add("integrity", LevelOK, "quick_check ok", "")
	}

	status, statusErr := st.Status(ctx)
	if statusErr != nil {
		add("database", LevelWarn, statusErr.Error(), "fat status may still work")
	} else {
		total := status.SizeBytes + status.WALBytes
		add("database", LevelOK, fmt.Sprintf("schema v%d · %s (db %s + wal %s) · %d tables",
			status.SchemaVersion, formatBytes(total), formatBytes(status.SizeBytes),
			formatBytes(status.WALBytes), len(status.Tables)), "")
		if len(status.Volumes) == 0 {
			add("volumes", LevelWarn, "no volume has been recorded", "run fat snap once")
		} else {
			add("volumes", LevelOK, fmt.Sprintf("%d recorded", len(status.Volumes)), "")
		}
		checkSpace(add, opts.DBPath, total)
	}

	if rep.Elevated {
		add("permissions", LevelOK, "administrator token", "")
	} else {
		add("permissions", LevelOK, "standard user token",
			"protected system directories are unreadable; coverage and error counts show the gap")
	}

	snapshots, snapErr := st.ListSnapshots(ctx, 1000)
	if snapErr != nil {
		add("snapshots", LevelWarn, snapErr.Error(), "run fat status to inspect the database")
	} else if len(snapshots) == 0 {
		add("snapshots", LevelWarn, "no snapshots yet", "run fat snap or fat maintain")
	}
	for _, root := range cfg.Schedule.Roots {
		checkRoot(ctx, st, add, root, opts.Now, snapshots)
	}

	if set, err := rules.Load(opts.DBPath); err != nil {
		add("rules", LevelFail, err.Error(), "fix rules.yaml; fat diff/report/serve also fail on it")
	} else {
		add("rules", LevelOK, fmt.Sprintf("%d built-in · %d user", set.RulesBuiltin, set.RulesUser),
			"")
	}

	if opts.SkipTasks {
		add("tasks", LevelSkip, "scheduled task checks skipped", "")
	} else {
		checkTasks(ctx, add, cfg.Schedule.ACOnly)
	}
	return rep
}

func checkSpace(add func(string, Level, string, string), dbPath string, dbBytes int64) {
	total, free, _, err := scan.GetVolumeSpace(filepath.Dir(dbPath))
	if err != nil {
		add("storage", LevelWarn, "cannot read free space: "+err.Error(), "")
		return
	}
	summary := fmt.Sprintf("database volume %s free / %s total", formatBytes(free), formatBytes(total))
	need := int64(1 << 30)
	if dbBytes*2 > need {
		need = dbBytes * 2
	}
	if free < need {
		add("storage", LevelWarn, summary,
			fmt.Sprintf("free space is below %s; prune old history or move the database", formatBytes(need)))
		return
	}
	add("storage", LevelOK, summary, "")
}

func checkRoot(ctx context.Context, st *store.Store, add func(string, Level, string, string),
	root string, now time.Time, all []store.SnapshotInfo) {
	name := "snapshot " + root
	info, err := os.Stat(root)
	if err != nil {
		add(name, LevelFail, "root is not accessible: "+err.Error(), "fix schedule.roots or reconnect the volume")
		return
	}
	if !info.IsDir() {
		add(name, LevelFail, "root is not a directory", "fix schedule.roots")
		return
	}
	meta, err := scan.VolumeMetadata(root)
	if err != nil {
		add(name, LevelFail, "cannot identify volume: "+err.Error(), "")
		return
	}
	if !strings.EqualFold(meta.FSType, "NTFS") {
		add(name, LevelFail, fmt.Sprintf("filesystem %s is not NTFS", meta.FSType),
			"FatTrace only supports local NTFS volumes")
		return
	}

	complete, ok := latestCompleteForRoot(ctx, st, root)
	if !ok {
		add(name, LevelFail, "no complete snapshot for this root",
			"run fat snap --kind daily \""+root+"\" or wait for the scheduled task")
		return
	}
	latest, hasLatest := latestSnapshotForRoot(all, root)
	level := LevelOK
	var notes, hints []string
	if hasLatest && !strings.EqualFold(latest.Status, "complete") && latest.StartedAt >= complete.StartedAt {
		level = LevelWarn
		notes = append(notes, fmt.Sprintf("#%d %s", latest.ID, latest.Status))
		hints = append(hints, "check logs/maintain.log and the scan error table")
	}
	age := now.Sub(time.UnixMilli(complete.StartedAt))
	if age < 0 {
		age = 0
	}
	if age > 36*time.Hour {
		level = LevelWarn
		notes = append(notes, fmt.Sprintf("last complete %.1f days ago", age.Hours()/24))
		hints = append(hints, "run fat maintain or check the scheduled task")
	}
	if model.IsVolumeRoot(root) && complete.VolumeUsed > 0 && complete.CoveragePct < 95 {
		level = LevelWarn
		notes = append(notes, fmt.Sprintf("coverage %.2f%%", complete.CoveragePct))
		hints = append(hints, "protected directories are unreadable; an elevated scan may close the gap")
	}
	if complete.Errors > 0 {
		level = LevelWarn
		hints = append(hints, "run fat report to inspect the error summary")
	}
	if complete.Files > 0 {
		if n, err := st.CountSnapshotFiles(ctx, []int64{complete.ID}); err == nil && n == 0 {
			level = LevelWarn
			notes = append(notes, "file rows pruned")
			hints = append(hints, "file-level diff/browse is unavailable for this snapshot; directory aggregates remain")
		}
	}
	summary := fmt.Sprintf("#%d %s · %s · %s · %.0f files · coverage %.2f%% · errors %d",
		complete.ID, complete.Kind, formatMillis(complete.StartedAt), humanAge(age),
		float64(complete.Files), complete.CoveragePct, complete.Errors)
	if len(notes) > 0 {
		summary += " · " + strings.Join(notes, " · ")
	}
	add(name, level, summary, strings.Join(hints, "; "))
}

func latestCompleteForRoot(ctx context.Context, st *store.Store, root string) (store.SnapshotInfo, bool) {
	list, err := st.RecentComplete(ctx, strings.TrimRight(root, `\`), 200)
	if err != nil {
		return store.SnapshotInfo{}, false
	}
	for _, sn := range list {
		if sameRoot(sn.Root, root) {
			return sn, true
		}
	}
	return store.SnapshotInfo{}, false
}

func latestSnapshotForRoot(all []store.SnapshotInfo, root string) (store.SnapshotInfo, bool) {
	for _, sn := range all {
		if sameRoot(sn.Root, root) {
			return sn, true
		}
	}
	return store.SnapshotInfo{}, false
}

func sameRoot(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func checkTasks(ctx context.Context, add func(string, Level, string, string), acOnly bool) {
	for _, name := range []string{schedule.DailyName, schedule.WeeklyName} {
		checkName := "task " + name
		r, err := schedule.Query(ctx, name)
		if err != nil {
			add(checkName, LevelWarn, err.Error(), "run fat schedule status")
			continue
		}
		if !r.Exists {
			add(checkName, LevelWarn, "not installed", "run fat schedule install")
			continue
		}
		level := LevelOK
		var issues, hints []string
		if !r.Enabled {
			level = LevelWarn
			issues = append(issues, "disabled")
		}
		if !r.StartWhenAvailable {
			level = LevelWarn
			issues = append(issues, "missed runs are not caught up")
			hints = append(hints, "enable StartWhenAvailable or reinstall the task")
		}
		if acOnly && !r.DisallowStartIfOnBatteries {
			level = LevelWarn
			issues = append(issues, "not AC-only")
			hints = append(hints, "reinstall with fat schedule install --force after setting schedule.ac_only")
		}
		if r.Command == "" {
			level = LevelFail
			issues = append(issues, "no executable configured")
		} else if _, err := os.Stat(r.Command); err != nil {
			level = LevelFail
			issues = append(issues, "binary missing: "+err.Error())
			hints = append(hints, "rebuild or reinstall with fat schedule install --force")
		}
		if strings.EqualFold(r.RunLevel, "HighestAvailable") {
			if r.Command == "" || !underProgramFiles(r.Command) {
				level = LevelFail
				issues = append(issues, "elevated task points outside Program Files")
				hints = append(hints, "install the binary under %ProgramFiles% and restrict its ACL before elevating")
			}
		}
		summary := fmt.Sprintf("%s · %s · %s", r.Command, r.RunLevel, r.LogonType)
		if r.StartBoundary != "" {
			summary += " · start " + r.StartBoundary
		}
		if len(issues) > 0 {
			summary += " · " + strings.Join(issues, " · ")
		} else {
			summary += " · healthy"
		}
		add(checkName, level, summary, strings.Join(hints, "; "))
	}
}

func underProgramFiles(path string) bool {
	path = strings.ToLower(filepath.Clean(path))
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		rel, err := filepath.Rel(strings.ToLower(filepath.Clean(base)), path)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, `..\`) {
			return true
		}
	}
	return false
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%.0fm ago", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1fh ago", d.Hours())
	default:
		return fmt.Sprintf("%.1fd ago", d.Hours()/24)
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit && n > -unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	v := n
	if v < 0 {
		v = -v
	}
	for v/unit >= unit {
		div *= unit
		exp++
		v /= unit
	}
	sign := ""
	if n < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%.2f %ciB", sign, float64(abs(n))/float64(div), "KMGTPE"[exp])
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func formatMillis(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}
