package daily

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"fattrace/internal/model"
	"fattrace/internal/rules"
	"fattrace/internal/store"
)

type fixture struct {
	st   *store.Store
	path string
	raw  *sql.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daily.db")
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
	return &fixture{st: st, path: path, raw: raw}
}

func (f *fixture) commit(t *testing.T, vol int64, root, kind string, recs []model.FileRecord) int64 {
	t.Helper()
	ctx := context.Background()
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
	agg, err := f.st.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	var files, dirs, alloc int64
	for _, r := range recs {
		if r.IsDir {
			dirs++
		} else {
			files++
			alloc += r.AllocSize
		}
	}
	id, err := f.st.BeginSnapshot(ctx, vol, kind, root)
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	err = f.st.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID: id, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{
			Files: files, Dirs: dirs, Bytes: alloc, AllocBytes: alloc,
			UniqueBytes: alloc, UniqueAllocBytes: alloc,
			VolumeUsed: 1_000_000_000 + alloc, CoveragePct: 100,
		},
		Dirs: agg, UpdateBaseline: true,
	})
	if err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	return id
}

func (f *fixture) setStarted(t *testing.T, id int64, at time.Time) {
	t.Helper()
	if _, err := f.raw.Exec(`UPDATE snapshots SET started_at = ? WHERE id = ?`, at.UnixMilli(), id); err != nil {
		t.Fatalf("set started_at #%d: %v", id, err)
	}
}

