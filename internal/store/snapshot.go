package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Xeayy/FatTrace/internal/model"
)

const updateSnapshotSQL = `UPDATE snapshots SET
	status = ?, finished_at = ?, duration_ms = ?, files = ?, dirs = ?, bytes = ?, alloc_bytes = ?,
	unique_bytes = ?, unique_alloc_bytes = ?, hardlink_dupes = ?, reparse_skipped = ?, errors = ?,
	volume_total = ?, volume_free = ?, volume_used = ?, coverage_pct = ?, heap_peak_bytes = ?
	WHERE id = ?`

const copyCurrentFilesSQL = `INSERT INTO current_files
	(volume_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs)
	SELECT volume_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs
	FROM scan_files WHERE volume_id = ? AND is_dir = 0`

const copySnapFilesSQL = `INSERT INTO snap_files
	(snapshot_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs)
	SELECT ?, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs
	FROM scan_files WHERE volume_id = ? AND is_dir = 0`

const copyCurrentDirsSQL = `INSERT INTO current_dirs
	(volume_id, frn, parent_frn, name, attrs, total_bytes, total_alloc_bytes, total_files, unknown)
	SELECT ?, d.frn, d.parent_frn, d.name, COALESCE(s.attrs, 0),
	       d.total_bytes, d.total_alloc_bytes, d.total_files, d.unknown
	FROM snap_dirs d
	JOIN scan_files s ON s.volume_id = ? AND s.frn = d.frn AND s.is_dir = 1
	WHERE d.snapshot_id = ?`

const snapDirsColumns = "snapshot_id, frn, parent_frn, name, depth, total_bytes, total_alloc_bytes, total_files, unknown"

const upsertScanErrorSQL = `INSERT INTO scan_errors
	(path, op, msg, first_seen_at, last_seen_at, last_snapshot_id, hits)
	VALUES (?, ?, ?, ?, ?, ?, 1)
	ON CONFLICT(path, op, msg) DO UPDATE SET
		last_seen_at = excluded.last_seen_at,
		last_snapshot_id = excluded.last_snapshot_id,
		hits = scan_errors.hits + 1`

var validKinds = map[string]bool{"daily": true, "weekly": true, "manual": true}

