package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Xeayy/FatTrace/internal/model"
)

const (
	scanFilesColumns      = "volume_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs, is_dir"
	scanFilesPerStatement = 256
)

// ScanWriter streams records into the scan_files staging table inside a single
// transaction. Hard links share a FileID, so duplicate FRNs are ignored and each
// file is staged once (at the first path seen).
type ScanWriter struct {
	volumeID int64
	tx       *sql.Tx
	full     *sql.Stmt
	buf      []model.FileRecord
	args     []any
	inserted int64
	closed   bool
}

// BeginScan clears the previous staging rows for the volume and opens the
// staging transaction. Call Commit on success or Rollback on any failure.
func (s *Store) BeginScan(ctx context.Context, volumeID int64) (*ScanWriter, error) {
	if s.reader {
		return nil, errors.New("store: read-only connection")
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM scan_files WHERE volume_id = ?", volumeID); err != nil {
		return nil, fmt.Errorf("clear scan staging: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	full, err := tx.PrepareContext(ctx, buildInsertIgnoreSQL("scan_files", scanFilesColumns, scanFilesPerStatement, 9))
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return &ScanWriter{
		volumeID: volumeID,
		tx:       tx,
		full:     full,
		buf:      make([]model.FileRecord, 0, scanFilesPerStatement),
		args:     make([]any, 0, scanFilesPerStatement*9),
	}, nil
}

func (w *ScanWriter) Add(ctx context.Context, rec model.FileRecord) error {
	if w.closed {
		return errors.New("store: scan writer is closed")
	}
	w.buf = append(w.buf, rec)
	if len(w.buf) < scanFilesPerStatement {
		return nil
	}
	return w.flush(ctx, w.full)
}

func (w *ScanWriter) Inserted() int64 { return w.inserted }

// Commit flushes the tail batch and commits the staging transaction.
func (w *ScanWriter) Commit(ctx context.Context) (int64, error) {
	if w.closed {
		return w.inserted, errors.New("store: scan writer is closed")
	}
	if rem := len(w.buf); rem > 0 {
		tail, err := w.tx.PrepareContext(ctx, buildInsertIgnoreSQL("scan_files", scanFilesColumns, rem, 9))
		if err != nil {
			w.Rollback()
			return 0, err
		}
		err = w.flush(ctx, tail)
		tail.Close()
		if err != nil {
			w.Rollback()
			return 0, err
		}
	}
	if err := w.tx.Commit(); err != nil {
		w.Rollback()
		return 0, err
	}
	w.full.Close()
	w.closed = true
	return w.inserted, nil
}

func (w *ScanWriter) Rollback() {
	if w.closed {
		return
	}
	w.tx.Rollback()
	w.full.Close()
	w.closed = true
}

func (w *ScanWriter) flush(ctx context.Context, stmt *sql.Stmt) error {
	if len(w.buf) == 0 {
		return nil
	}
	w.args = w.args[:0]
	for i := range w.buf {
		r := &w.buf[i]
		w.args = append(w.args,
			w.volumeID, int64(r.FRN), int64(r.ParentFRN), r.Name,
			r.Size, r.AllocSize, mtimeMillis(r.MTime), int64(r.Attrs), boolInt(r.IsDir))
	}
	res, err := stmt.ExecContext(ctx, w.args...)
	if err != nil {
		return fmt.Errorf("stage %d rows: %w", len(w.buf), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	w.inserted += n
	w.buf = w.buf[:0]
	return nil
}

// ClearScan removes staged rows (used after an aborted scan when the writer has
// already been rolled back).
func (s *Store) ClearScan(ctx context.Context, volumeID int64) error {
	if s.reader {
		return errors.New("store: read-only connection")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM scan_files WHERE volume_id = ?", volumeID)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
