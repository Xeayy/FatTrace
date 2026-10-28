//go:build windows

package snap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"fattrace/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	q := url.Values{}
	q.Add("_pragma", "query_only(ON)")
	q.Add("_pragma", "busy_timeout(5000)")
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path), RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func scalarInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func TestRunSubdirSnapshot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), 5)
	writeFile(t, filepath.Join(root, "sub", "b.bin"), 1000)
	writeFile(t, filepath.Join(root, "sub", "deep", "c"), 1)

	st := newStore(t)
	res, err := Run(context.Background(), st, Options{Root: root, Kind: "manual"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != "complete" {
		t.Fatalf("status = %q, want complete", res.Status)
	}
	if res.BaselineUpdated {
		t.Fatal("subdirectory scan must not update the volume baseline")
	}
	if res.Files != 3 || res.Dirs != 3 {
		t.Fatalf("files/dirs = %d/%d, want 3/3", res.Files, res.Dirs)
	}
	if res.StagedRows != 6 || res.SnapshotDirs != 3 {
		t.Fatalf("staged/snapshot dirs = %d/%d, want 6/3", res.StagedRows, res.SnapshotDirs)
	}

	db := openRaw(t, st.Path())
	var status string
	var files, dirs int64
	if err := db.QueryRow(`SELECT status, files, dirs FROM snapshots WHERE id = ?`, res.SnapshotID).
		Scan(&status, &files, &dirs); err != nil {
		t.Fatal(err)
	}
	if status != "complete" || files != 3 || dirs != 3 {
		t.Fatalf("snapshot row = %q %d/%d, want complete 3/3", status, files, dirs)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, res.SnapshotID); n != 3 {
		t.Fatalf("snap_files = %d, want 3", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ?`, res.SnapshotID); n != 3 {
		t.Fatalf("snap_dirs = %d, want 3", n)
	}

	var rootBytes, rootFiles, rootDepth, unknown int64
	if err := db.QueryRow(`SELECT total_bytes, total_files, depth, unknown FROM snap_dirs
		WHERE snapshot_id = ? AND parent_frn = 0`, res.SnapshotID).
		Scan(&rootBytes, &rootFiles, &rootDepth, &unknown); err != nil {
		t.Fatal(err)
	}
	if rootBytes != 1006 || rootFiles != 3 || rootDepth != 0 || unknown != 0 {
		t.Fatalf("root dir = bytes %d files %d depth %d unknown %d, want 1006/3/0/0",
			rootBytes, rootFiles, rootDepth, unknown)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ? AND depth = 2`, res.SnapshotID); n != 1 {
		t.Fatalf("depth-2 dirs = %d, want 1", n)
	}

	if n := countRows(t, db, `SELECT COUNT(*) FROM current_files`); n != 0 {
		t.Fatalf("current_files = %d after subdir scan, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM current_dirs`); n != 0 {
		t.Fatalf("current_dirs = %d after subdir scan, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM scan_files`); n != 0 {
		t.Fatalf("scan_files = %d after commit, want 0 (staging cleared)", n)
	}
}

func TestRunRepeatable(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "one.bin"), 100)
	writeFile(t, filepath.Join(root, "sub", "two.bin"), 200)
	writeFile(t, filepath.Join(root, "sub", "three.bin"), 300)

	st := newStore(t)
	ctx := context.Background()
	first, err := Run(ctx, st, Options{Root: root, Kind: "weekly"})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	second, err := Run(ctx, st, Options{Root: root, Kind: "weekly"})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if first.SnapshotID == second.SnapshotID {
		t.Fatal("second run reused the first snapshot id")
	}
	if first.Files != second.Files || first.Dirs != second.Dirs ||
		first.StagedRows != second.StagedRows || first.SnapshotDirs != second.SnapshotDirs {
		t.Fatalf("runs differ: %+v vs %+v", first, second)
	}
	if first.Stats.Bytes != second.Stats.Bytes || first.Stats.AllocBytes != second.Stats.AllocBytes {
		t.Fatalf("byte totals differ: %+v vs %+v", first.Stats, second.Stats)
	}

	db := openRaw(t, st.Path())
	if n := countRows(t, db, `SELECT COUNT(*) FROM snapshots WHERE status = 'complete'`); n != 2 {
		t.Fatalf("complete snapshots = %d, want 2", n)
	}
	for _, id := range []int64{first.SnapshotID, second.SnapshotID} {
		if n := countRows(t, db, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, id); n != 3 {
			t.Fatalf("snap_files for #%d = %d, want 3", id, n)
		}
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM scan_files`); n != 0 {
		t.Fatalf("scan_files = %d after two commits, want 0", n)
	}
}

func TestRunHardlinkDedup(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original.bin")
	writeFile(t, original, 100)
	link := filepath.Join(root, "hard.bin")
	if out, err := exec.Command("cmd", "/c", "mklink", "/H", link, original).CombinedOutput(); err != nil {
		t.Skipf("mklink /H unavailable: %v (%s)", err, out)
	}

	st := newStore(t)
	res, err := Run(context.Background(), st, Options{Root: root, Kind: "manual"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Files != 2 || res.Stats.HardlinkDupes != 1 || res.Stats.Bytes != 200 {
		t.Fatalf("stats = files %d dupes %d bytes %d, want 2/1/200",
			res.Files, res.Stats.HardlinkDupes, res.Stats.Bytes)
	}
	if res.StagedRows != 2 {
		t.Fatalf("staged rows = %d, want 2 (root dir + one file identity)", res.StagedRows)
	}

	db := openRaw(t, st.Path())
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, res.SnapshotID); n != 1 {
		t.Fatalf("snap_files = %d, want 1", n)
	}
	if got := scalarInt(t, db, `SELECT total_bytes FROM snap_dirs WHERE snapshot_id = ? AND parent_frn = 0`, res.SnapshotID); got != 100 {
		t.Fatalf("root total_bytes = %d, want 100 (unique)", got)
	}
	if got := scalarInt(t, db, `SELECT unique_bytes FROM snapshots WHERE id = ?`, res.SnapshotID); got != 100 {
		t.Fatalf("snapshot unique bytes = %d, want 100", got)
	}
}

func TestRunCancelledMidScan(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		for j := 0; j < 50; j++ {
			dir := filepath.Join(root, fmt.Sprintf("d%02d", i), fmt.Sprintf("s%02d", j))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
	}

	st := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res, err := Run(ctx, st, Options{
		Root:             root,
		Kind:             "manual",
		ProgressInterval: 500 * time.Microsecond,
		Progress:         func(files, dirs int64) { cancel() },
	})
	if err == nil {
		t.Fatalf("cancelled run returned no error (status %q)", res.Status)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if res.Status != "partial" {
		t.Fatalf("status = %q, want partial", res.Status)
	}
	if res.BaselineUpdated {
		t.Fatal("partial scan must not update the baseline")
	}

	db := openRaw(t, st.Path())
	var status string
	if err := db.QueryRow(`SELECT status FROM snapshots WHERE id = ?`, res.SnapshotID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "partial" {
		t.Fatalf("snapshot status = %q, want partial", status)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, res.SnapshotID); n != 0 {
		t.Fatalf("partial snapshot rows = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ?`, res.SnapshotID); n != 0 {
		t.Fatalf("partial dir rows = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM scan_files`); n != 0 {
		t.Fatalf("staging rows = %d after partial, want 0", n)
	}
}
