package store

import (
	"context"
	"testing"

	"github.com/Xeayy/FatTrace/internal/model"
)

func commitStaged(t *testing.T, s *Store, volumeID int64, recs []model.FileRecord) int64 {
	t.Helper()
	ctx := context.Background()
	w, err := s.BeginScan(ctx, volumeID)
	if err != nil {
		t.Fatalf("begin scan: %v", err)
	}
	for _, r := range recs {
		if err := w.Add(ctx, r); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	n, err := w.Commit(ctx)
	if err != nil {
		t.Fatalf("commit staging: %v", err)
	}
	return n
}

func TestAggregateDirs(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 7, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	recs := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "a", IsDir: true},
		{FRN: 3, ParentFRN: 2, Name: "b", IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "f1", Size: 10, AllocSize: 4096},
		{FRN: 11, ParentFRN: 2, Name: "f2", Size: 20, AllocSize: 4096},
		{FRN: 12, ParentFRN: 3, Name: "f3", Size: 30, AllocSize: 4096},
		{FRN: 10, ParentFRN: 3, Name: "f1-link", Size: 10, AllocSize: 4096},
	}
	if n := commitStaged(t, s, vol, recs); n != 6 {
		t.Fatalf("staged rows = %d, want 6 (hard link deduplicated)", n)
	}
	dirs, err := s.AggregateDirs(ctx, vol, map[uint64]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	byFRN := map[uint64]DirTotal{}
	for _, d := range dirs {
		byFRN[d.FRN] = d
	}
	root, a, b := byFRN[1], byFRN[2], byFRN[3]
	if root.Depth != 0 || a.Depth != 1 || b.Depth != 2 {
		t.Fatalf("depths = %d/%d/%d, want 0/1/2", root.Depth, a.Depth, b.Depth)
	}
	if root.Files != 3 || root.Bytes != 60 || root.AllocBytes != 3*4096 {
		t.Fatalf("root totals = files %d bytes %d alloc %d", root.Files, root.Bytes, root.AllocBytes)
	}
	if a.Files != 2 || a.Bytes != 50 {
		t.Fatalf("a totals = files %d bytes %d", a.Files, a.Bytes)
	}
	if b.Files != 1 || b.Bytes != 30 || !b.Unknown {
		t.Fatalf("b totals = files %d bytes %d unknown %v", b.Files, b.Bytes, b.Unknown)
	}
	if !root.Unknown || !a.Unknown {
		t.Fatal("unknown must propagate to ancestors")
	}
}

