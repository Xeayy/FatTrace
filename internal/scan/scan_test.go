//go:build windows

package scan

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"fattrace/internal/model"
)

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFileIDBothDirInfoLayout(t *testing.T) {
	var info fileIDBothDirInfo
	if got := unsafe.Sizeof(info); got != 104 {
		t.Fatalf("struct size = %d, want 104", got)
	}
	if off := unsafe.Offsetof(info.FileID); off != 96 {
		t.Fatalf("FileID offset = %d, want 96", off)
	}
	if off := unsafe.Offsetof(info.FileNameLength); off != 60 {
		t.Fatalf("FileNameLength offset = %d, want 60", off)
	}
}

func TestWalkBasic(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, filepath.Join(root, "a.txt"), 5)
	writeSizedFile(t, filepath.Join(root, "sub", "b.bin"), 1000)
	writeSizedFile(t, filepath.Join(root, "sub", "deep", "c"), 1)

	var emitted int
	stats, err := Walk(context.Background(), root, Options{DedupeHardlinks: true}, func(r model.FileRecord) error {
		emitted++
		if r.FRN == 0 {
			t.Errorf("record %q has zero FRN", r.Name)
		}
		if r.ParentFRN == 0 {
			t.Errorf("record %q has zero parent FRN", r.Name)
		}
		if r.IsDir {
			t.Errorf("unexpected directory record %q", r.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if stats.Files != 3 {
		t.Fatalf("files = %d, want 3", stats.Files)
	}
	if stats.Dirs != 3 {
		t.Fatalf("dirs = %d, want 3", stats.Dirs)
	}
	if stats.Bytes != 1006 {
		t.Fatalf("bytes = %d, want 1006", stats.Bytes)
	}
	if emitted != 3 {
		t.Fatalf("emitted = %d, want 3", emitted)
	}
	if stats.Errors != 0 {
		t.Fatalf("errors = %d, want 0: %+v", stats.Errors, stats.ErrorSamples)
	}
}

func TestFileIDStableAcrossRenameAndMove(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	writeSizedFile(t, file, 10)

	id1, err := FileID(file)
	if err != nil {
		t.Fatalf("id before rename: %v", err)
	}
	renamed := filepath.Join(root, "b.txt")
	if err := os.Rename(file, renamed); err != nil {
		t.Fatalf("rename: %v", err)
	}
	id2, err := FileID(renamed)
	if err != nil {
		t.Fatalf("id after rename: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("file ID changed on rename: %d -> %d", id1, id2)
	}

	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dirID1, err := FileID(dir)
	if err != nil {
		t.Fatalf("dir id: %v", err)
	}
	moved := filepath.Join(dir, "b.txt")
	if err := os.Rename(renamed, moved); err != nil {
		t.Fatalf("move: %v", err)
	}
	id3, err := FileID(moved)
	if err != nil {
		t.Fatalf("id after move: %v", err)
	}
	if id1 != id3 {
		t.Fatalf("file ID changed on move: %d -> %d", id1, id3)
	}

	dir2 := filepath.Join(root, "dir2")
	if err := os.Rename(dir, dir2); err != nil {
		t.Fatalf("dir rename: %v", err)
	}
	dirID2, err := FileID(dir2)
	if err != nil {
		t.Fatalf("dir id after rename: %v", err)
	}
	if dirID1 != dirID2 {
		t.Fatalf("dir ID changed on rename: %d -> %d", dirID1, dirID2)
	}
}

func TestWalkSkipsJunction(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	writeSizedFile(t, filepath.Join(target, "one.bin"), 100)
	writeSizedFile(t, filepath.Join(target, "two.bin"), 200)
	link := filepath.Join(root, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}

	stats, err := Walk(context.Background(), root, Options{DedupeHardlinks: true}, nil)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if stats.Files != 2 {
		t.Fatalf("files = %d, want 2 (junction must not be followed)", stats.Files)
	}
	if stats.ReparseSkipped != 1 {
		t.Fatalf("reparse skipped = %d, want 1", stats.ReparseSkipped)
	}
}

func TestWalkDetectsHardlink(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.bin")
	writeSizedFile(t, original, 100)
	link := filepath.Join(root, "hard.bin")
	if out, err := exec.Command("cmd", "/c", "mklink", "/H", link, original).CombinedOutput(); err != nil {
		t.Skipf("mklink /H unavailable: %v (%s)", err, out)
	}

	stats, err := Walk(context.Background(), root, Options{DedupeHardlinks: true}, nil)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if stats.Files != 2 {
		t.Fatalf("files = %d, want 2", stats.Files)
	}
	if stats.Bytes != 200 {
		t.Fatalf("logical bytes = %d, want 200", stats.Bytes)
	}
	if stats.UniqueBytes != 100 {
		t.Fatalf("unique bytes = %d, want 100", stats.UniqueBytes)
	}
	if stats.HardlinkDupes != 1 {
		t.Fatalf("hardlink dupes = %d, want 1", stats.HardlinkDupes)
	}
}

func TestWalkUnicodeAndLongPath(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < 15; i++ {
		dir = filepath.Join(dir, fmt.Sprintf("%02d-%s", i, strings.Repeat("长", 20)))
	}
	file := filepath.Join(dir, "数据.txt")
	writeSizedFile(t, file, 7)

	stats, err := Walk(context.Background(), root, Options{DedupeHardlinks: true}, nil)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if stats.Files != 1 {
		t.Fatalf("files = %d, want 1", stats.Files)
	}
	if stats.Bytes != 7 {
		t.Fatalf("bytes = %d, want 7", stats.Bytes)
	}
}

func TestWalkIncludeDirs(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, filepath.Join(root, "a.txt"), 5)
	writeSizedFile(t, filepath.Join(root, "sub", "b.bin"), 1000)
	writeSizedFile(t, filepath.Join(root, "sub", "deep", "c"), 1)

	var dirs []model.FileRecord
	files := 0
	stats, err := Walk(context.Background(), root, Options{IncludeDirs: true, DedupeHardlinks: true}, func(r model.FileRecord) error {
		if r.IsDir {
			dirs = append(dirs, r)
		} else {
			files++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if files != 3 {
		t.Fatalf("file records = %d, want 3", files)
	}
	if len(dirs) != 3 {
		t.Fatalf("dir records = %d, want 3 (root, sub, sub/deep): %+v", len(dirs), dirs)
	}
	if dirs[0].ParentFRN != 0 {
		t.Fatalf("root parent = %d, want 0", dirs[0].ParentFRN)
	}
	if dirs[0].Name != root {
		t.Fatalf("root name = %q, want %q", dirs[0].Name, root)
	}
	byName := map[string]model.FileRecord{}
	for _, d := range dirs {
		byName[d.Name] = d
		if d.FRN == 0 {
			t.Errorf("dir %q has zero FRN", d.Name)
		}
	}
	if got := byName["sub"].ParentFRN; got != dirs[0].FRN {
		t.Fatalf("sub parent = %d, want root %d", got, dirs[0].FRN)
	}
	if got := byName["deep"].ParentFRN; got != byName["sub"].FRN {
		t.Fatalf("deep parent = %d, want sub %d", got, byName["sub"].FRN)
	}
	if stats.Dirs != 3 {
		t.Fatalf("stats dirs = %d, want 3", stats.Dirs)
	}
}

func TestVolumeMetadata(t *testing.T) {
	root := t.TempDir()
	vol := filepath.VolumeName(root) + `\`
	vm, err := VolumeMetadata(vol)
	if err != nil {
		t.Fatalf("volume metadata: %v", err)
	}
	if !vm.IsRoot {
		t.Fatalf("IsRoot = false for %q", vol)
	}
	if vm.Serial == 0 {
		t.Fatal("serial = 0")
	}
	if vm.FSType == "" {
		t.Fatal("empty file system type")
	}
	sub, err := VolumeMetadata(root)
	if err != nil {
		t.Fatalf("subdir metadata: %v", err)
	}
	if sub.IsRoot {
		t.Fatalf("IsRoot = true for subdir %q", root)
	}
	if sub.Serial != vm.Serial {
		t.Fatalf("serial mismatch: %08X vs %08X", sub.Serial, vm.Serial)
	}
}
