package diff

import (
	"testing"

	"fattrace/internal/model"
)

func TestOnFileChangeSeesEveryPath(t *testing.T) {
	fromDirs := []model.SnapshotDir{
		{FRN: 1, ParentFRN: 0, Name: `D:\root`, Depth: 0},
	}
	toDirs := []model.SnapshotDir{
		{FRN: 1, ParentFRN: 0, Name: `D:\root`, Depth: 0},
		{FRN: 2, ParentFRN: 1, Name: "sub", Depth: 1},
	}
	fromFiles := []model.FileRecord{
		{FRN: 10, ParentFRN: 1, Name: "grown.bin", Size: 10, AllocSize: 10},
		{FRN: 11, ParentFRN: 1, Name: "old.bin", Size: 5, AllocSize: 5},
	}
	toFiles := []model.FileRecord{
		{FRN: 10, ParentFRN: 1, Name: "grown.bin", Size: 15, AllocSize: 15},
		{FRN: 12, ParentFRN: 2, Name: "new.bin", Size: 7, AllocSize: 7},
	}

	seen := map[string]model.ChangeKind{}
	res := Compute(Input{
		FromFiles: fromFiles,
		ToFiles:   toFiles,
		FromDirs:  fromDirs,
		ToDirs:    toDirs,
		TopN:      1,
		OnFileChange: func(c model.FileChange) {
			seen[c.Path] = c.Kind
		},
	})

	if got := seen[`D:\root\grown.bin`]; got != model.ChangeGrown {
		t.Fatalf("grown path kind = %q, seen=%v", got, seen)
	}
	if got := seen[`D:\root\sub\new.bin`]; got != model.ChangeAdded {
		t.Fatalf("added path kind = %q, seen=%v", got, seen)
	}
	if got := seen[`D:\root\old.bin`]; got != model.ChangeDeleted {
		t.Fatalf("deleted path kind = %q, seen=%v", got, seen)
	}
	if len(res.TopGrowth) != 1 {
		t.Fatalf("top growth truncated to %d, want 1", len(res.TopGrowth))
	}
}