func TestSnapshotBaselineSwap(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 9, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	dirs := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true, Attrs: 0x10},
		{FRN: 2, ParentFRN: 1, Name: "a", IsDir: true, Attrs: 0x10},
	}
	files1 := []model.FileRecord{
		{FRN: 10, ParentFRN: 1, Name: "f1", Size: 100, AllocSize: 4096},
		{FRN: 11, ParentFRN: 2, Name: "f2", Size: 200, AllocSize: 4096},
	}
	commitStaged(t, s, vol, append(append([]model.FileRecord{}, dirs...), files1...))
	agg1, err := s.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap1, err := s.BeginSnapshot(ctx, vol, "weekly", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitSnapshot(ctx, SnapshotCommit{
		SnapshotID: snap1, VolumeID: vol, Status: "complete",
		Stats:          model.ScanStats{Files: 2, Dirs: 2, Bytes: 300, AllocBytes: 8192, DurationMS: 5},
		Dirs:           agg1,
		UpdateBaseline: true,
	}); err != nil {
		t.Fatalf("commit 1: %v", err)
	}

	if n := countRows(t, s, "SELECT COUNT(*) FROM current_files WHERE volume_id = ?", vol); n != 2 {
		t.Fatalf("baseline files = %d, want 2", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM current_dirs WHERE volume_id = ?", vol); n != 2 {
		t.Fatalf("baseline dirs = %d, want 2", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?", snap1); n != 2 {
		t.Fatalf("snap_files = %d, want 2", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ?", snap1); n != 2 {
		t.Fatalf("snap_dirs = %d, want 2", n)
	}
	var rootBytes int64
	if err := s.db.QueryRow("SELECT total_bytes FROM snap_dirs WHERE snapshot_id = ? AND frn = 1", snap1).Scan(&rootBytes); err != nil {
		t.Fatal(err)
	}
	if rootBytes != 300 {
		t.Fatalf("snapshot root bytes = %d, want 300", rootBytes)
	}

	// Second scan: f1 gone, f2 grew, f3 added.
	files2 := []model.FileRecord{
		{FRN: 11, ParentFRN: 2, Name: "f2", Size: 250, AllocSize: 4096},
		{FRN: 12, ParentFRN: 1, Name: "f3", Size: 50, AllocSize: 4096},
	}
	commitStaged(t, s, vol, append(append([]model.FileRecord{}, dirs...), files2...))
	agg2, err := s.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := s.BeginSnapshot(ctx, vol, "weekly", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitSnapshot(ctx, SnapshotCommit{
		SnapshotID: snap2, VolumeID: vol, Status: "complete",
		Stats:          model.ScanStats{Files: 2, Dirs: 2, Bytes: 300},
		Dirs:           agg2,
		UpdateBaseline: true,
	}); err != nil {
		t.Fatalf("commit 2: %v", err)
	}

	if n := countRows(t, s, "SELECT COUNT(*) FROM current_files WHERE volume_id = ?", vol); n != 2 {
		t.Fatalf("baseline files after swap = %d, want 2", n)
	}
	var size int64
	if err := s.db.QueryRow("SELECT size FROM current_files WHERE volume_id = ? AND frn = 11", vol).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != 250 {
		t.Fatalf("f2 size = %d, want 250", size)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?", snap1); n != 2 {
		t.Fatalf("snapshot 1 rows were lost: %d", n)
	}
}

func TestPartialKeepsBaseline(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 11, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	dirs := []model.FileRecord{{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true}}
	files := []model.FileRecord{{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 4096}}
	commitStaged(t, s, vol, append(append([]model.FileRecord{}, dirs...), files...))
	agg, err := s.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.BeginSnapshot(ctx, vol, "manual", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CommitSnapshot(ctx, SnapshotCommit{
		SnapshotID: snap, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{Files: 1, Dirs: 1}, Dirs: agg, UpdateBaseline: true,
	}); err != nil {
		t.Fatal(err)
	}

	// A new scan is interrupted before commit.
	commitStaged(t, s, vol, append(append([]model.FileRecord{}, dirs...),
		model.FileRecord{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 4096},
		model.FileRecord{FRN: 12, ParentFRN: 1, Name: "new.bin", Size: 999, AllocSize: 4096}))
	partial, err := s.BeginSnapshot(ctx, vol, "manual", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSnapshot(ctx, partial, "partial", model.ScanStats{Files: 1, Dirs: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearScan(ctx, vol); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, s, "SELECT COUNT(*) FROM current_files WHERE volume_id = ?", vol); n != 1 {
		t.Fatalf("baseline files = %d after partial, want 1", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?", partial); n != 0 {
		t.Fatalf("partial snapshot wrote %d file rows, want 0", n)
	}
	var status string
	if err := s.db.QueryRow("SELECT status FROM snapshots WHERE id = ?", partial).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "partial" {
		t.Fatalf("status = %q, want partial", status)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM scan_files WHERE volume_id = ?", vol); n != 0 {
		t.Fatalf("staging rows = %d after clear, want 0", n)
	}
}

func TestStaleRunningSnapshotMarkedFailed(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 13, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginSnapshot(ctx, vol, "manual", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginSnapshot(ctx, vol, "manual", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.db.QueryRow("SELECT status FROM snapshots WHERE id = ?", first).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("stale snapshot status = %q, want failed", status)
	}
	if err := s.db.QueryRow("SELECT status FROM snapshots WHERE id = ?", second).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("new snapshot status = %q, want running", status)
	}
}

func TestScanErrorsUpsert(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 15, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	errs := []model.ScanError{{Path: `C:\secret`, FRN: 5, Op: "open", Msg: "access denied"}}
	commit := func() int64 {
		dirs := []model.FileRecord{{FRN: 1, ParentFRN: 0, Name: `C:\`, IsDir: true}}
		commitStaged(t, s, vol, dirs)
		agg, err := s.AggregateDirs(ctx, vol, map[uint64]bool{5: true})
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.BeginSnapshot(ctx, vol, "weekly", `C:\`)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CommitSnapshot(ctx, SnapshotCommit{
			SnapshotID: id, VolumeID: vol, Status: "complete",
			Stats: model.ScanStats{Files: 0, Dirs: 1, Errors: 1}, Dirs: agg, Errors: errs,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := commit()
	second := commit()
	if n := countRows(t, s, "SELECT COUNT(*) FROM scan_errors"); n != 1 {
		t.Fatalf("scan_errors rows = %d, want 1", n)
	}
	var hits, lastSnap int64
	if err := s.db.QueryRow("SELECT hits, last_snapshot_id FROM scan_errors").Scan(&hits, &lastSnap); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2", hits)
	}
	if lastSnap != second || lastSnap == first {
		t.Fatalf("last_snapshot_id = %d, want %d", lastSnap, second)
	}
}

func TestCommitRequiresCompleteStatus(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 17, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.BeginSnapshot(ctx, vol, "manual", `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	err = s.CommitSnapshot(ctx, SnapshotCommit{SnapshotID: id, VolumeID: vol, Status: "partial"})
	if err == nil {
		t.Fatal("CommitSnapshot accepted a partial status")
	}
}

func TestSnapshotErrorsBySnapshot(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 19, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	errs := []model.ScanError{{Path: `C:\secret`, FRN: 5, Op: "open", Msg: "access denied"}}
	commit := func() int64 {
		dirs := []model.FileRecord{{FRN: 1, ParentFRN: 0, Name: `C:\`, IsDir: true}}
		commitStaged(t, s, vol, dirs)
		agg, err := s.AggregateDirs(ctx, vol, nil)
		if err != nil {
			t.Fatal(err)
		}
		id, err := s.BeginSnapshot(ctx, vol, "weekly", `C:\`)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.CommitSnapshot(ctx, SnapshotCommit{
			SnapshotID: id, VolumeID: vol, Status: "complete",
			Stats: model.ScanStats{Dirs: 1, Errors: 1}, Dirs: agg, Errors: errs,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := commit()
	got, err := s.SnapshotErrors(ctx, first, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hits != 1 || got[0].Msg != "access denied" || got[0].Op != "open" {
		t.Fatalf("first snapshot errors = %+v", got)
	}

	second := commit()
	got, err = s.SnapshotErrors(ctx, first, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("errors attributed to the first snapshot after a later scan: %+v", got)
	}
	got, err = s.SnapshotErrors(ctx, second, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Hits != 2 {
		t.Fatalf("second snapshot errors = %+v, want hits 2", got)
	}
}
