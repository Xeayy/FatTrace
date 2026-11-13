package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Xeayy/FatTrace/internal/model"
)

// ErrDirNotFound reports an unknown directory FRN inside a snapshot.
var ErrDirNotFound = errors.New("store: directory not found")

// DirNode describes one snap_dirs row for breadcrumbs and drilldown headers.
type DirNode struct {
	FRN             uint64 `json:"frn"`
	ParentFRN       uint64 `json:"parent_frn"`
	Name            string `json:"name"`
	Depth           int    `json:"depth"`
	TotalBytes      int64  `json:"total_bytes"`
	TotalAllocBytes int64  `json:"total_alloc_bytes"`
	TotalFiles      int64  `json:"total_files"`
	Unknown         bool   `json:"unknown,omitempty"`
}

// DirChild is one entry of a directory listing. Directory rows carry recursive
// totals; file rows only their own size.
type DirChild struct {
	FRN     uint64 `json:"frn"`
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Alloc   int64  `json:"alloc_size"`
	Files   int64  `json:"files"`
	MTimeMS int64  `json:"mtime_ms,omitempty"`
	Unknown bool   `json:"unknown,omitempty"`
}

// DirListing is everything the UI needs to render one drilldown level.
type DirListing struct {
	Snapshot       SnapshotInfo `json:"snapshot"`
	Dir            DirNode      `json:"dir"`
	Breadcrumb     []DirNode    `json:"breadcrumb"`
	Path           string       `json:"path"`
	Dirs           []DirChild   `json:"dirs"`
	Files          []DirChild   `json:"files"`
	DirsTotal      int64        `json:"dirs_total"`
	FilesTotal     int64        `json:"files_total"`
	DirsTruncated  bool         `json:"dirs_truncated,omitempty"`
	FilesTruncated bool         `json:"files_truncated,omitempty"`
	FilesExpired   bool         `json:"files_expired,omitempty"`
}

const dirNodeSelectSQL = `SELECT frn, parent_frn, name, depth, total_bytes, total_alloc_bytes,
	total_files, unknown FROM snap_dirs WHERE snapshot_id = ?`

func scanDirNode(row rowScanner) (DirNode, error) {
	var d DirNode
	var frn, parent, depth, unknown int64
	if err := row.Scan(&frn, &parent, &d.Name, &depth, &d.TotalBytes, &d.TotalAllocBytes,
		&d.TotalFiles, &unknown); err != nil {
		return DirNode{}, err
	}
	d.FRN = uint64(frn)
	d.ParentFRN = uint64(parent)
	d.Depth = int(depth)
	d.Unknown = unknown != 0
	return d, nil
}

// BrowseDir returns one directory level of a snapshot: the directory itself,
// its breadcrumb path and its direct child directories and files, each sorted by
// allocated size. FRN 0 resolves to the snapshot's scan root.
func (s *Store) BrowseDir(ctx context.Context, snapshotID int64, frn uint64, dirLimit, fileLimit int) (DirListing, error) {
	if dirLimit <= 0 {
		dirLimit = 500
	}
	if fileLimit <= 0 {
		fileLimit = 200
	}
	sn, err := s.GetSnapshot(ctx, snapshotID)
	if err != nil {
		return DirListing{}, err
	}

	var dir DirNode
	if frn == 0 {
		dir, err = scanDirNode(s.db.QueryRowContext(ctx,
			dirNodeSelectSQL+" AND parent_frn = 0 ORDER BY depth ASC LIMIT 1", snapshotID))
	} else {
		dir, err = scanDirNode(s.db.QueryRowContext(ctx, dirNodeSelectSQL+" AND frn = ?", snapshotID, int64(frn)))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return DirListing{}, fmt.Errorf("%w (snapshot #%d frn %d)", ErrDirNotFound, snapshotID, frn)
	}
	if err != nil {
		return DirListing{}, err
	}

	crumb, err := s.dirBreadcrumb(ctx, snapshotID, dir.FRN)
	if err != nil {
		return DirListing{}, err
	}

	res := DirListing{Snapshot: sn, Dir: dir, Breadcrumb: crumb, Path: dirPath(crumb)}
	if sn.Files > 0 {
		n, err := s.CountSnapshotFiles(ctx, []int64{snapshotID})
		if err != nil {
			return DirListing{}, err
		}
		res.FilesExpired = n == 0
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snap_dirs WHERE snapshot_id = ? AND parent_frn = ?`,
		snapshotID, int64(dir.FRN)).Scan(&res.DirsTotal); err != nil {
		return DirListing{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM snap_files WHERE snapshot_id = ? AND parent_frn = ?`,
		snapshotID, int64(dir.FRN)).Scan(&res.FilesTotal); err != nil {
		return DirListing{}, err
	}
	res.DirsTruncated = res.DirsTotal > int64(dirLimit)
	res.FilesTruncated = res.FilesTotal > int64(fileLimit)

	drows, err := s.db.QueryContext(ctx, `
		SELECT frn, name, total_bytes, total_alloc_bytes, total_files, unknown
		FROM snap_dirs WHERE snapshot_id = ? AND parent_frn = ?
		ORDER BY total_alloc_bytes DESC, name LIMIT ?`, snapshotID, int64(dir.FRN), dirLimit)
	if err != nil {
		return DirListing{}, err
	}
	defer drows.Close()
	for drows.Next() {
		var c DirChild
		var cfrn, unknown int64
		if err := drows.Scan(&cfrn, &c.Name, &c.Size, &c.Alloc, &c.Files, &unknown); err != nil {
			return DirListing{}, err
		}
		c.FRN = uint64(cfrn)
		c.IsDir = true
		c.Unknown = unknown != 0
		res.Dirs = append(res.Dirs, c)
	}
	if err := drows.Err(); err != nil {
		return DirListing{}, err
	}

	frows, err := s.db.QueryContext(ctx, `
		SELECT frn, name, size, alloc_size, mtime_ms
		FROM snap_files WHERE snapshot_id = ? AND parent_frn = ?
		ORDER BY alloc_size DESC, name LIMIT ?`, snapshotID, int64(dir.FRN), fileLimit)
	if err != nil {
		return DirListing{}, err
	}
	defer frows.Close()
	for frows.Next() {
		var c DirChild
		var cfrn int64
		if err := frows.Scan(&cfrn, &c.Name, &c.Size, &c.Alloc, &c.MTimeMS); err != nil {
			return DirListing{}, err
		}
		c.FRN = uint64(cfrn)
		res.Files = append(res.Files, c)
	}
	return res, frows.Err()
}

