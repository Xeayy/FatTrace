//go:build windows

package doctor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/store"
)

type fixture struct {
	dir  string
	db   string
	root string
	now  time.Time
}

func newFixture(t *testing.T, age time.Duration, files int64) fixture {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fattrace.db")
	root := filepath.Join(dir, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cfg := config.Defaults()
	cfg.Schedule.Roots = []string{root}
	if err := config.Save(filepath.Join(dir, config.FileName), cfg); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	vol, err := st.EnsureVolume(context.Background(), 0xD0C, "NTFS", "doctor")
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	started := now.Add(-age)
	_, err = raw.Exec(`INSERT INTO snapshots
		(volume_id, kind, status, root, started_at, finished_at, duration_ms,
		 files, dirs, unique_alloc_bytes, volume_used, errors, coverage_pct)
		VALUES (?, 'daily', 'complete', ?, ?, ?, 1000, ?, 1, 4096, 100000, 0, 99.5)`,
		vol, root, started.UnixMilli(), started.Add(time.Second).UnixMilli(), files)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{dir: dir, db: dbPath, root: root, now: now}
}

func findCheck(t *testing.T, rep Report, name string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", name, rep.Checks)
	return Check{}
}

func TestRunHealthy(t *testing.T) {
	f := newFixture(t, 2*time.Hour, 0)
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	if rep.Failed() {
		t.Fatalf("healthy report failed: %+v", rep.Checks)
	}
	if c := findCheck(t, rep, "config"); c.Level != LevelOK {
		t.Fatalf("config = %+v", c)
	}
	if c := findCheck(t, rep, "integrity"); c.Level != LevelOK {
		t.Fatalf("integrity = %+v", c)
	}
	snap := findCheck(t, rep, "snapshot "+f.root)
	if snap.Level != LevelOK {
		t.Fatalf("snapshot = %+v", snap)
	}
	if c := findCheck(t, rep, "tasks"); c.Level != LevelSkip {
		t.Fatalf("tasks = %+v", c)
	}
	if rep.Elevated {
		t.Log("running elevated; permission check is informational")
	}
}

func TestRunStaleAndExpired(t *testing.T) {
	f := newFixture(t, 48*time.Hour, 5)
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	snap := findCheck(t, rep, "snapshot "+f.root)
	if snap.Level != LevelFail {
		t.Fatalf("stale snapshot level = %s (%s)", snap.Level, snap.Summary)
	}
	if !strings.Contains(snap.Summary, "days ago") || !strings.Contains(snap.Summary, "file rows pruned") {
		t.Fatalf("stale summary = %q", snap.Summary)
	}
	if !strings.Contains(snap.Hint, "fat maintain") {
		t.Fatalf("stale hint = %q", snap.Hint)
	}
}

func TestRunConsecutiveFailures(t *testing.T) {
	f := newFixture(t, 2*time.Hour, 100)
	raw, err := sql.Open("sqlite", f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for i, age := range []time.Duration{time.Hour, 30 * time.Minute} {
		started := f.now.Add(-age)
		if _, err := raw.Exec(`INSERT INTO snapshots
			(volume_id, kind, status, root, started_at, finished_at, duration_ms,
			 files, dirs, unique_alloc_bytes, volume_used, errors, coverage_pct)
			VALUES ((SELECT id FROM volumes LIMIT 1), 'daily', 'failed', ?, ?, ?, 1000, 0, 0, 0, 0, 0, 0)`,
			f.root, started.UnixMilli(), started.Add(time.Second).UnixMilli()); err != nil {
			t.Fatalf("insert failure %d: %v", i, err)
		}
	}
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	snap := findCheck(t, rep, "snapshot "+f.root)
	if snap.Level != LevelFail {
		t.Fatalf("consecutive failures level = %s (%s)", snap.Level, snap.Summary)
	}
	if !strings.Contains(snap.Summary, "连续失败 2 次") {
		t.Fatalf("summary = %q", snap.Summary)
	}
	if !strings.Contains(snap.Hint, "进程在完成前退出") {
		t.Fatalf("hint = %q", snap.Hint)
	}
}

func TestRunMissingRoot(t *testing.T) {
	f := newFixture(t, time.Hour, 0)
	missing := filepath.Join(f.dir, "missing")
	cfg := config.Defaults()
	cfg.Schedule.Roots = []string{missing}
	if err := config.Save(filepath.Join(f.dir, config.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	if !rep.Failed() {
		t.Fatalf("missing root did not fail: %+v", rep.Checks)
	}
	if c := findCheck(t, rep, "snapshot "+missing); c.Level != LevelFail {
		t.Fatalf("missing root check = %+v", c)
	}
}

func TestRunFreshDatabaseIsWarning(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fattrace.db")
	root := filepath.Join(dir, "data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Schedule.Roots = []string{root}
	if err := config.Save(filepath.Join(dir, config.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	rep := Run(context.Background(), Options{DBPath: dbPath, Now: time.Now(), SkipTasks: true})
	if rep.Failed() {
		t.Fatalf("fresh database should not fail: %+v", rep.Checks)
	}
	snap := findCheck(t, rep, "snapshot "+root)
	if snap.Level != LevelWarn {
		t.Fatalf("fresh snapshot check = %+v", snap)
	}
	if !strings.Contains(snap.Summary, "no snapshot yet") {
		t.Fatalf("fresh summary = %q", snap.Summary)
	}
	if !strings.Contains(snap.Hint, "fat snap") {
		t.Fatalf("fresh hint = %q", snap.Hint)
	}
}

func TestRunMissingSnapshotForExistingRootFails(t *testing.T) {
	f := newFixture(t, time.Hour, 0)
	other := filepath.Join(f.dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Schedule.Roots = []string{f.root, other}
	if err := config.Save(filepath.Join(f.dir, config.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	if c := findCheck(t, rep, "snapshot "+other); c.Level != LevelFail {
		t.Fatalf("unscanned root should fail: %+v", c)
	}
}

func TestRunMalformedConfig(t *testing.T) {
	f := newFixture(t, time.Hour, 0)
	if err := os.WriteFile(filepath.Join(f.dir, config.FileName),
		[]byte("schedule:\n  daily_at: \"03:00\"\n  typo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := Run(context.Background(), Options{DBPath: f.db, Now: f.now, SkipTasks: true})
	if c := findCheck(t, rep, "config"); c.Level != LevelFail {
		t.Fatalf("config = %+v", c)
	}
	if c := findCheck(t, rep, "database"); c.Level != LevelOK {
		t.Fatalf("database should still be checked after a config error: %+v", c)
	}
}

func TestUnderProgramFiles(t *testing.T) {
	base := t.TempDir()
	t.Setenv("ProgramFiles", base)
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("ProgramW6432", "")
	if !underProgramFiles(filepath.Join(base, "FatTrace", "fat.exe")) {
		t.Fatal("path inside ProgramFiles was rejected")
	}
	if underProgramFiles(filepath.Join(t.TempDir(), "fat.exe")) {
		t.Fatal("path outside ProgramFiles was accepted")
	}
}
