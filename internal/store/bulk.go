package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/model"
)

const (
	currentFilesColumns = "volume_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs"
	snapFilesColumns    = "snapshot_id, frn, parent_frn, name, size, alloc_size, mtime_ms, attrs"
	filesPerStatement   = 256
)

// InsertCurrentFiles inserts baseline rows in one transaction. The caller is
// responsible for clearing the previous baseline rows for the same volume.
func (s *Store) InsertCurrentFiles(ctx context.Context, volumeID int64, recs []model.FileRecord) (int64, error) {
	return s.bulkInsert(ctx, "current_files", currentFilesColumns, volumeID, recs)
}

// InsertSnapFiles writes file-level rows for a weekly snapshot in one transaction.
func (s *Store) InsertSnapFiles(ctx context.Context, snapshotID int64, recs []model.FileRecord) (int64, error) {
	return s.bulkInsert(ctx, "snap_files", snapFilesColumns, snapshotID, recs)
}

func (s *Store) bulkInsert(ctx context.Context, table, columns string, extra any, recs []model.FileRecord) (int64, error) {
	if s.reader {
		return 0, errors.New("store: read-only connection")
	}
	if len(recs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	fullStmt, err := tx.PrepareContext(ctx, buildInsertSQL(table, columns, filesPerStatement, 8))
	if err != nil {
		return 0, err
	}
	defer fullStmt.Close()

	var tailStmt *sql.Stmt
	if rem := len(recs) % filesPerStatement; rem > 0 {
		tailStmt, err = tx.PrepareContext(ctx, buildInsertSQL(table, columns, rem, 8))
		if err != nil {
			return 0, err
		}
		defer tailStmt.Close()
	}

	args := make([]any, 0, filesPerStatement*8)
	for start := 0; start < len(recs); start += filesPerStatement {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		end := start + filesPerStatement
		if end > len(recs) {
			end = len(recs)
		}
		args = args[:0]
		for i := start; i < end; i++ {
			r := &recs[i]
			args = append(args, extra, int64(r.FRN), int64(r.ParentFRN), r.Name,
				r.Size, r.AllocSize, mtimeMillis(r.MTime), int64(r.Attrs))
		}
		stmt := fullStmt
		if end-start != filesPerStatement {
			stmt = tailStmt
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return 0, fmt.Errorf("insert rows %d-%d into %s: %w", start, end, table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(recs)), nil
}

func buildInsertSQL(table, columns string, rows, cols int) string {
	return buildInsertSQLVerb("INSERT INTO", table, columns, rows, cols)
}

func buildInsertIgnoreSQL(table, columns string, rows, cols int) string {
	return buildInsertSQLVerb("INSERT OR IGNORE INTO", table, columns, rows, cols)
}

func buildInsertSQLVerb(verb, table, columns string, rows, cols int) string {
	row := "(?" + strings.Repeat(",?", cols-1) + ")"
	var b strings.Builder
	b.Grow(len(verb) + len(table) + len(columns) + rows*(len(row)+1) + 32)
	b.WriteString(verb)
	b.WriteByte(' ')
	b.WriteString(table)
	b.WriteString(" (")
	b.WriteString(columns)
	b.WriteString(") VALUES ")
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(row)
	}
	return b.String()
}

func mtimeMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
