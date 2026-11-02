package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"fattrace/internal/model"
)

// ErrSnapshotNotFound reports an unknown or filtered-out snapshot id.
var ErrSnapshotNotFound = errors.New("store: snapshot not found")

const snapshotSelectSQL = `SELECT id, volume_id, kind, status, root, started_at, finished_at, duration_ms,
	files, dirs, unique_alloc_bytes, volume_used, errors, coverage_pct
	FROM snapshots`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshotInfo(row rowScanner) (SnapshotInfo, error) {
	var sn SnapshotInfo
	var finished sql.NullInt64
	err := row.Scan(&sn.ID, &sn.VolumeID, &sn.Kind, &sn.Status, &sn.Root, &sn.StartedAt,
		&finished, &sn.DurationMS, &sn.Files, &sn.Dirs, &sn.UniqueAllocBytes, &sn.VolumeUsed,
		&sn.Errors, &sn.CoveragePct)
	if err != nil {
		return SnapshotInfo{}, err
	}
	if finished.Valid {
		sn.FinishedAt = &finished.Int64
	}
	return sn, nil
}

// GetSnapshot returns one snapshot row by id.
func (s *Store) GetSnapshot(ctx context.Context, id int64) (SnapshotInfo, error) {
	sn, err := scanSnapshotInfo(s.db.QueryRowContext(ctx, snapshotSelectSQL+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotInfo{}, fmt.Errorf("%w: #%d", ErrSnapshotNotFound, id)
	}
	return sn, err
}

// LatestComplete returns the newest complete snapshot, optionally filtered by a
// root prefix such as `D:`. An empty prefix means any volume.
func (s *Store) LatestComplete(ctx context.Context, rootPrefix string) (SnapshotInfo, error) {
	like := "%"
	if rootPrefix != "" {
		like = rootPrefix + "%"
	}
	sn, err := scanSnapshotInfo(s.db.QueryRowContext(ctx,
		snapshotSelectSQL+` WHERE status = 'complete' AND root LIKE ?
		ORDER BY started_at DESC, id DESC LIMIT 1`, like))
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotInfo{}, fmt.Errorf("%w (no complete snapshot for %q)", ErrSnapshotNotFound, rootPrefix)
	}
	return sn, err
}

