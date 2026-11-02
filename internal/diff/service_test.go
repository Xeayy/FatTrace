package diff

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fattrace/internal/model"
	"fattrace/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "diff.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func rootDir(frn uint64, name string) model.FileRecord {
	return model.FileRecord{FRN: frn, ParentFRN: 0, Name: name, IsDir: true, Attrs: 0x10}
}

func seedSnapshot(t *testing.T, st *store.Store, vol int64, root, status string, used int64, recs []model.FileRecord) int64 {
	t.Helper()
	ctx := context.Background()
	w, err := st.BeginScan(ctx, vol)
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
	agg, err := st.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	id, err := st.BeginSnapshot(ctx, vol, "weekly", root)
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	if status != "complete" {
		if err := st.FinishSnapshot(ctx, id, status, model.ScanStats{}); err != nil {
			t.Fatalf("finish snapshot: %v", err)
		}
		if err := st.ClearScan(ctx, vol); err != nil {
			t.Fatalf("clear staging: %v", err)
		}
		return id
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
	err = st.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID: id, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{
			Files: files, Dirs: dirs, Bytes: alloc, AllocBytes: alloc,
			UniqueBytes: alloc, UniqueAllocBytes: alloc,
			VolumeUsed: used, CoveragePct: 99.9,
		},
		Dirs: agg,
	})
	if err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	return id
}