func (f *fixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.raw.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func file(frn, parent uint64, name string, alloc int64) model.FileRecord {
	return model.FileRecord{FRN: frn, ParentFRN: parent, Name: name, Size: alloc, AllocSize: alloc}
}

func TestComputeDayBaselines(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xB1, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	base := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "data", IsDir: true},
	}
	loc := time.Local
	day1 := func(h, m int) time.Time { return time.Date(2026, 10, 27, h, m, 0, 0, loc) }
	day2 := func(h, m int) time.Time { return time.Date(2026, 10, 28, h, m, 0, 0, loc) }
	with := func(extra ...model.FileRecord) []model.FileRecord {
		out := append([]model.FileRecord{}, base...)
		return append(out, extra...)
	}

	snap1 := f.commit(t, vol, `D:\`, "daily", with(file(10, 2, "a.bin", 1000)))
	snap2 := f.commit(t, vol, `D:\`, "daily", with(file(10, 2, "a.bin", 1000), file(11, 2, "b.bin", 2000)))
	snap3 := f.commit(t, vol, `D:\`, "daily", with(file(10, 2, "a.bin", 1500), file(11, 2, "b.bin", 2000)))
	snap4 := f.commit(t, vol, `D:\`, "daily", with(file(10, 2, "a.bin", 1500), file(11, 2, "b.bin", 2000), file(12, 2, "c.bin", 3000)))
	f.setStarted(t, snap1, day1(9, 0))
	f.setStarted(t, snap2, day1(10, 0))
	f.setStarted(t, snap3, day2(9, 0))
	f.setStarted(t, snap4, day2(23, 0))

	vol2, err := f.st.EnsureVolume(ctx, 0xB2, "NTFS", "other")
	if err != nil {
		t.Fatal(err)
	}
	single := f.commit(t, vol2, `E:\`, "daily", []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `E:\`, IsDir: true},
		file(10, 1, "only.bin", 500),
	})
	f.setStarted(t, single, day1(8, 0))

	now := time.Date(2026, 10, 28, 23, 30, 0, 0, loc)
	rep, err := Compute(ctx, f.st, rules.MustBuiltin(), Options{Days: 7, TopN: 10, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Days) != 3 {
		t.Fatalf("days = %d, want 3 (%+v)", len(rep.Days), rep.Days)
	}
	// Newest first: D day2, D day1, E day1.
	if rep.Days[0].Summary.Day != "2026-10-28" || rep.Days[0].Summary.Root != `D:\` {
		t.Fatalf("first row = %+v", rep.Days[0].Summary)
	}
	d2 := rep.Days[0]
	if d2.FromID != snap2 || d2.ToID != snap4 {
		t.Fatalf("day2 baseline = #%d → #%d, want #%d → #%d", d2.FromID, d2.ToID, snap2, snap4)
	}
	if d2.Summary.DeltaBytes != 3500 {
		t.Fatalf("day2 delta = %d, want 3500", d2.Summary.DeltaBytes)
	}
	if d2.Summary.GrowthBytes != 3500 {
		t.Fatalf("day2 growth = %d, want 3500", d2.Summary.GrowthBytes)
	}
	if d2.Summary.SnapshotID != snap4 || d2.Summary.UsedBytes != 1_000_000_000+6500 {
		t.Fatalf("day2 summary = %+v", d2.Summary)
	}

	var d1 DayResult
	for _, r := range rep.Days {
		if r.Summary.Root == `D:\` && r.Summary.Day == "2026-10-27" {
			d1 = r
		}
	}
	if d1.FromID != snap1 || d1.ToID != snap2 {
		t.Fatalf("day1 baseline = #%d → #%d, want #%d → #%d", d1.FromID, d1.ToID, snap1, snap2)
	}
	if d1.Summary.DeltaBytes != 2000 {
		t.Fatalf("day1 delta = %d, want 2000 (intraday, not cross-day)", d1.Summary.DeltaBytes)
	}

	var e DayResult
	for _, r := range rep.Days {
		if r.Summary.Root == `E:\` {
			e = r
		}
	}
	if !e.SingleShot || e.Summary.DeltaBytes != 0 || e.Summary.GrowthBytes != 0 {
		t.Fatalf("single-shot day = %+v", e)
	}
	if len(e.TopDirs) != 0 || len(e.Events) != 0 {
		t.Fatalf("single-shot day has dirs/events: %+v", e)
	}

	summaries, err := f.st.ListDailySummaries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 3 {
		t.Fatalf("stored summaries = %d, want 3", len(summaries))
	}
	dirs, err := f.st.DailyTopDirs(ctx, vol, "2026-10-28", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no top dirs stored for day2")
	}
	found := false
	for _, d := range dirs {
		if strings.HasSuffix(strings.ToLower(d.Path), `\data`) && d.DeltaBytes == 3500 {
			found = true
		}
	}
	if !found {
		t.Fatalf("top dirs = %+v, want data 3500", dirs)
	}

	if n := f.count(t, `SELECT COUNT(*) FROM file_events WHERE snapshot_id = ?`, snap4); n != 2 {
		t.Fatalf("events for #%d = %d, want 2 (1 new + 1 grew)", snap4, n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM file_events WHERE snapshot_id = ?`, snap2); n != 1 {
		t.Fatalf("events for #%d = %d, want 1", snap2, n)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM file_events WHERE snapshot_id = ?`, single); n != 0 {
		t.Fatalf("single-shot events = %d", n)
	}

	// A stale event can survive from an earlier run whose older pair was then
	// pruned; recomputing the now-single day must remove it.
	if _, err := f.raw.Exec(`INSERT INTO file_events
		(volume_id, snapshot_id, kind, frn, path, size_before, size_after, delta_bytes, detected_at)
		VALUES (?, ?, 'new', 10, 'E:\only.bin', 0, 500, 500, ?)`,
		vol2, single, now.UnixMilli()); err != nil {
		t.Fatalf("insert stale event: %v", err)
	}
	if _, err := Compute(ctx, f.st, rules.MustBuiltin(), Options{Days: 7, TopN: 10, Now: now}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT COUNT(*) FROM file_events WHERE snapshot_id = ?`, single); n != 0 {
		t.Fatalf("stale single-shot events after recompute = %d", n)
	}
}

func TestComputeWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	vol, err := f.st.EnsureVolume(ctx, 0xB3, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	base := []model.FileRecord{{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true}}
	snap1 := f.commit(t, vol, `D:\`, "daily", append(append([]model.FileRecord{}, base...), file(10, 1, "a.bin", 1000)))
	snap2 := f.commit(t, vol, `D:\`, "daily", append(append([]model.FileRecord{}, base...), file(10, 1, "a.bin", 1000)))
	loc := time.Local
	f.setStarted(t, snap1, time.Date(2026, 10, 20, 10, 0, 0, 0, loc))
	f.setStarted(t, snap2, time.Date(2026, 10, 28, 10, 0, 0, 0, loc))

	now := time.Date(2026, 10, 28, 12, 0, 0, 0, loc)
	rep, err := Compute(ctx, f.st, rules.MustBuiltin(), Options{Days: 1, TopN: 10, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Days) != 1 || rep.Days[0].Summary.Day != "2026-10-28" {
		t.Fatalf("windowed days = %+v", rep.Days)
	}
	if rep.Days[0].FromID != snap1 {
		t.Fatalf("windowed baseline = #%d, want #%d", rep.Days[0].FromID, snap1)
	}
}
