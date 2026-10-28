package diff

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"fattrace/internal/model"
)

func file(frn, parent uint64, name string, size, alloc int64) model.FileRecord {
	return model.FileRecord{FRN: frn, ParentFRN: parent, Name: name, Size: size, AllocSize: alloc}
}

func dir(frn, parent uint64, name string, alloc, bytes, files int64) model.SnapshotDir {
	return model.SnapshotDir{
		FRN: frn, ParentFRN: parent, Name: name,
		TotalAllocBytes: alloc, TotalBytes: bytes, TotalFiles: files,
	}
}

func findFile(t *testing.T, list []model.FileChange, frn uint64) model.FileChange {
	t.Helper()
	for _, c := range list {
		if c.FRN == frn {
			return c
		}
	}
	t.Fatalf("file FRN %d not found in list of %d", frn, len(list))
	return model.FileChange{}
}

func findDir(t *testing.T, list []model.DirChange, frn uint64) model.DirChange {
	t.Helper()
	for _, d := range list {
		if d.FRN == frn {
			return d
		}
	}
	t.Fatalf("dir FRN %d not found in list of %d", frn, len(list))
	return model.DirChange{}
}

func hasWarning(res model.DiffResult, sub string) bool {
	for _, w := range res.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestFileClassification(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 24576, 1500, 5),
			dir(2, 1, "a", 24576, 1500, 5),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 28672, 2250, 5),
			dir(2, 1, "a", 24576, 1750, 4),
		},
		FromFiles: []model.FileRecord{
			file(10, 2, "grow.bin", 100, 4096),
			file(11, 2, "shrink.bin", 200, 8192),
			file(12, 2, "del.bin", 300, 4096),
			file(13, 2, "keep.bin", 400, 4096),
			file(14, 2, "move.bin", 500, 4096),
		},
		ToFiles: []model.FileRecord{
			file(10, 2, "grow.bin", 600, 8192),
			file(11, 2, "shrink.bin", 50, 4096),
			file(13, 2, "keep.bin", 400, 4096),
			file(14, 1, "move.bin", 500, 4096),
			file(15, 2, "new.bin", 700, 8192),
		},
	})

	tot := res.Totals
	if tot.FilesAdded != 1 || tot.FilesDeleted != 1 || tot.FilesGrown != 1 ||
		tot.FilesShrunk != 1 || tot.FilesMoved != 1 || tot.FilesRenamed != 0 {
		t.Fatalf("file totals = %+v", tot)
	}
	if tot.AllocAdded != 8192 || tot.AllocDeleted != -4096 ||
		tot.AllocGrown != 4096 || tot.AllocShrunk != -4096 || tot.DeltaAlloc != 4096 {
		t.Fatalf("alloc totals = %+v", tot)
	}
	if tot.BytesAdded != 700 || tot.BytesDeleted != -300 ||
		tot.BytesGrown != 500 || tot.BytesShrunk != -150 || tot.DeltaBytes != 750 {
		t.Fatalf("byte totals = %+v", tot)
	}
	if res.Check.RootDeltaAlloc != 4096 {
		t.Fatalf("root delta = %d, want 4096", res.Check.RootDeltaAlloc)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	if len(res.TopGrowth) != 2 {
		t.Fatalf("top growth = %d rows, want 2", len(res.TopGrowth))
	}
	if g := res.TopGrowth[0]; g.FRN != 15 || g.Path != `D:\a\new.bin` || g.DeltaAlloc != 8192 {
		t.Fatalf("top growth[0] = %+v", g)
	}
	if d := res.TopDeleted[0]; d.FRN != 12 || d.Path != `D:\a\del.bin` || d.DeltaAlloc != -4096 {
		t.Fatalf("top deleted[0] = %+v", d)
	}
	if m := res.Moves[0]; m.FRN != 14 || m.Path != `D:\move.bin` || m.OldPath != `D:\a\move.bin` {
		t.Fatalf("move[0] = %+v", m)
	}

	if len(res.TopDirs) != 1 {
		t.Fatalf("top dirs = %+v, want only the directory that owns the growth", res.TopDirs)
	}
	if a := res.TopDirs[0]; a.FRN != 2 || a.Path != `D:\a` || a.DeltaAlloc != 4096 {
		t.Fatalf("dir growth[0] = %+v", a)
	}
	if len(res.ShrunkDirs) != 0 {
		t.Fatalf("unexpected shrunk dirs: %+v", res.ShrunkDirs)
	}
}