func TestServiceResolveLatestVolumeRootPair(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0x1111, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}

	base := []model.FileRecord{
		rootDir(1, `D:\`),
		{FRN: 2, ParentFRN: 1, Name: "sub", IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 1000},
	}
	s1 := seedSnapshot(t, st, vol, `D:\`, "complete", 10_000, base)
	subRecs := []model.FileRecord{
		{FRN: 3, ParentFRN: 0, Name: `D:\sub`, IsDir: true},
		{FRN: 11, ParentFRN: 3, Name: "x.bin", Size: 10, AllocSize: 4096},
	}
	s2 := seedSnapshot(t, st, vol, `D:\sub`, "complete", 20_000, subRecs)
	withNew := append(append([]model.FileRecord{}, base...),
		model.FileRecord{FRN: 12, ParentFRN: 1, Name: "new.bin", Size: 100, AllocSize: 1000})
	s3 := seedSnapshot(t, st, vol, `D:\`, "complete", 11_000, withNew)
	s4 := seedSnapshot(t, st, vol, `D:\`, "partial", 0, base)

	res, err := Run(ctx, st, Options{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.FromSnapshot.ID != s1 || res.ToSnapshot.ID != s3 {
		t.Fatalf("pair = #%d → #%d, want #%d → #%d (subdir #%d, partial #%d ignored)",
			res.FromSnapshot.ID, res.ToSnapshot.ID, s1, s3, s2, s4)
	}
	if res.Totals.FilesAdded != 1 || res.Totals.FilesDeleted != 0 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	if !res.Check.OK || res.Check.Skipped {
		t.Fatalf("volume check = %+v", res.Check)
	}
	if res.Check.RootDeltaAlloc != 1000 || res.Check.DiskDeltaAlloc != 1000 {
		t.Fatalf("check deltas = %+v", res.Check)
	}
	if _, err := Run(ctx, st, Options{FromID: s2, ToID: s3}); err == nil ||
		!strings.Contains(err.Error(), "different roots") {
		t.Fatalf("subdir pair error = %v, want different roots", err)
	}
	if _, err := Run(ctx, st, Options{ToID: s4}); err == nil ||
		!strings.Contains(err.Error(), "must be complete") {
		t.Fatalf("partial pair error = %v, want status rejection", err)
	}
}

func TestServiceRejectsCrossVolumePair(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	vol1, err := st.EnsureVolume(ctx, 0x1111, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	vol2, err := st.EnsureVolume(ctx, 0x2222, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	a := seedSnapshot(t, st, vol1, `D:\`, "complete", 0,
		[]model.FileRecord{rootDir(1, `D:\`)})
	b := seedSnapshot(t, st, vol2, `E:\`, "complete", 0,
		[]model.FileRecord{rootDir(50, `E:\`)})

	_, err = Run(ctx, st, Options{FromID: a, ToID: b})
	if err == nil || !strings.Contains(err.Error(), "different volumes") {
		t.Fatalf("cross-volume error = %v", err)
	}
}

func TestServiceCrossVolumeAnnotation(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	vol1, err := st.EnsureVolume(ctx, 0xAAAA, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	vol2, err := st.EnsureVolume(ctx, 0xBBBB, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.UnixMilli(1_700_000_123_000)
	empty := []model.FileRecord{rootDir(1, `D:\`)}
	from := seedSnapshot(t, st, vol1, `D:\`, "complete", 0, empty)
	withFile := []model.FileRecord{
		rootDir(1, `D:\`),
		{FRN: 10, ParentFRN: 1, Name: "move.bin", Size: 5000, AllocSize: 8192, MTime: mtime},
	}
	to := seedSnapshot(t, st, vol1, `D:\`, "complete", 8192, withFile)
	seedSnapshot(t, st, vol2, `E:\`, "complete", 8192, []model.FileRecord{
		rootDir(50, `E:\`),
		{FRN: 60, ParentFRN: 50, Name: "move.bin", Size: 5000, AllocSize: 8192, MTime: mtime},
	})

	res, err := Run(ctx, st, Options{FromID: from, ToID: to, CrossVolume: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := findFile(t, res.TopGrowth, 10)
	if got.SuspectedFromVolume != 0xBBBB {
		t.Fatalf("suspected volume = %#x, want 0xBBBB", got.SuspectedFromVolume)
	}

	res, err = Run(ctx, st, Options{FromID: from, ToID: to})
	if err != nil {
		t.Fatalf("run without candidates: %v", err)
	}
	if got := findFile(t, res.TopGrowth, 10); got.SuspectedFromVolume != 0 {
		t.Fatalf("annotation leaked without the flag: %+v", got)
	}
}

func TestServiceCrossVolumeCandidatesUseVolumeRootSnapshot(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	vol1, err := st.EnsureVolume(ctx, 0xAAAA, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	vol2, err := st.EnsureVolume(ctx, 0xBBBB, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.UnixMilli(1_700_000_123_000)
	from := seedSnapshot(t, st, vol1, `D:\`, "complete", 0,
		[]model.FileRecord{rootDir(1, `D:\`)})
	to := seedSnapshot(t, st, vol1, `D:\`, "complete", 8192, []model.FileRecord{
		rootDir(1, `D:\`),
		{FRN: 10, ParentFRN: 1, Name: "move.bin", Size: 5000, AllocSize: 8192, MTime: mtime},
	})
	seedSnapshot(t, st, vol2, `E:\`, "complete", 8192, []model.FileRecord{
		rootDir(50, `E:\`),
		{FRN: 60, ParentFRN: 50, Name: "move.bin", Size: 5000, AllocSize: 8192, MTime: mtime},
	})
	// A newer subdirectory snapshot must not hide the volume-root candidates.
	seedSnapshot(t, st, vol2, `E:\other`, "complete", 0, []model.FileRecord{
		{FRN: 51, ParentFRN: 0, Name: `E:\other`, IsDir: true},
	})

	res, err := Run(ctx, st, Options{FromID: from, ToID: to, CrossVolume: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := findFile(t, res.TopGrowth, 10)
	if got.SuspectedFromVolume != 0xBBBB {
		t.Fatalf("suspected volume = %#x, want 0xBBBB", got.SuspectedFromVolume)
	}
}

func TestServiceRejectsExpiredFileRows(t *testing.T) {
	base := []model.FileRecord{
		rootDir(1, `D:\`),
		{FRN: 10, ParentFRN: 1, Name: "a.bin", Size: 100, AllocSize: 100},
	}
	with := func(extra model.FileRecord) []model.FileRecord {
		out := append([]model.FileRecord{}, base...)
		return append(out, extra)
	}

	cases := []struct {
		name    string
		expired string
		delete  int
	}{
		{"from expired", "from", 1},
		{"to expired", "to", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			ctx := context.Background()
			vol, err := st.EnsureVolume(ctx, 0x2222, "NTFS", "")
			if err != nil {
				t.Fatal(err)
			}
			s1 := seedSnapshot(t, st, vol, `D:\`, "complete", 1000, with(
				model.FileRecord{FRN: 11, ParentFRN: 1, Name: "b.bin", Size: 100, AllocSize: 100}))
			s2 := seedSnapshot(t, st, vol, `D:\`, "complete", 1200, with(
				model.FileRecord{FRN: 12, ParentFRN: 1, Name: "c.bin", Size: 200, AllocSize: 200}))
			from, to := s1, s2
			expired := s1
			if tc.delete == 2 {
				expired = s2
			}
			if _, err := st.DeleteSnapshotFiles(ctx, []int64{expired}); err != nil {
				t.Fatal(err)
			}
			_, err = Run(ctx, st, Options{FromID: from, ToID: to, TopN: 5})
			if err == nil || !strings.Contains(err.Error(), "retention") {
				t.Fatalf("diff error = %v, want retention error", err)
			}
			if !strings.Contains(err.Error(), "#"+itoa64(expired)) {
				t.Fatalf("diff error does not name snapshot #%d: %v", expired, err)
			}
		})
	}
}

func itoa64(v int64) string {
	return fmt.Sprintf("%d", v)
}

func TestVolumeCheckTolerance(t *testing.T) {
	from := store.SnapshotInfo{ID: 1, Status: "complete", Root: `D:\`, VolumeUsed: 100_000}
	to := store.SnapshotInfo{ID: 2, Status: "complete", Root: `D:\`, VolumeUsed: 110_000}

	mismatch := model.DiffResult{Check: model.VolumeCheck{RootDeltaAlloc: 1_000}}
	applyVolumeCheck(&mismatch, from, to, nil)
	if mismatch.Check.OK || mismatch.Check.DiskDeltaAlloc != 10_000 || mismatch.Check.Skipped {
		t.Fatalf("mismatch check = %+v", mismatch.Check)
	}

	agree := model.DiffResult{Check: model.VolumeCheck{RootDeltaAlloc: 10_000}}
	applyVolumeCheck(&agree, from, to, nil)
	if !agree.Check.OK || agree.Check.DiffPct != 0 {
		t.Fatalf("agree check = %+v", agree.Check)
	}

	subdir := model.DiffResult{}
	applyVolumeCheck(&subdir, from, store.SnapshotInfo{
		ID: 2, Status: "complete", Root: `D:\sub`, VolumeUsed: 110_000,
	}, nil)
	if !subdir.Check.Skipped || !subdir.Check.OK {
		t.Fatalf("subdir check = %+v", subdir.Check)
	}

	noUsage := model.DiffResult{}
	applyVolumeCheck(&noUsage, from, store.SnapshotInfo{
		ID: 3, Status: "complete", Root: `D:\`,
	}, nil)
	if !noUsage.Check.Skipped {
		t.Fatalf("missing usage check = %+v", noUsage.Check)
	}
}
