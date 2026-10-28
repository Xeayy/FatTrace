package store

import (
	"context"
	"sort"
)

// DirTotal is one directory with its recursive totals. Depths start at 0 for the
// scan root; Unknown propagates from error directories up to their ancestors.
type DirTotal struct {
	FRN        uint64
	ParentFRN  uint64
	Name       string
	Attrs      uint32
	Depth      int
	Bytes      int64
	AllocBytes int64
	Files      int64
	Unknown    bool
}

// AggregateDirs computes recursive directory totals from the scan_files staging
// table. unknown marks directories whose own scan failed; the flag is propagated
// to every ancestor.
func (s *Store) AggregateDirs(ctx context.Context, volumeID int64, unknown map[uint64]bool) ([]DirTotal, error) {
	dirs := make(map[uint64]*DirTotal, 4096)
	rows, err := s.db.QueryContext(ctx,
		`SELECT frn, parent_frn, name, attrs FROM scan_files WHERE volume_id = ? AND is_dir = 1`, volumeID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var frn, parent, attrs int64
		var name string
		if err := rows.Scan(&frn, &parent, &name, &attrs); err != nil {
			rows.Close()
			return nil, err
		}
		dirs[uint64(frn)] = &DirTotal{
			FRN:       uint64(frn),
			ParentFRN: uint64(parent),
			Name:      name,
			Attrs:     uint32(attrs),
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	frows, err := s.db.QueryContext(ctx, `
		SELECT parent_frn, COALESCE(SUM(size), 0), COALESCE(SUM(alloc_size), 0), COUNT(*)
		FROM scan_files WHERE volume_id = ? AND is_dir = 0
		GROUP BY parent_frn`, volumeID)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var parent, bytes, alloc, files int64
		if err := frows.Scan(&parent, &bytes, &alloc, &files); err != nil {
			frows.Close()
			return nil, err
		}
		if d, ok := dirs[uint64(parent)]; ok {
			d.Bytes += bytes
			d.AllocBytes += alloc
			d.Files += files
		}
	}
	if err := frows.Err(); err != nil {
		frows.Close()
		return nil, err
	}
	frows.Close()

	for frn := range unknown {
		if d, ok := dirs[frn]; ok {
			d.Unknown = true
		}
	}

	state := make(map[uint64]uint8, len(dirs))
	var resolve func(uint64) int
	resolve = func(frn uint64) int {
		d := dirs[frn]
		if d == nil {
			return 0
		}
		switch state[frn] {
		case 2:
			return d.Depth
		case 1:
			return 0 // cycle guard
		}
		state[frn] = 1
		depth := 0
		if d.ParentFRN != 0 {
			if _, ok := dirs[d.ParentFRN]; ok {
				depth = resolve(d.ParentFRN) + 1
			}
		}
		d.Depth = depth
		state[frn] = 2
		return depth
	}

	ptrs := make([]*DirTotal, 0, len(dirs))
	for frn, d := range dirs {
		resolve(frn)
		ptrs = append(ptrs, d)
	}
	// Children first: each subtree rolls into its parent exactly once.
	sort.Slice(ptrs, func(i, j int) bool {
		if ptrs[i].Depth != ptrs[j].Depth {
			return ptrs[i].Depth > ptrs[j].Depth
		}
		return ptrs[i].FRN < ptrs[j].FRN
	})
	for _, d := range ptrs {
		if d.ParentFRN == 0 {
			continue
		}
		if p, ok := dirs[d.ParentFRN]; ok {
			p.Bytes += d.Bytes
			p.AllocBytes += d.AllocBytes
			p.Files += d.Files
			if d.Unknown {
				p.Unknown = true
			}
		}
	}

	out := make([]DirTotal, len(ptrs))
	for i, d := range ptrs {
		out[i] = *d
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FRN < out[j].FRN })
	return out, nil
}