func TestFileRenameAndGrownRename(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{dir(1, 0, `D:\`, 8192, 200, 2)},
		ToDirs:   []model.SnapshotDir{dir(1, 0, `D:\`, 12288, 300, 2)},
		FromFiles: []model.FileRecord{
			file(10, 1, "old.txt", 100, 4096),
			file(11, 1, "a.log", 100, 4096),
		},
		ToFiles: []model.FileRecord{
			file(10, 1, "new.txt", 100, 4096),
			file(11, 1, "b.log", 200, 8192),
		},
	})

	if res.Totals.FilesRenamed != 1 || res.Totals.FilesGrown != 1 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	if len(res.Moves) != 1 {
		t.Fatalf("moves = %+v", res.Moves)
	}
	r := findFile(t, res.Moves, 10)
	if r.Kind != model.ChangeRenamed || r.Path != `D:\new.txt` || r.OldPath != `D:\old.txt` {
		t.Fatalf("rename = %+v", r)
	}
	g := findFile(t, res.TopGrowth, 11)
	if g.Kind != model.ChangeGrown || g.Path != `D:\b.log` || g.OldPath != `D:\a.log` {
		t.Fatalf("grown rename = %+v", g)
	}
}

func TestDirectoryRename(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 4096, 100, 1),
			dir(2, 1, "old", 4096, 100, 1),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 4096, 100, 1),
			dir(2, 1, "new", 4096, 100, 1),
		},
		FromFiles: []model.FileRecord{file(10, 2, "keep.bin", 100, 4096)},
		ToFiles:   []model.FileRecord{file(10, 2, "keep.bin", 100, 4096)},
	})

	if res.Totals.DirsRenamed != 1 || res.Totals.DirsMoved != 0 {
		t.Fatalf("dir totals = %+v", res.Totals)
	}
	if len(res.DirMoves) != 1 {
		t.Fatalf("dir moves = %+v", res.DirMoves)
	}
	d := res.DirMoves[0]
	if d.Kind != model.ChangeRenamed || d.Path != `D:\new` || d.OldPath != `D:\old` {
		t.Fatalf("dir rename = %+v", d)
	}
	if len(res.TopDirs) != 0 {
		t.Fatalf("rename produced dir growth: %+v", res.TopDirs)
	}
}

func TestDirectoryMoveAdjustsOwnership(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 10000, 100, 1),
			dir(2, 1, "A", 10000, 100, 1),
			dir(3, 1, "B", 0, 0, 0),
			dir(4, 2, "sub", 10000, 100, 1),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 10000, 100, 1),
			dir(2, 1, "A", 0, 0, 0),
			dir(3, 1, "B", 10000, 100, 1),
			dir(4, 3, "sub", 10000, 100, 1),
		},
		FromFiles: []model.FileRecord{file(10, 4, "x.bin", 100, 10000)},
		ToFiles:   []model.FileRecord{file(10, 4, "x.bin", 100, 10000)},
	})

	if res.Totals.DirsMoved != 1 {
		t.Fatalf("dirs moved = %d, want 1", res.Totals.DirsMoved)
	}
	if len(res.TopDirs) != 0 || len(res.ShrunkDirs) != 0 {
		t.Fatalf("pure move leaked into growth: growth=%+v shrunk=%+v", res.TopDirs, res.ShrunkDirs)
	}
	if len(res.DirMoves) != 1 {
		t.Fatalf("dir moves = %+v", res.DirMoves)
	}
	m := res.DirMoves[0]
	if m.FRN != 4 || m.Path != `D:\B\sub` || m.OldPath != `D:\A\sub` {
		t.Fatalf("dir move = %+v", m)
	}
	if res.Check.RootDeltaAlloc != 0 || len(res.Warnings) != 0 {
		t.Fatalf("check = %+v warnings = %v", res.Check, res.Warnings)
	}
}

func TestDirectoryMoveWithIntrinsicGrowth(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 1000, 100, 1),
			dir(2, 1, "A", 1000, 100, 1),
			dir(3, 1, "B", 0, 0, 0),
			dir(4, 2, "sub", 1000, 100, 1),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 1500, 150, 1),
			dir(2, 1, "A", 0, 0, 0),
			dir(3, 1, "B", 1500, 150, 1),
			dir(4, 3, "sub", 1500, 150, 1),
		},
		FromFiles: []model.FileRecord{file(10, 4, "x.bin", 100, 1000)},
		ToFiles:   []model.FileRecord{file(10, 4, "x.bin", 150, 1500)},
	})

	if res.Totals.FilesGrown != 1 || res.Totals.DeltaAlloc != 500 || res.Totals.DeltaBytes != 50 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	moved := findDir(t, res.TopDirs, 4)
	if moved.Kind != model.ChangeMoved || moved.DeltaAlloc != 500 || moved.Path != `D:\B\sub` {
		t.Fatalf("moved dir growth = %+v", moved)
	}
	for _, d := range res.TopDirs {
		if d.FRN == 2 || d.FRN == 3 {
			t.Fatalf("old/new parent leaked into growth: %+v", d)
		}
	}
	if len(res.DirMoves) != 1 || res.DirMoves[0].FRN != 4 {
		t.Fatalf("dir moves = %+v", res.DirMoves)
	}
	if hasWarning(res, "conservation") {
		t.Fatalf("unexpected conservation warning: %v", res.Warnings)
	}
}

func TestCrossVolumeCandidateAnnotation(t *testing.T) {
	const serial uint32 = 0xCAFE
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{dir(1, 0, `D:\`, 0, 0, 0)},
		ToDirs:   []model.SnapshotDir{dir(1, 0, `D:\`, 12288, 8010, 2)},
		ToFiles: []model.FileRecord{
			{FRN: 20, ParentFRN: 1, Name: "huge.iso", Size: 8000, AllocSize: 8192,
				MTime: time.UnixMilli(1_700_000_000_000)},
			{FRN: 21, ParentFRN: 1, Name: "plain.bin", Size: 10, AllocSize: 4096},
		},
		Candidates: []model.FileCandidate{
			{Name: "huge.iso", Size: 8000, MTimeMS: 1_700_000_000_000, VolumeSerial: serial},
			{Name: "plain.bin", Size: 10, MTimeMS: 1_700_000_000_000, VolumeSerial: serial},
		},
	})

	huge := findFile(t, res.TopGrowth, 20)
	if huge.SuspectedFromVolume != serial {
		t.Fatalf("suspected volume = %#x, want %#x", huge.SuspectedFromVolume, serial)
	}
	plain := findFile(t, res.TopGrowth, 21)
	if plain.SuspectedFromVolume != 0 {
		t.Fatalf("file without mtime was annotated: %+v", plain)
	}
}

func TestNewlyUnknownLeafWarning(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 1000, 100, 1),
			dir(2, 1, "a", 1000, 100, 1),
		},
		ToDirs: []model.SnapshotDir{
			{FRN: 1, ParentFRN: 0, Name: `D:\`, TotalAllocBytes: 1000, TotalBytes: 100, TotalFiles: 1, Unknown: true},
			{FRN: 2, ParentFRN: 1, Name: "a", TotalAllocBytes: 1000, TotalBytes: 100, TotalFiles: 1, Unknown: true},
		},
		FromFiles: []model.FileRecord{file(10, 2, "x.bin", 100, 1000)},
		ToFiles:   []model.FileRecord{file(10, 2, "x.bin", 100, 1000)},
	})

	if len(res.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly the unknown warning", res.Warnings)
	}
	if !strings.Contains(res.Warnings[0], "1 directory") {
		t.Fatalf("unknown warning should count the leaf only: %q", res.Warnings[0])
	}
}

func TestLongUnicodePaths(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 0, 0, 0),
			dir(2, 1, "资料", 0, 0, 0),
			dir(3, 2, "2026年", 0, 0, 0),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 4096, 1024, 1),
			dir(2, 1, "资料", 4096, 1024, 1),
			dir(3, 2, "2026年", 4096, 1024, 1),
		},
		ToFiles: []model.FileRecord{file(10, 3, "报告-最终版.docx", 1024, 4096)},
	})

	if len(res.TopGrowth) != 1 {
		t.Fatalf("top growth = %+v", res.TopGrowth)
	}
	want := `D:\示例\2026年\报告-最终版.docx`
	if got := res.TopGrowth[0].Path; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestTopNLimit(t *testing.T) {
	var files []model.FileRecord
	for i := 0; i < 30; i++ {
		files = append(files, file(uint64(10+i), 1, fmt.Sprintf("f%02d.bin", i), 1000, 1000))
	}
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{dir(1, 0, `D:\`, 0, 0, 0)},
		ToDirs:   []model.SnapshotDir{dir(1, 0, `D:\`, 30000, 30000, 30)},
		ToFiles:  files,
		TopN:     5,
	})

	if len(res.TopGrowth) != 5 {
		t.Fatalf("top growth rows = %d, want 5", len(res.TopGrowth))
	}
	for i, c := range res.TopGrowth {
		if want := uint64(10 + i); c.FRN != want {
			t.Fatalf("row %d FRN = %d, want %d", i, c.FRN, want)
		}
	}
	if res.Totals.FilesAdded != 30 {
		t.Fatalf("totals still count everything: %+v", res.Totals)
	}
}

func TestConservationWarning(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{dir(1, 0, `D:\`, 0, 0, 0)},
		ToDirs:   []model.SnapshotDir{dir(1, 0, `D:\`, 5000, 4096, 1)},
		ToFiles:  []model.FileRecord{file(10, 1, "x.bin", 4096, 4096)},
	})

	if res.Check.RootDeltaAlloc != 5000 {
		t.Fatalf("root delta = %d", res.Check.RootDeltaAlloc)
	}
	if !hasWarning(res, "conservation") {
		t.Fatalf("warnings = %v, want a conservation warning", res.Warnings)
	}
}

func TestSparseAllocationGrowth(t *testing.T) {
	res := Compute(Input{
		FromDirs:  []model.SnapshotDir{dir(1, 0, `D:\`, 4096, 100, 1)},
		ToDirs:    []model.SnapshotDir{dir(1, 0, `D:\`, 4096, 200, 1)},
		FromFiles: []model.FileRecord{file(10, 1, "sparse.bin", 100, 4096)},
		ToFiles:   []model.FileRecord{file(10, 1, "sparse.bin", 200, 4096)},
	})

	if res.Totals.FilesGrown != 1 || res.Totals.DeltaAlloc != 0 || res.Totals.DeltaBytes != 100 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	if len(res.TopGrowth) != 1 || res.TopGrowth[0].DeltaAlloc != 0 || res.TopGrowth[0].DeltaBytes != 100 {
		t.Fatalf("top growth = %+v", res.TopGrowth)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("alloc-neutral growth should conserve: %v", res.Warnings)
	}
}

func TestDedupedHardlinkCountsOnce(t *testing.T) {
	res := Compute(Input{
		FromDirs:  []model.SnapshotDir{dir(1, 0, `D:\`, 4096, 100, 1)},
		ToDirs:    []model.SnapshotDir{dir(1, 0, `D:\`, 8192, 200, 2)},
		FromFiles: []model.FileRecord{file(10, 1, "keep.bin", 100, 4096)},
		ToFiles: []model.FileRecord{
			file(10, 1, "keep.bin", 100, 4096),
			file(11, 1, "link.bin", 100, 4096),
		},
	})

	if res.Totals.FilesAdded != 1 || res.Totals.DeltaAlloc != 4096 {
		t.Fatalf("deduped rows counted wrong: %+v", res.Totals)
	}
}

func TestDeletedDirectoryPaths(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 4096, 100, 1),
			dir(2, 1, "A", 4096, 100, 1),
		},
		ToDirs:    []model.SnapshotDir{dir(1, 0, `D:\`, 0, 0, 0)},
		FromFiles: []model.FileRecord{file(10, 2, "gone.bin", 100, 4096)},
	})

	if res.Totals.DirsDeleted != 1 || res.Totals.FilesDeleted != 1 {
		t.Fatalf("totals = %+v", res.Totals)
	}
	d := findDir(t, res.ShrunkDirs, 2)
	if d.Kind != model.ChangeDeleted || d.Path != `D:\A` || d.DeltaAlloc != -4096 {
		t.Fatalf("deleted dir = %+v", d)
	}
	f := res.TopDeleted[0]
	if f.Path != `D:\A\gone.bin` {
		t.Fatalf("deleted file path = %q", f.Path)
	}
	for _, d := range res.ShrunkDirs {
		if d.FRN == 1 {
			t.Fatalf("ancestor should be hidden: %+v", d)
		}
	}
}

func TestAncestorInheritedGrowthHidden(t *testing.T) {
	res := Compute(Input{
		FromDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 0, 0, 0),
			dir(2, 1, "a", 0, 0, 0),
			dir(3, 2, "b", 0, 0, 0),
		},
		ToDirs: []model.SnapshotDir{
			dir(1, 0, `D:\`, 4096, 2000, 1),
			dir(2, 1, "a", 4096, 2000, 1),
			dir(3, 2, "b", 4096, 2000, 1),
		},
		ToFiles: []model.FileRecord{file(10, 3, "payload.bin", 2000, 4096)},
	})

	if len(res.TopDirs) != 1 {
		t.Fatalf("top dirs = %+v, want only the deepest owner", res.TopDirs)
	}
	if d := res.TopDirs[0]; d.FRN != 3 || d.Path != `D:\a\b` {
		t.Fatalf("top dir = %+v", d)
	}
}