// RecentComplete lists the newest complete snapshots, optionally filtered by a
// root prefix such as `D:`. Callers pick the first entry that suits them.
func (s *Store) RecentComplete(ctx context.Context, rootPrefix string, limit int) ([]SnapshotInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	like := "%"
	if rootPrefix != "" {
		like = rootPrefix + "%"
	}
	rows, err := s.db.QueryContext(ctx, snapshotSelectSQL+`
		WHERE status = 'complete' AND root LIKE ?
		ORDER BY started_at DESC, id DESC LIMIT ?`, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotInfo
	for rows.Next() {
		sn, err := scanSnapshotInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// PreviousComplete returns the newest complete snapshot with the same volume and
// root older than beforeID. Root matching keeps subdirectory snapshots from
// being paired with volume-root snapshots.
func (s *Store) PreviousComplete(ctx context.Context, volumeID, beforeID int64, root string) (SnapshotInfo, error) {
	sn, err := scanSnapshotInfo(s.db.QueryRowContext(ctx,
		snapshotSelectSQL+` WHERE volume_id = ? AND status = 'complete' AND id < ?
		AND lower(root) = lower(?)
		ORDER BY started_at DESC, id DESC LIMIT 1`, volumeID, beforeID, root))
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotInfo{}, fmt.Errorf("%w (no complete snapshot before #%d for %s)", ErrSnapshotNotFound, beforeID, root)
	}
	return sn, err
}

// NextComplete returns the oldest complete snapshot with the same volume and
// root newer than afterID.
func (s *Store) NextComplete(ctx context.Context, volumeID, afterID int64, root string) (SnapshotInfo, error) {
	sn, err := scanSnapshotInfo(s.db.QueryRowContext(ctx,
		snapshotSelectSQL+` WHERE volume_id = ? AND status = 'complete' AND id > ?
		AND lower(root) = lower(?)
		ORDER BY started_at ASC, id ASC LIMIT 1`, volumeID, afterID, root))
	if errors.Is(err, sql.ErrNoRows) {
		return SnapshotInfo{}, fmt.Errorf("%w (no complete snapshot after #%d for %s)", ErrSnapshotNotFound, afterID, root)
	}
	return sn, err
}

// LoadCrossVolumeCandidates returns name/size/mtime keys from the latest
// complete volume-root snapshot of every volume except excludeVolumeID. Only
// metadata is kept; callers build their own index.
func (s *Store) LoadCrossVolumeCandidates(ctx context.Context, excludeVolumeID int64) ([]model.FileCandidate, error) {
	snaps, err := s.CompleteVolumeRootSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	latest := make(map[int64]SnapshotInfo)
	for _, sn := range snaps {
		if sn.VolumeID == excludeVolumeID {
			continue
		}
		cur, ok := latest[sn.VolumeID]
		if !ok || sn.StartedAt > cur.StartedAt ||
			(sn.StartedAt == cur.StartedAt && sn.ID > cur.ID) {
			latest[sn.VolumeID] = sn
		}
	}
	if len(latest) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(latest))
	for _, sn := range latest {
		ids = append(ids, sn.ID)
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.QueryContext(ctx, `
		SELECT sf.name, sf.size, sf.mtime_ms, v.serial
		FROM snap_files sf
		JOIN snapshots s ON s.id = sf.snapshot_id
		JOIN volumes v ON v.id = s.volume_id
		WHERE s.id IN (`+placeholders+`) AND sf.mtime_ms > 0`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FileCandidate
	for rows.Next() {
		var c model.FileCandidate
		if err := rows.Scan(&c.Name, &c.Size, &c.MTimeMS, &c.VolumeSerial); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// StreamSnapshotFiles calls fn for every file row of a snapshot in FRN order.
// The callback runs while the query holds the store's single connection, so it
// must not issue further store queries.
func (s *Store) StreamSnapshotFiles(ctx context.Context, snapshotID int64, fn func(model.FileRecord) error) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT frn, parent_frn, name, size, alloc_size, mtime_ms, attrs
		 FROM snap_files WHERE snapshot_id = ? ORDER BY frn`, snapshotID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r model.FileRecord
		var frn, parent, mtime, attrs int64
		if err := rows.Scan(&frn, &parent, &r.Name, &r.Size, &r.AllocSize, &mtime, &attrs); err != nil {
			return err
		}
		r.FRN = uint64(frn)
		r.ParentFRN = uint64(parent)
		r.Attrs = uint32(attrs)
		if mtime > 0 {
			r.MTime = time.UnixMilli(mtime)
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// LoadSnapshotFiles returns every file row of a snapshot, ordered by FRN.
func (s *Store) LoadSnapshotFiles(ctx context.Context, snapshotID int64) ([]model.FileRecord, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, snapshotID).Scan(&n); err != nil {
		return nil, err
	}
	recs := make([]model.FileRecord, 0, n)
	err := s.StreamSnapshotFiles(ctx, snapshotID, func(r model.FileRecord) error {
		recs = append(recs, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return recs, nil
}

// LoadSnapshotFileIndex loads a snapshot into a FRN-indexed map. It is the
// memory-cheaper counterpart of LoadSnapshotFiles for diff alignment.
func (s *Store) LoadSnapshotFileIndex(ctx context.Context, snapshotID int64) (map[uint64]model.FileRecord, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ?`, snapshotID).Scan(&n); err != nil {
		return nil, err
	}
	index := make(map[uint64]model.FileRecord, n)
	err := s.StreamSnapshotFiles(ctx, snapshotID, func(r model.FileRecord) error {
		index[r.FRN] = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

// ScanErrorInfo is one aggregated scan error as recorded by the most recent
// scan that hit it.
type ScanErrorInfo struct {
	Path       string `json:"path"`
	Op         string `json:"op"`
	Msg        string `json:"msg"`
	Hits       int64  `json:"hits"`
	LastSeenAt int64  `json:"last_seen_at_ms"`
}

// SnapshotErrors returns the errors whose last occurrence was the given
// snapshot, most frequent first. Errors seen only by later scans are excluded.
func (s *Store) SnapshotErrors(ctx context.Context, snapshotID int64, limit int) ([]ScanErrorInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, op, msg, hits, last_seen_at FROM scan_errors
		WHERE last_snapshot_id = ?
		ORDER BY hits DESC, last_seen_at DESC, path
		LIMIT ?`, snapshotID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanErrorInfo
	for rows.Next() {
		var e ScanErrorInfo
		if err := rows.Scan(&e.Path, &e.Op, &e.Msg, &e.Hits, &e.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LoadSnapshotDirs returns every directory row of a snapshot, ordered by FRN.
func (s *Store) LoadSnapshotDirs(ctx context.Context, snapshotID int64) ([]model.SnapshotDir, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ?`, snapshotID).Scan(&n); err != nil {
		return nil, err
	}
	dirs := make([]model.SnapshotDir, 0, n)
	rows, err := s.db.QueryContext(ctx,
		`SELECT frn, parent_frn, name, depth, total_bytes, total_alloc_bytes, total_files, unknown
		 FROM snap_dirs WHERE snapshot_id = ? ORDER BY frn`, snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d model.SnapshotDir
		var frn, parent, depth, unknown int64
		if err := rows.Scan(&frn, &parent, &d.Name, &depth, &d.TotalBytes, &d.TotalAllocBytes,
			&d.TotalFiles, &unknown); err != nil {
			return nil, err
		}
		d.FRN = uint64(frn)
		d.ParentFRN = uint64(parent)
		d.Depth = int(depth)
		d.Unknown = unknown != 0
		dirs = append(dirs, d)
	}
	return dirs, rows.Err()
}
