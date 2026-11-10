package health

import (
	"strings"
	"testing"
	"time"

	"fattrace/internal/store"
)

func snap(id int64, status string, started time.Time, files, dirs, errors int64) store.SnapshotInfo {
	finished := started.Add(time.Minute).UnixMilli()
	return store.SnapshotInfo{
		ID: id, Kind: "daily", Status: status, Root: `D:\`,
		StartedAt: started.UnixMilli(), FinishedAt: &finished,
		Files: files, Dirs: dirs, Errors: errors,
	}
}

func TestEvaluateHealthy(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(3, "failed", now.Add(-1*time.Hour), 0, 0, 0),
		snap(2, "complete", now.Add(-26*time.Hour), 100, 10, 0),
	}
	// The failed run is newer than the complete one; one failure is a warning.
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	if got := rep.Roots[0]; got.Level != LevelWarn || got.ConsecutiveFailures != 1 {
		t.Fatalf("one failure = %+v", got)
	}
	if !strings.Contains(rep.Roots[0].Reason, "进程在完成前退出") {
		t.Fatalf("reason = %q", rep.Roots[0].Reason)
	}
}

func TestEvaluateConsecutiveFailures(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(4, "failed", now.Add(-1*time.Hour), 0, 0, 0),
		snap(3, "partial", now.Add(-2*time.Hour), 50, 5, 2),
		snap(2, "complete", now.Add(-26*time.Hour), 100, 10, 0),
	}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelDanger || got.ConsecutiveFailures != 2 {
		t.Fatalf("two failures = %+v", got)
	}
	if got.LastFailure == nil || got.LastFailure.ID != 4 {
		t.Fatalf("last failure = %+v", got.LastFailure)
	}
}

func TestEvaluateIgnoresFailuresBeforeLatestComplete(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(3, "complete", now.Add(-2*time.Hour), 100, 10, 0),
		snap(2, "failed", now.Add(-10*time.Hour), 0, 0, 0),
		snap(1, "complete", now.Add(-26*time.Hour), 90, 10, 0),
	}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelOK || got.ConsecutiveFailures != 0 {
		t.Fatalf("healthy root = %+v", got)
	}
	if got.LastFailure == nil || got.LastFailure.ID != 2 || got.LastFailureReason == "" {
		t.Fatalf("historical failure = %+v reason=%q", got.LastFailure, got.LastFailureReason)
	}
	if got.Reason != "" {
		t.Fatalf("healthy reason = %q", got.Reason)
	}
	if got.LastComplete == nil || got.LastComplete.ID != 3 {
		t.Fatalf("last complete = %+v", got.LastComplete)
	}
}

func TestEvaluateStale(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{snap(2, "complete", now.Add(-48*time.Hour), 100, 10, 0)}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelDanger || !got.Stale {
		t.Fatalf("stale = %+v", got)
	}
	if !strings.Contains(got.Reason, "48.0 小时") && !strings.Contains(got.Reason, "48.0") {
		t.Fatalf("stale reason = %q", got.Reason)
	}
}

func TestEvaluateNoCompleteSnapshot(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{snap(2, "failed", now.Add(-1*time.Hour), 0, 0, 0)}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelDanger || got.LastComplete != nil {
		t.Fatalf("no complete = %+v", got)
	}
	if !strings.Contains(got.Reason, "还没有完成过一次") {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestEvaluateRunningIsInProgress(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(3, "running", now.Add(-10*time.Minute), 0, 0, 0),
		snap(2, "complete", now.Add(-26*time.Hour), 100, 10, 0),
	}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelOK || got.ConsecutiveFailures != 0 {
		t.Fatalf("running = %+v", got)
	}
	if got.Reason != "扫描进行中" {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestEvaluateStaleRunningIsFailure(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(3, "running", now.Add(-8*time.Hour), 0, 0, 0),
		snap(2, "complete", now.Add(-26*time.Hour), 100, 10, 0),
	}
	rep := Evaluate(list, []string{`D:\`}, 36*time.Hour, now)
	got := rep.Roots[0]
	if got.Level != LevelWarn || got.ConsecutiveFailures != 1 {
		t.Fatalf("stale running = %+v", got)
	}
}

func TestEvaluateIgnoresSubdirectorySnapshots(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	clean := snap(2, "complete", now.Add(-2*time.Hour), 100, 10, 0)
	sub := snap(3, "failed", now.Add(-1*time.Hour), 0, 0, 0)
	sub.Root = `D:\Work`
	rep := Evaluate([]store.SnapshotInfo{sub, clean}, []string{`D:\`}, 36*time.Hour, now)
	if got := rep.Roots[0]; got.Level != LevelOK || got.ConsecutiveFailures != 0 {
		t.Fatalf("subdirectory snapshot leaked into root health: %+v", got)
	}
}

func TestEvaluateWorstLevel(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)
	list := []store.SnapshotInfo{
		snap(2, "complete", now.Add(-2*time.Hour), 100, 10, 0),
	}
	e := snap(3, "failed", now.Add(-1*time.Hour), 0, 0, 0)
	e.Root = `E:\`
	ec := snap(1, "complete", now.Add(-2*time.Hour), 50, 5, 0)
	ec.Root = `E:\`
	rep := Evaluate(append(list, e, ec), []string{`D:\`, `E:\`}, 36*time.Hour, now)
	if rep.Level != LevelWarn || len(rep.Roots) != 2 {
		t.Fatalf("report = %+v", rep)
	}
}
