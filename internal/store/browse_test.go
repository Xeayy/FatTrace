package store

import (
	"context"
	"errors"
	"testing"

	"fattrace/internal/model"
)

func seedBrowseSnapshot(t *testing.T, s *Store, vol int64, root, status string, unknown map[uint64]bool, recs []model.FileRecord) int64 {
	t.Helper()
	ctx := context.Background()
	n := commitStaged(t, s, vol, recs)
	agg, err := s.AggregateDirs(ctx, vol, unknown)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	id, err := s.BeginSnapshot(ctx, vol, "manual", root)
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	if status != "complete" {
		if err := s.FinishSnapshot(ctx, id, status, model.ScanStats{}); err != nil {
			t.Fatalf("finish snapshot: %v", err)
		}
		if err := s.ClearScan(ctx, vol); err != nil {
			t.Fatalf("clear staging: %v", err)
		}
		return id
	}
	err = s.CommitSnapshot(ctx, SnapshotCommit{
		SnapshotID: id, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{Files: n, Dirs: int64(len(agg))},
		Dirs:  agg,
	})
	if err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	return id
}

func browseFixture(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s, _ := testStore(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 31, "NTFS", "browse")
	if err != nil {
		t.Fatal(err)
	}
	recs := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "subA", IsDir: true},
		{FRN: 3, ParentFRN: 1, Name: "subB", IsDir: true},
		{FRN: 4, ParentFRN: 2, Name: "deep", IsDir: true},
		{FRN: 10, ParentFRN: 2, Name: "a1.bin", Size: 1000, AllocSize: 4096},
		{FRN: 11, ParentFRN: 2, Name: "a2.bin", Size: 2000, AllocSize: 8192},
		{FRN: 12, ParentFRN: 3, Name: "b1.bin", Size: 500, AllocSize: 4096},
		{FRN: 13, ParentFRN: 1, Name: "root.bin", Size: 7000, AllocSize: 7000},
		{FRN: 14, ParentFRN: 4, Name: "d1.bin", Size: 10, AllocSize: 1024},
	}
	snap := seedBrowseSnapshot(t, s, vol, `D:\`, "complete", map[uint64]bool{4: true}, recs)
	return s, vol, snap
}

func TestBrowseDirRootAndChildren(t *testing.T) {
	s, _, snap := browseFixture(t)
	ctx := context.Background()

	res, err := s.BrowseDir(ctx, snap, 0, 0, 0)
	if err != nil {
		t.Fatalf("browse root: %v", err)
	}
	if res.Dir.FRN != 1 || res.Dir.Name != `D:\` || res.Path != `D:\` {
		t.Fatalf("root = %+v path %q", res.Dir, res.Path)
	}
	if len(res.Breadcrumb) != 1 || res.Breadcrumb[0].FRN != 1 {
		t.Fatalf("breadcrumb = %+v", res.Breadcrumb)
	}
	if res.DirsTotal != 2 || res.FilesTotal != 1 || res.DirsTruncated || res.FilesTruncated {
		t.Fatalf("counts = dirs %d files %d trunc %v/%v", res.DirsTotal, res.FilesTotal,
			res.DirsTruncated, res.FilesTruncated)
	}
	if len(res.Dirs) != 2 || res.Dirs[0].FRN != 2 || res.Dirs[1].FRN != 3 {
		t.Fatalf("dirs = %+v", res.Dirs)
	}
	if res.Dirs[0].Alloc != 4096+8192+1024 || res.Dirs[0].Files != 3 {
		t.Fatalf("subA totals = %+v", res.Dirs[0])
	}
	if len(res.Files) != 1 || res.Files[0].FRN != 13 || res.Files[0].Alloc != 7000 {
		t.Fatalf("files = %+v", res.Files)
	}
	if !res.Dirs[0].Unknown {
		t.Fatal("subA holds an unreadable descendant and must be flagged unknown")
	}
	if res.DirsTotal > int64(len(res.Dirs)) {
		t.Fatal("truncation flag mismatch")
	}

	deep, err := s.BrowseDir(ctx, snap, 4, 0, 0)
	if err != nil {
		t.Fatalf("browse deep: %v", err)
	}
	if deep.Path != `D:\subA\deep` {
		t.Fatalf("deep path = %q", deep.Path)
	}
	if len(deep.Breadcrumb) != 3 || deep.Breadcrumb[0].FRN != 1 ||
		deep.Breadcrumb[1].FRN != 2 || deep.Breadcrumb[2].FRN != 4 {
		t.Fatalf("deep breadcrumb = %+v", deep.Breadcrumb)
	}
	if len(deep.Dirs) != 0 || len(deep.Files) != 1 || deep.Files[0].FRN != 14 {
		t.Fatalf("deep listing = %+v", deep)
	}
	if !deep.Dir.Unknown {
		t.Fatal("deep itself was unreadable and must be flagged unknown")
	}
}

func TestBrowseDirLimitsAndErrors(t *testing.T) {
	s, _, snap := browseFixture(t)
	ctx := context.Background()

	res, err := s.BrowseDir(ctx, snap, 1, 1, 0)
	if err != nil {
		t.Fatalf("browse with limits: %v", err)
	}
	if len(res.Dirs) != 1 || !res.DirsTruncated || res.DirsTotal != 2 {
		t.Fatalf("dir truncation = %+v", res)
	}
	if len(res.Files) != 1 || res.FilesTruncated {
		t.Fatalf("file truncation = %+v", res)
	}

	if _, err := s.BrowseDir(ctx, snap, 999, 0, 0); !errors.Is(err, ErrDirNotFound) {
		t.Fatalf("missing frn error = %v", err)
	}
	if _, err := s.BrowseDir(ctx, 9999, 0, 0, 0); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("missing snapshot error = %v", err)
	}
}

func TestVolumeRootSnapshotsAndList(t *testing.T) {
	s, _, _ := browseFixture(t)
	ctx := context.Background()
	vol, err := s.EnsureVolume(ctx, 32, "NTFS", "trend")
	if err != nil {
		t.Fatal(err)
	}
	rootRecs := []model.FileRecord{{FRN: 100, ParentFRN: 0, Name: `E:\`, IsDir: true}}
	subRecs := []model.FileRecord{{FRN: 200, ParentFRN: 0, Name: `E:\data`, IsDir: true}}
	first := seedBrowseSnapshot(t, s, vol, `E:\`, "complete", nil, rootRecs)
	seedBrowseSnapshot(t, s, vol, `E:\data`, "complete", nil, subRecs)
	partial := seedBrowseSnapshot(t, s, vol, `E:\`, "partial", nil, rootRecs)
	second := seedBrowseSnapshot(t, s, vol, `E:\`, "complete", nil, rootRecs)
	seedBrowseSnapshot(t, s, vol, `E:\`, "failed", nil, rootRecs)

	points, err := s.VolumeRootSnapshots(ctx, vol, 8)
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(points) != 2 || points[0].ID != first || points[1].ID != second {
		t.Fatalf("trend = %+v, want #%d,#%d (partial #%d excluded)", points, first, second, partial)
	}

	all, err := s.ListSnapshots(ctx, 3)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("list len = %d, want 3", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].StartedAt > all[i-1].StartedAt {
			t.Fatalf("list not newest-first: %+v", all)
		}
	}
}
