package prune

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"fattrace/internal/config"
	"fattrace/internal/model"
	"fattrace/internal/store"
)

type fixture struct {
	st  *store.Store
	raw *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prune.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	raw, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		st.Close()
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() {
		raw.Close()
		st.Close()
	})
	return &fixture{st: st, raw: raw}
}

func (f *fixture) snapshot(t *testing.T, vol int64, kind, status string, at time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	recs := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "a.bin", Size: 100, AllocSize: 1000},
	}
	w, err := f.st.BeginScan(ctx, vol)
	if err != nil {
		t.Fatalf("begin scan: %v", err)
	}
	for _, r := range recs {
		if err := w.Add(ctx, r); err != nil {
			t.Fatalf("stage: %v", err)
		}
	}
	if _, err := w.Commit(ctx); err != nil {
		t.Fatalf("commit staging: %v", err)
	}
	id, err := f.st.BeginSnapshot(ctx, vol, kind, `D:\`)
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	if status == "complete" {
		agg, err := f.st.AggregateDirs(ctx, vol, nil)
		if err != nil {
			t.Fatalf("aggregate: %v", err)
		}
		err = f.st.CommitSnapshot(ctx, store.SnapshotCommit{
			SnapshotID: id, VolumeID: vol, Status: "complete",
			Stats: model.ScanStats{Files: 1, Dirs: 1, Bytes: 100, AllocBytes: 1000,
				UniqueBytes: 100, UniqueAllocBytes: 1000},
			Dirs: agg, UpdateBaseline: false,
		})
		if err != nil {
			t.Fatalf("commit snapshot: %v", err)
		}
	} else {
		if err := f.st.FinishSnapshot(ctx, id, status, model.ScanStats{}); err != nil {
			t.Fatalf("finish snapshot: %v", err)
		}
		if err := f.st.ClearScan(ctx, vol); err != nil {
			t.Fatalf("clear staging: %v", err)
		}
	}
	if _, err := f.raw.Exec(`UPDATE snapshots SET started_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		t.Fatalf("set started_at: %v", err)
	}
	return id
}