// dirBreadcrumb walks parent links up to the scan root. The depth guard breaks
// accidental cycles caused by corrupt parent pointers.
func (s *Store) dirBreadcrumb(ctx context.Context, snapshotID int64, frn uint64) ([]DirNode, error) {
	const q = `
		WITH RECURSIVE up(frn, parent_frn, name, depth, total_bytes, total_alloc_bytes,
		                  total_files, unknown, hop) AS (
			SELECT frn, parent_frn, name, depth, total_bytes, total_alloc_bytes, total_files, unknown, 0
			FROM snap_dirs WHERE snapshot_id = ? AND frn = ?
			UNION ALL
			SELECT d.frn, d.parent_frn, d.name, d.depth, d.total_bytes, d.total_alloc_bytes,
			       d.total_files, d.unknown, up.hop + 1
			FROM snap_dirs d JOIN up ON d.frn = up.parent_frn AND d.snapshot_id = ?
			WHERE up.hop < 256
		)
		SELECT frn, parent_frn, name, depth, total_bytes, total_alloc_bytes, total_files, unknown
		FROM up ORDER BY hop DESC`
	rows, err := s.db.QueryContext(ctx, q, snapshotID, int64(frn), snapshotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirNode
	for rows.Next() {
		var d DirNode
		var dfrn, parent, depth, unknown int64
		if err := rows.Scan(&dfrn, &parent, &d.Name, &depth, &d.TotalBytes, &d.TotalAllocBytes,
			&d.TotalFiles, &unknown); err != nil {
			return nil, err
		}
		d.FRN = uint64(dfrn)
		d.ParentFRN = uint64(parent)
		d.Depth = int(depth)
		d.Unknown = unknown != 0
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w (snapshot #%d frn %d)", ErrDirNotFound, snapshotID, frn)
	}
	return out, nil
}

// dirPath joins a root-anchored breadcrumb into a display path. The first
// crumb's name is already the full scan root (e.g. `D:\`).
func dirPath(crumb []DirNode) string {
	if len(crumb) == 0 {
		return ""
	}
	parts := make([]string, len(crumb))
	for i, d := range crumb {
		parts[i] = strings.TrimRight(d.Name, `\/`)
	}
	path := strings.Join(parts, `\`)
	if len(crumb) == 1 && model.IsVolumeRoot(crumb[0].Name) {
		return path + `\`
	}
	return path
}

// ListSnapshots returns the newest snapshots of any status.
func (s *Store) ListSnapshots(ctx context.Context, limit int) ([]SnapshotInfo, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		snapshotSelectSQL+` ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
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

// VolumeRootSnapshots returns the newest complete volume-root snapshots of a
// volume in chronological order, for trend charts. Subdirectory scans and
// non-complete snapshots are skipped.
func (s *Store) VolumeRootSnapshots(ctx context.Context, volumeID int64, limit int) ([]SnapshotInfo, error) {
	if limit <= 0 {
		limit = 8
	}
	// Subdirectory scans share the volume id, so scan a wider window and filter.
	scanLimit := limit * 16
	if scanLimit < 64 {
		scanLimit = 64
	}
	rows, err := s.db.QueryContext(ctx,
		snapshotSelectSQL+` WHERE volume_id = ? AND status = 'complete'
		ORDER BY started_at DESC, id DESC LIMIT ?`, volumeID, scanLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotInfo
	for rows.Next() && len(out) < limit {
		sn, err := scanSnapshotInfo(rows)
		if err != nil {
			return nil, err
		}
		if model.IsVolumeRoot(sn.Root) {
			out = append(out, sn)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