// BeginSnapshot records a running snapshot. Any other running snapshot for the
// same volume is marked failed: the writer lock guarantees those are stale.
func (s *Store) BeginSnapshot(ctx context.Context, volumeID int64, kind, root string) (int64, error) {
	if s.reader {
		return 0, errors.New("store: read-only connection")
	}
	if !validKinds[kind] {
		return 0, fmt.Errorf("invalid snapshot kind %q", kind)
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE snapshots SET status = 'failed', finished_at = ? WHERE volume_id = ? AND status = 'running'`,
		now, volumeID); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO snapshots (volume_id, kind, status, root, started_at) VALUES (?, ?, 'running', ?, ?)`,
		volumeID, kind, root, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// FinishSnapshot closes a snapshot without writing file/dir data (partial or
// failed scans).
func (s *Store) FinishSnapshot(ctx context.Context, id int64, status string, stats model.ScanStats) error {
	if s.reader {
		return errors.New("store: read-only connection")
	}
	if status != "partial" && status != "failed" {
		return fmt.Errorf("FinishSnapshot: invalid status %q", status)
	}
	_, err := s.db.ExecContext(ctx, updateSnapshotSQL, snapshotArgs(id, status, stats)...)
	return err
}

type SnapshotCommit struct {
	SnapshotID     int64
	VolumeID       int64
	Status         string
	Stats          model.ScanStats
	Dirs           []DirTotal
	Errors         []model.ScanError
	UpdateBaseline bool
}

// CommitSnapshot atomically publishes a completed scan: file-level snapshot
// rows, directory aggregates, the volume baseline (optional) and the snapshot
// metadata all commit together. On failure nothing is published.
func (s *Store) CommitSnapshot(ctx context.Context, c SnapshotCommit) error {
	if s.reader {
		return errors.New("store: read-only connection")
	}
	if c.Status != "complete" {
		return fmt.Errorf("CommitSnapshot: status must be complete, got %q", c.Status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if c.UpdateBaseline {
		if _, err := tx.ExecContext(ctx, `DELETE FROM current_files WHERE volume_id = ?`, c.VolumeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM current_dirs WHERE volume_id = ?`, c.VolumeID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, copyCurrentFilesSQL, c.VolumeID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, copySnapFilesSQL, c.SnapshotID, c.VolumeID); err != nil {
		return err
	}
	if err := insertSnapDirs(ctx, tx, c.SnapshotID, c.Dirs); err != nil {
		return err
	}
	if c.UpdateBaseline {
		if _, err := tx.ExecContext(ctx, copyCurrentDirsSQL, c.VolumeID, c.VolumeID, c.SnapshotID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, updateSnapshotSQL, snapshotArgs(c.SnapshotID, c.Status, c.Stats)...); err != nil {
		return err
	}
	if err := upsertScanErrors(ctx, tx, c.SnapshotID, c.Errors); err != nil {
		return err
	}
	// Publication is atomic with clearing the staging table: scan_files only
	// exists to feed this transaction, and keeping it would duplicate snap_files.
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_files WHERE volume_id = ?`, c.VolumeID); err != nil {
		return err
	}
	return tx.Commit()
}

func snapshotArgs(id int64, status string, stats model.ScanStats) []any {
	return []any{
		status, time.Now().UnixMilli(), stats.DurationMS, stats.Files, stats.Dirs,
		stats.Bytes, stats.AllocBytes, stats.UniqueBytes, stats.UniqueAllocBytes,
		stats.HardlinkDupes, stats.ReparseSkipped, stats.Errors,
		stats.VolumeTotal, stats.VolumeFree, stats.VolumeUsed, stats.CoveragePct,
		int64(stats.HeapPeakBytes), id,
	}
}

func insertSnapDirs(ctx context.Context, tx *sql.Tx, snapshotID int64, dirs []DirTotal) error {
	if len(dirs) == 0 {
		return nil
	}
	batch := scanFilesPerStatement
	if len(dirs) < batch {
		batch = len(dirs)
	}
	full, err := tx.PrepareContext(ctx, buildInsertSQL("snap_dirs", snapDirsColumns, batch, 9))
	if err != nil {
		return err
	}
	defer full.Close()

	var tail *sql.Stmt
	if rem := len(dirs) % batch; rem > 0 {
		tail, err = tx.PrepareContext(ctx, buildInsertSQL("snap_dirs", snapDirsColumns, rem, 9))
		if err != nil {
			return err
		}
		defer tail.Close()
	}

	args := make([]any, 0, batch*9)
	for start := 0; start < len(dirs); start += batch {
		end := start + batch
		if end > len(dirs) {
			end = len(dirs)
		}
		args = args[:0]
		for i := start; i < end; i++ {
			d := &dirs[i]
			args = append(args, snapshotID, int64(d.FRN), int64(d.ParentFRN), d.Name, d.Depth,
				d.Bytes, d.AllocBytes, d.Files, boolInt(d.Unknown))
		}
		stmt := full
		if end-start != batch {
			stmt = tail
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert snap_dirs rows %d-%d: %w", start, end, err)
		}
	}
	return nil
}

func upsertScanErrors(ctx context.Context, tx *sql.Tx, snapshotID int64, errs []model.ScanError) error {
	if len(errs) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	stmt, err := tx.PrepareContext(ctx, upsertScanErrorSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := range errs {
		e := &errs[i]
		if _, err := stmt.ExecContext(ctx, e.Path, e.Op, e.Msg, now, now, snapshotID); err != nil {
			return fmt.Errorf("upsert scan_error %s: %w", e.Path, err)
		}
	}
	return nil
}

// Checkpoint truncates the WAL after a large write burst.
func (s *Store) Checkpoint(ctx context.Context) error {
	if s.reader {
		return errors.New("store: read-only connection")
	}
	_, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