func (f *fixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.raw.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestRunDryRunThenApply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xC1, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	days := func(n int) time.Time { return now.AddDate(0, 0, -n) }

	dailyOld := f.snapshot(t, vol, "daily", "complete", days(100))
	dailyMid := f.snapshot(t, vol, "daily", "complete", days(40))
	dailyPartial := f.snapshot(t, vol, "daily", "partial", days(50))
	dailyKeep := f.snapshot(t, vol, "daily", "complete", days(5))
	dailyNewest := f.snapshot(t, vol, "daily", "complete", days(1))
	weeklyOld := f.snapshot(t, vol, "weekly", "complete", days(400))
	weeklyNew := f.snapshot(t, vol, "weekly", "complete", days(100))
	manualOld := f.snapshot(t, vol, "manual", "complete", days(300))

	cfg := config.Retention{
		DailySnapshotsDays: 30, WeeklySnapshotsDays: 365,
		DailySummaryDays: 90, FileEventsDays: 56,
	}
	if err := f.st.SaveDailySummary(ctx, model.DailySummary{
		VolumeID: vol, Root: `D:\`, Day: "2026-06-01", SnapshotID: dailyOld,
		UsedBytes: 1, DeltaBytes: 1,
	}, []model.DailyTopDir{{VolumeID: vol, Day: "2026-06-01", Rank: 1, Path: `D:\old`, DeltaBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveDailySummary(ctx, model.DailySummary{
		VolumeID: vol, Root: `D:\`, Day: "2026-10-28", SnapshotID: dailyNewest,
		UsedBytes: 1, DeltaBytes: 1,
	}, nil); err != nil {
		t.Fatal(err)
	}
	oldEvent := model.FileEvent{
		VolumeID: vol, SnapshotID: weeklyNew, Kind: "new", FRN: 10, Path: `D:\old.bin`,
		SizeAfter: 100, DetectedAt: days(100).UnixMilli(),
	}
	newEvent := model.FileEvent{
		VolumeID: vol, SnapshotID: weeklyNew, Kind: "new", FRN: 11, Path: `D:\new.bin`,
		SizeAfter: 100, DetectedAt: days(10).UnixMilli(),
	}
	if err := f.st.ReplaceFileEvents(ctx, weeklyNew, []model.FileEvent{oldEvent, newEvent}); err != nil {
		t.Fatal(err)
	}

	before := f.count(t, `SELECT COUNT(*) FROM snapshots`)
	rep, err := Run(ctx, f.st, cfg, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied {
		t.Fatal("dry run reports applied")
	}
	if len(rep.Snapshots) != 4 {
		t.Fatalf("candidates = %+v, want 4", rep.Snapshots)
	}
	want := map[int64]bool{dailyOld: true, dailyMid: true, dailyPartial: true, weeklyOld: true}
	for _, sn := range rep.Snapshots {
		if !want[sn.ID] {
			t.Fatalf("unexpected candidate #%d (%s %s)", sn.ID, sn.Kind, sn.Root)
		}
		delete(want, sn.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing candidates: %v", want)
	}
	if rep.Summaries != 1 || rep.TopDirs != 1 || rep.FileEvents != 1 {
		t.Fatalf("counts = summaries %d dirs %d events %d", rep.Summaries, rep.TopDirs, rep.FileEvents)
	}
	if rep.Deleted.Snapshots != 0 || rep.Deleted.Summaries != 0 || rep.Deleted.FileEvents != 0 {
		t.Fatalf("dry run deleted rows: %+v", rep.Deleted)
	}
	if after := f.count(t, `SELECT COUNT(*) FROM snapshots`); after != before {
		t.Fatalf("dry run removed snapshots: %d -> %d", before, after)
	}

	rep, err = Run(ctx, f.st, cfg, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || rep.Deleted.Snapshots != 4 || rep.Deleted.Summaries != 1 ||
		rep.Deleted.TopDirs != 1 || rep.Deleted.FileEvents != 1 {
		t.Fatalf("applied report = %+v", rep)
	}
	for _, id := range []int64{dailyOld, dailyMid, dailyPartial, weeklyOld} {
		if f.count(t, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, id) != 0 {
			t.Fatalf("snapshot #%d survived", id)
		}
	}
	for _, id := range []int64{dailyKeep, dailyNewest, weeklyNew, manualOld} {
		if f.count(t, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, id) != 1 {
			t.Fatalf("snapshot #%d was removed", id)
		}
	}
	if n := f.count(t, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, dailyMid); n != 0 {
		t.Fatalf("snap_files cascade left %d rows", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM daily_summary`); n != 1 {
		t.Fatalf("daily summaries after prune = %d, want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM daily_top_dirs`); n != 0 {
		t.Fatalf("daily top dirs after prune = %d, want 0", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM file_events`); n != 1 {
		t.Fatalf("file events after prune = %d, want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM file_events WHERE path = ?`, `D:\new.bin`); n != 1 {
		t.Fatal("recent event was pruned")
	}
}

func TestFileRowRetentionKeepsSnapshotsAndDirs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xC3, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	days := func(n int) time.Time { return now.AddDate(0, 0, -n) }

	dailyOld := f.snapshot(t, vol, "daily", "complete", days(40))
	dailyExpired := f.snapshot(t, vol, "daily", "complete", days(11))
	dailySecond := f.snapshot(t, vol, "daily", "complete", days(10))
	dailyNewest := f.snapshot(t, vol, "daily", "complete", days(9))
	weeklyOld := f.snapshot(t, vol, "weekly", "complete", days(60))
	weeklySecond := f.snapshot(t, vol, "weekly", "complete", days(30))
	weeklyNewest := f.snapshot(t, vol, "weekly", "complete", days(2))
	manualOld := f.snapshot(t, vol, "manual", "complete", days(100))

	cfg := config.Retention{
		DailySnapshotsDays: 90, WeeklySnapshotsDays: 365,
		DailyFileDays: 7, WeeklyFileWeeks: 3,
		DailySummaryDays: 90, FileEventsDays: 56,
	}
	rep, err := Run(ctx, f.st, cfg, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Snapshots) != 0 {
		t.Fatalf("full-snapshot candidates = %+v, want none", rep.Snapshots)
	}
	want := map[int64]bool{dailyOld: true, dailyExpired: true, weeklyOld: true}
	if len(rep.FileSnapshotIDs) != len(want) {
		t.Fatalf("file candidates = %v, want %v", rep.FileSnapshotIDs, want)
	}
	for _, id := range rep.FileSnapshotIDs {
		if !want[id] {
			t.Fatalf("unexpected file candidate #%d", id)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Fatalf("missing file candidates: %v", want)
	}
	if rep.FileRows != 3 || rep.Deleted.FileRows != 0 {
		t.Fatalf("dry-run file rows = planned %d deleted %d", rep.FileRows, rep.Deleted.FileRows)
	}
	for _, id := range []int64{dailyOld, dailyExpired, weeklyOld} {
		if n := f.count(t, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, id); n != 1 {
			t.Fatalf("dry run removed file rows of #%d", id)
		}
	}

	rep, err = Run(ctx, f.st, cfg, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted.FileRows != 3 || rep.Deleted.Snapshots != 0 {
		t.Fatalf("applied deletions = %+v", rep.Deleted)
	}
	for _, id := range []int64{dailyOld, dailyExpired, weeklyOld} {
		if n := f.count(t, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, id); n != 0 {
			t.Fatalf("snap_files of #%d survived", id)
		}
		if n := f.count(t, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, id); n != 1 {
			t.Fatalf("snapshot #%d was removed", id)
		}
		if n := f.count(t, `SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ?`, id); n != 1 {
			t.Fatalf("snap_dirs of #%d = %d, want 1", id, n)
		}
	}
	for _, id := range []int64{dailySecond, dailyNewest, weeklySecond, weeklyNewest, manualOld} {
		if n := f.count(t, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, id); n != 1 {
			t.Fatalf("protected snapshot #%d lost file rows", id)
		}
	}
}

func TestPruneKeepsNewestCompleteAfterNewerPartialRun(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xC4, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	older := f.snapshot(t, vol, "daily", "complete", now.AddDate(0, 0, -50))
	complete := f.snapshot(t, vol, "daily", "complete", now.AddDate(0, 0, -40))
	partial := f.snapshot(t, vol, "daily", "partial", now.AddDate(0, 0, -2))
	cfg := config.Retention{DailySnapshotsDays: 30}

	rep, err := Run(ctx, f.st, cfg, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Snapshots) != 1 || rep.Snapshots[0].ID != older {
		t.Fatalf("candidates = %+v, want only #%d", rep.Snapshots, older)
	}

	rep, err = Run(ctx, f.st, cfg, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted.Snapshots != 1 {
		t.Fatalf("deleted snapshots = %d, want 1", rep.Deleted.Snapshots)
	}
	for _, id := range []int64{complete, partial} {
		if f.count(t, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, id) != 1 {
			t.Fatalf("snapshot #%d was pruned", id)
		}
	}
}

func TestPruneCountsSummariesOnlyWhenEnabled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xC5, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	sn := f.snapshot(t, vol, "daily", "complete", now.AddDate(0, 0, -40))
	if err := f.st.SaveDailySummary(ctx, model.DailySummary{
		VolumeID: vol, Root: `D:\`, Day: "2026-06-01", SnapshotID: sn,
		UsedBytes: 1, DeltaBytes: 1,
	}, []model.DailyTopDir{{VolumeID: vol, Day: "2026-06-01", Rank: 1, Path: `D:\old`, DeltaBytes: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ReplaceFileEvents(ctx, sn, []model.FileEvent{
		{VolumeID: vol, SnapshotID: sn, Kind: "new", FRN: 10, Path: `D:\old.bin`,
			SizeAfter: 100, DetectedAt: now.AddDate(0, 0, -100).UnixMilli()},
		{VolumeID: vol, SnapshotID: sn, Kind: "new", FRN: 11, Path: `D:\new.bin`,
			SizeAfter: 100, DetectedAt: now.AddDate(0, 0, -10).UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Retention{FileEventsDays: 56}

	rep, err := Run(ctx, f.st, cfg, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.SummaryCutoff != "" || rep.Summaries != 0 || rep.TopDirs != 0 || rep.FileEvents != 1 {
		t.Fatalf("dry-run counts = cutoff %q summaries %d dirs %d events %d",
			rep.SummaryCutoff, rep.Summaries, rep.TopDirs, rep.FileEvents)
	}

	rep, err = Run(ctx, f.st, cfg, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted.Summaries != 0 || rep.Deleted.TopDirs != 0 || rep.Deleted.FileEvents != 1 {
		t.Fatalf("applied deletions = %+v", rep.Deleted)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM daily_summary`); n != 1 {
		t.Fatalf("daily summaries = %d, want 1", n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM daily_top_dirs`); n != 1 {
		t.Fatalf("daily top dirs = %d, want 1", n)
	}
}

func TestZeroRetentionDisablesDimensions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xC2, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	old := f.snapshot(t, vol, "daily", "complete", now.AddDate(0, 0, -400))
	rep, err := Run(ctx, f.st, config.Retention{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Snapshots) != 0 || rep.DailyCutoff != "" || rep.WeeklyCutoff != "" ||
		rep.SummaryCutoff != "" || rep.EventsCutoffMS != 0 {
		t.Fatalf("zero retention planned deletions: %+v", rep)
	}
	if f.count(t, `SELECT COUNT(*) FROM snapshots WHERE id = ?`, old) != 1 {
		t.Fatal("zero retention removed a snapshot")
	}
}
