package diff

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/Xeayy/FatTrace/internal/model"
)

type candidateKey struct {
	name    string
	size    int64
	mtimeMS int64
}

// Input is the pure-data contract for a snapshot diff. The store loader fills it;
// golden tests construct it directly without touching the file system.
// FromFileIndex is an optional prebuilt index for the from side: streaming
// callers keep only that side in memory and feed the to side through
// StreamSnapshotFiles. The index is consumed by the diff (matched entries are
// deleted, leaving the deletions), so it must not be shared afterwards.
type Input struct {
	FromFiles     []model.FileRecord
	FromFileIndex map[uint64]model.FileRecord
	ToFiles       []model.FileRecord
	FromDirs      []model.SnapshotDir
	ToDirs        []model.SnapshotDir
	Candidates    []model.FileCandidate
	TopN          int
	// OnFileChange, if set, receives every file change with its display path
	// resolved, before top-N truncation.
	OnFileChange func(model.FileChange)
}

const defaultTopN = 20
const maxAncestorDepth = 128

// Compute aligns two snapshots by FRN, classifies every change and returns the
// ranked result. Directory growth is move-adjusted: moving a subtree changes
// ownership, not size, so ancestors outside the moved subtree stay at zero.
func Compute(in Input) model.DiffResult {
	fromFiles := in.FromFileIndex
	if fromFiles == nil {
		fromFiles = make(map[uint64]model.FileRecord, len(in.FromFiles))
		for _, f := range in.FromFiles {
			fromFiles[f.FRN] = f
		}
	}
	c := newComputer(fromFiles, in.Candidates)
	for _, f := range in.ToFiles {
		c.addFile(f)
	}
	return c.finish(in)
}

// computer accumulates file changes one row at a time so the to-side snapshot
// never has to be materialized as a slice.
type computer struct {
	res        model.DiffResult
	cand       map[candidateKey]uint32
	fromFiles  map[uint64]model.FileRecord
	changes    []model.FileChange
	movedFiles []model.FileChange
}

func newComputer(fromFiles map[uint64]model.FileRecord, candidates []model.FileCandidate) *computer {
	cand := make(map[candidateKey]uint32, len(candidates))
	for _, c := range candidates {
		if c.MTimeMS == 0 {
			continue
		}
		cand[candidateKey{c.Name, c.Size, c.MTimeMS}] = c.VolumeSerial
	}
	return &computer{cand: cand, fromFiles: fromFiles}
}

// addFile folds one to-side row into the accumulator. It matches the
// StreamSnapshotFiles callback signature and never fails.
func (c *computer) addFile(f model.FileRecord) error {
	old, matched := c.fromFiles[f.FRN]
	if matched {
		delete(c.fromFiles, f.FRN)
		ch := model.FileChange{
			FRN:           f.FRN,
			ParentFRN:     f.ParentFRN,
			OldParentFRN:  old.ParentFRN,
			Name:          f.Name,
			OldName:       old.Name,
			SizeBefore:    old.Size,
			SizeAfter:     f.Size,
			AllocBefore:   old.AllocSize,
			AllocAfter:    f.AllocSize,
			DeltaBytes:    f.Size - old.Size,
			DeltaAlloc:    f.AllocSize - old.AllocSize,
			MTimeBeforeMS: mtimeMS(old.MTime),
			MTimeAfterMS:  mtimeMS(f.MTime),
		}
		switch {
		case ch.DeltaAlloc > 0 || (ch.DeltaAlloc == 0 && ch.DeltaBytes > 0):
			ch.Kind = model.ChangeGrown
			c.res.Totals.FilesGrown++
			c.res.Totals.BytesGrown += ch.DeltaBytes
			c.res.Totals.AllocGrown += ch.DeltaAlloc
		case ch.DeltaAlloc < 0 || (ch.DeltaAlloc == 0 && ch.DeltaBytes < 0):
			ch.Kind = model.ChangeShrunk
			c.res.Totals.FilesShrunk++
			c.res.Totals.BytesShrunk += ch.DeltaBytes
			c.res.Totals.AllocShrunk += ch.DeltaAlloc
		case f.ParentFRN != old.ParentFRN:
			ch.Kind = model.ChangeMoved
			c.res.Totals.FilesMoved++
		case f.Name != old.Name:
			ch.Kind = model.ChangeRenamed
			c.res.Totals.FilesRenamed++
		default:
			return nil
		}
		if f.ParentFRN != old.ParentFRN {
			c.movedFiles = append(c.movedFiles, ch)
		}
		c.changes = append(c.changes, ch)
		return nil
	}

	ch := model.FileChange{
		FRN:          f.FRN,
		Kind:         model.ChangeAdded,
		ParentFRN:    f.ParentFRN,
		Name:         f.Name,
		SizeAfter:    f.Size,
		AllocAfter:   f.AllocSize,
		DeltaBytes:   f.Size,
		DeltaAlloc:   f.AllocSize,
		MTimeAfterMS: mtimeMS(f.MTime),
	}
	if ch.MTimeAfterMS != 0 {
		if serial, hit := c.cand[candidateKey{f.Name, f.Size, ch.MTimeAfterMS}]; hit {
			ch.SuspectedFromVolume = serial
		}
	}
	c.res.Totals.FilesAdded++
	c.res.Totals.BytesAdded += f.Size
	c.res.Totals.AllocAdded += f.AllocSize
	c.changes = append(c.changes, ch)
	return nil
}

// finish turns the leftovers of the from index into deletions, then computes
// totals, directory ownership and the ranked top lists.
func (c *computer) finish(in Input) model.DiffResult {
	topN := in.TopN
	if topN <= 0 {
		topN = defaultTopN
	}

	for _, old := range c.fromFiles {
		ch := model.FileChange{
			FRN:           old.FRN,
			Kind:          model.ChangeDeleted,
			ParentFRN:     old.ParentFRN,
			Name:          old.Name,
			SizeBefore:    old.Size,
			AllocBefore:   old.AllocSize,
			DeltaBytes:    -old.Size,
			DeltaAlloc:    -old.AllocSize,
			MTimeBeforeMS: mtimeMS(old.MTime),
		}
		c.res.Totals.FilesDeleted++
		c.res.Totals.BytesDeleted -= old.Size
		c.res.Totals.AllocDeleted -= old.AllocSize
		c.changes = append(c.changes, ch)
	}
	c.res.Totals.DeltaBytes = c.res.Totals.BytesAdded + c.res.Totals.BytesDeleted +
		c.res.Totals.BytesGrown + c.res.Totals.BytesShrunk
	c.res.Totals.DeltaAlloc = c.res.Totals.AllocAdded + c.res.Totals.AllocDeleted +
		c.res.Totals.AllocGrown + c.res.Totals.AllocShrunk

	dirChanges, newlyUnknown := diffDirs(in, c.movedFiles, &c.res)
	rootDelta, hasRoot := rootDeltaAlloc(in)
	if hasRoot {
		c.res.Check.RootDeltaAlloc = rootDelta
		if rootDelta != c.res.Totals.DeltaAlloc {
			c.res.Warnings = append(c.res.Warnings, fmt.Sprintf(
				"conservation check failed: file delta %d bytes != root alloc delta %d bytes",
				c.res.Totals.DeltaAlloc, rootDelta))
		}
	}
	if newlyUnknown > 0 {
		c.res.Warnings = append(c.res.Warnings, fmt.Sprintf(
			"%d director%s flagged unknown after this snapshot; growth under them may be underreported",
			newlyUnknown, pluralY(newlyUnknown)))
	}

	fromDirs := indexDirs(in.FromDirs)
	toDirs := indexDirs(in.ToDirs)

	if in.OnFileChange != nil {
		fillFilePaths(c.changes, fromDirs, toDirs)
		for _, ch := range c.changes {
			in.OnFileChange(ch)
		}
	}

	c.res.TopGrowth = rankGrowth(c.changes, topN)
	c.res.TopDeleted = rankDeleted(c.changes, topN)
	c.res.Moves = rankMoves(c.changes, topN)
	fillFilePaths(c.res.TopGrowth, fromDirs, toDirs)
	fillFilePaths(c.res.TopDeleted, fromDirs, toDirs)
	fillFilePaths(c.res.Moves, fromDirs, toDirs)

	c.res.TopDirs, c.res.ShrunkDirs, c.res.DirMoves = rankDirs(dirChanges, topN)
	fillDirPaths(c.res.TopDirs, fromDirs, toDirs)
	fillDirPaths(c.res.ShrunkDirs, fromDirs, toDirs)
	fillDirPaths(c.res.DirMoves, fromDirs, toDirs)
	return c.res
}

// diffDirs classifies directories and computes move-adjusted intrinsic deltas.
// The returned map holds every identity-level change plus unchanged ancestors
// whose adjusted delta is non-zero (for example the old and new parents of a
// moved subtree).
func diffDirs(in Input, movedFiles []model.FileChange, res *model.DiffResult) (map[uint64]*model.DirChange, int64) {
	fromAll := indexDirs(in.FromDirs)
	to := indexDirs(in.ToDirs)
	from := make(map[uint64]model.SnapshotDir, len(fromAll))
	for frn, d := range fromAll {
		from[frn] = d
	}

	changes := make(map[uint64]*model.DirChange, len(in.ToDirs)+len(in.FromDirs))
	adj := make(map[uint64]int64, len(in.ToDirs)+len(in.FromDirs))
	adjBytes := make(map[uint64]int64, len(in.ToDirs)+len(in.FromDirs))
	newlyUnknownSet := make(map[uint64]bool)

	for _, d := range in.ToDirs {
		old, matched := from[d.FRN]
		if matched {
			delete(from, d.FRN)
		}
		if !matched {
			dc := &model.DirChange{
				FRN: d.FRN, Kind: model.ChangeAdded, ParentFRN: d.ParentFRN, Name: d.Name, Depth: d.Depth,
				TotalBytesAfter: d.TotalBytes, TotalAllocAfter: d.TotalAllocBytes, FilesAfter: d.TotalFiles,
				DeltaBytes: d.TotalBytes, DeltaAlloc: d.TotalAllocBytes, UnknownAfter: d.Unknown,
			}
			if d.Unknown {
				newlyUnknownSet[d.FRN] = true
			}
			res.Totals.DirsAdded++
			changes[d.FRN] = dc
			adj[d.FRN] += d.TotalAllocBytes
			adjBytes[d.FRN] += d.TotalBytes
			continue
		}
		dc := &model.DirChange{
			FRN: d.FRN, ParentFRN: d.ParentFRN, OldParentFRN: old.ParentFRN,
			Name: d.Name, OldName: old.Name, Depth: d.Depth,
			TotalBytesBefore: old.TotalBytes, TotalBytesAfter: d.TotalBytes,
			TotalAllocBefore: old.TotalAllocBytes, TotalAllocAfter: d.TotalAllocBytes,
			DeltaBytes:  d.TotalBytes - old.TotalBytes,
			DeltaAlloc:  d.TotalAllocBytes - old.TotalAllocBytes,
			FilesBefore: old.TotalFiles, FilesAfter: d.TotalFiles,
			UnknownBefore: old.Unknown, UnknownAfter: d.Unknown,
			NewlyUnknown: d.Unknown && !old.Unknown,
		}
		if dc.NewlyUnknown {
			newlyUnknownSet[d.FRN] = true
		}
		switch {
		case d.ParentFRN != old.ParentFRN:
			dc.Kind = model.ChangeMoved
			res.Totals.DirsMoved++
		case d.Name != old.Name:
			dc.Kind = model.ChangeRenamed
			res.Totals.DirsRenamed++
		case dc.DeltaAlloc > 0 || (dc.DeltaAlloc == 0 && dc.DeltaBytes > 0):
			dc.Kind = model.ChangeGrown
		case dc.DeltaAlloc < 0 || (dc.DeltaAlloc == 0 && dc.DeltaBytes < 0):
			dc.Kind = model.ChangeShrunk
		}
		changes[d.FRN] = dc
		adj[d.FRN] += dc.DeltaAlloc
		adjBytes[d.FRN] += dc.DeltaBytes
	}
	for _, old := range from {
		dc := &model.DirChange{
			FRN: old.FRN, Kind: model.ChangeDeleted, ParentFRN: old.ParentFRN, Name: old.Name, Depth: old.Depth,
			TotalBytesBefore: old.TotalBytes, TotalAllocBefore: old.TotalAllocBytes, FilesBefore: old.TotalFiles,
			DeltaBytes: -old.TotalBytes, DeltaAlloc: -old.TotalAllocBytes, UnknownBefore: old.Unknown,
		}
		res.Totals.DirsDeleted++
		changes[old.FRN] = dc
		adj[old.FRN] -= old.TotalAllocBytes
		adjBytes[old.FRN] -= old.TotalBytes
	}

	// Remove ownership artifacts from every ancestor that contains only one of
	// the old/new parents. The lowest common ancestor and above stay untouched.
	adjustMove := func(oldParent, newParent uint64, oldAlloc, oldBytes, newAlloc, newBytes int64) {
		oldChain := ancestorChain(fromAll, oldParent)
		newChain := ancestorChain(to, newParent)
		inOld := make(map[uint64]bool, len(oldChain))
		for _, frn := range oldChain {
			inOld[frn] = true
		}
		inNew := make(map[uint64]bool, len(newChain))
		for _, frn := range newChain {
			inNew[frn] = true
		}
		for _, frn := range oldChain {
			if inNew[frn] {
				break
			}
			adj[frn] += oldAlloc
			adjBytes[frn] += oldBytes
		}
		for _, frn := range newChain {
			if inOld[frn] {
				break
			}
			adj[frn] -= newAlloc
			adjBytes[frn] -= newBytes
		}
	}
	for _, dc := range changes {
		if dc.Kind == model.ChangeMoved {
			adjustMove(dc.OldParentFRN, dc.ParentFRN,
				dc.TotalAllocBefore, dc.TotalBytesBefore, dc.TotalAllocAfter, dc.TotalBytesAfter)
		}
	}
	// A moved file shifts ownership equal to its pre-move size; any growth it
	// gained in between stays attributed to the destination directory.
	for _, fc := range movedFiles {
		adjustMove(fc.OldParentFRN, fc.ParentFRN,
			fc.AllocBefore, fc.SizeBefore, fc.AllocBefore, fc.SizeBefore)
	}

	out := make(map[uint64]*model.DirChange, len(changes))
	for frn, dc := range changes {
		c := *dc
		c.DeltaAlloc = adj[frn]
		c.DeltaBytes = adjBytes[frn]
		switch c.Kind {
		case model.ChangeGrown, model.ChangeShrunk, "":
			// A subtree moved in or out can cancel the owner's apparent growth,
			// or turn an unchanged owner into a real (intrinsic) mover.
			if c.DeltaAlloc == 0 && c.DeltaBytes == 0 {
				continue
			}
			if c.DeltaAlloc < 0 || (c.DeltaAlloc == 0 && c.DeltaBytes < 0) {
				c.Kind = model.ChangeShrunk
			} else {
				c.Kind = model.ChangeGrown
			}
		}
		out[frn] = &c
	}

	// Materialize unchanged ancestors that gained or lost only by a subtree move.
	for frn, delta := range adj {
		if _, ok := changes[frn]; ok {
			continue
		}
		if delta == 0 && adjBytes[frn] == 0 {
			continue
		}
		oldD, hasOld := fromAll[frn]
		newD, hasNew := to[frn]
		if !hasOld && !hasNew {
			continue
		}
		info := newD
		if !hasNew {
			info = oldD
		}
		dc := &model.DirChange{
			FRN: frn, ParentFRN: info.ParentFRN, Name: info.Name, Depth: info.Depth,
			TotalBytesBefore: oldD.TotalBytes, TotalBytesAfter: newD.TotalBytes,
			TotalAllocBefore: oldD.TotalAllocBytes, TotalAllocAfter: newD.TotalAllocBytes,
			FilesBefore: oldD.TotalFiles, FilesAfter: newD.TotalFiles,
			DeltaAlloc: delta, DeltaBytes: adjBytes[frn],
			UnknownBefore: oldD.Unknown, UnknownAfter: newD.Unknown,
		}
		if hasOld && hasNew {
			dc.OldParentFRN = oldD.ParentFRN
			dc.OldName = oldD.Name
		}
		if delta < 0 || (delta == 0 && adjBytes[frn] < 0) {
			dc.Kind = model.ChangeShrunk
		} else {
			dc.Kind = model.ChangeGrown
		}
		out[frn] = dc
	}

	// unknown propagates to ancestors; count only the leaf-most newly-unknown
	// directories so the warning names the number of actual read failures.
	withUnknownChild := make(map[uint64]bool, len(newlyUnknownSet))
	for frn := range newlyUnknownSet {
		if d, ok := to[frn]; ok && newlyUnknownSet[d.ParentFRN] {
			withUnknownChild[d.ParentFRN] = true
		}
	}
	var newlyUnknown int64
	for frn := range newlyUnknownSet {
		if !withUnknownChild[frn] {
			newlyUnknown++
		}
	}
	return out, newlyUnknown
}

func indexDirs(dirs []model.SnapshotDir) map[uint64]model.SnapshotDir {
	m := make(map[uint64]model.SnapshotDir, len(dirs))
	for _, d := range dirs {
		m[d.FRN] = d
	}
	return m
}

// ancestorChain lists start and its ancestors, nearest first, stopping at the
// root, a missing FRN or the depth guard.
func ancestorChain(dirs map[uint64]model.SnapshotDir, start uint64) []uint64 {
	var chain []uint64
	for i := 0; i < maxAncestorDepth && start != 0; i++ {
		d, ok := dirs[start]
		if !ok {
			break
		}
		chain = append(chain, start)
		start = d.ParentFRN
	}
	return chain
}

func rootDeltaAlloc(in Input) (int64, bool) {
	var before, after int64
	found := false
	for _, d := range in.FromDirs {
		if d.ParentFRN == 0 {
			before = d.TotalAllocBytes
			found = true
		}
	}
	for _, d := range in.ToDirs {
		if d.ParentFRN == 0 {
			after = d.TotalAllocBytes
			found = true
		}
	}
	return after - before, found
}

func mtimeMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func pluralY(n int64) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func fillFilePaths(changes []model.FileChange, fromDirs, toDirs map[uint64]model.SnapshotDir) {
	for i := range changes {
		c := &changes[i]
		if c.Kind == model.ChangeDeleted {
			c.Path = buildPath(fromDirs, c.ParentFRN, c.Name)
			continue
		}
		c.Path = buildPath(toDirs, c.ParentFRN, c.Name)
		if c.OldName != "" && (c.ParentFRN != c.OldParentFRN || c.Name != c.OldName) {
			c.OldPath = buildPath(fromDirs, c.OldParentFRN, c.OldName)
		}
	}
}

func fillDirPaths(dirs []model.DirChange, fromDirs, toDirs map[uint64]model.SnapshotDir) {
	for i := range dirs {
		d := &dirs[i]
		if d.Kind == model.ChangeDeleted {
			d.Path = buildPath(fromDirs, d.ParentFRN, d.Name)
			continue
		}
		d.Path = buildPath(toDirs, d.ParentFRN, d.Name)
		if d.OldName != "" && (d.ParentFRN != d.OldParentFRN || d.Name != d.OldName) {
			d.OldPath = buildPath(fromDirs, d.OldParentFRN, d.OldName)
		}
	}
}

func rankGrowth(changes []model.FileChange, n int) []model.FileChange {
	var out []model.FileChange
	for _, c := range changes {
		if c.Kind == model.ChangeAdded || c.Kind == model.ChangeGrown {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DeltaAlloc != out[j].DeltaAlloc {
			return out[i].DeltaAlloc > out[j].DeltaAlloc
		}
		if out[i].DeltaBytes != out[j].DeltaBytes {
			return out[i].DeltaBytes > out[j].DeltaBytes
		}
		return out[i].FRN < out[j].FRN
	})
	return truncateFiles(out, n)
}

func rankDeleted(changes []model.FileChange, n int) []model.FileChange {
	var out []model.FileChange
	for _, c := range changes {
		if c.Kind == model.ChangeDeleted {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AllocBefore != out[j].AllocBefore {
			return out[i].AllocBefore > out[j].AllocBefore
		}
		return out[i].FRN < out[j].FRN
	})
	return truncateFiles(out, n)
}

func rankMoves(changes []model.FileChange, n int) []model.FileChange {
	var out []model.FileChange
	for _, c := range changes {
		if c.Kind == model.ChangeMoved || c.Kind == model.ChangeRenamed {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AllocAfter != out[j].AllocAfter {
			return out[i].AllocAfter > out[j].AllocAfter
		}
		return out[i].FRN < out[j].FRN
	})
	return truncateFiles(out, n)
}

func truncateFiles(in []model.FileChange, n int) []model.FileChange {
	if len(in) > n {
		return in[:n]
	}
	return in
}

func rankDirs(totals map[uint64]*model.DirChange, n int) ([]model.DirChange, []model.DirChange, []model.DirChange) {
	children := make(map[uint64][]model.DirChange, len(totals))
	for _, d := range totals {
		children[d.ParentFRN] = append(children[d.ParentFRN], *d)
	}
	// An ancestor whose whole delta arrived through one child adds no detail;
	// keep the deepest directory that owns the change.
	inherited := func(d model.DirChange) bool {
		for _, c := range children[d.FRN] {
			if c.DeltaAlloc == d.DeltaAlloc && c.DeltaBytes == d.DeltaBytes {
				return true
			}
		}
		return false
	}

	var growth, shrunk, moves []model.DirChange
	for _, d := range totals {
		switch d.Kind {
		case model.ChangeMoved, model.ChangeRenamed:
			moves = append(moves, *d)
		}
		if d.DeltaAlloc > 0 || (d.DeltaAlloc == 0 && d.DeltaBytes > 0) {
			if !inherited(*d) {
				growth = append(growth, *d)
			}
		} else if d.DeltaAlloc < 0 || (d.DeltaAlloc == 0 && d.DeltaBytes < 0) {
			if !inherited(*d) {
				shrunk = append(shrunk, *d)
			}
		}
	}
	sort.Slice(growth, func(i, j int) bool {
		if growth[i].DeltaAlloc != growth[j].DeltaAlloc {
			return growth[i].DeltaAlloc > growth[j].DeltaAlloc
		}
		return growth[i].FRN < growth[j].FRN
	})
	sort.Slice(shrunk, func(i, j int) bool {
		if shrunk[i].DeltaAlloc != shrunk[j].DeltaAlloc {
			return shrunk[i].DeltaAlloc < shrunk[j].DeltaAlloc
		}
		return shrunk[i].FRN < shrunk[j].FRN
	})
	sort.Slice(moves, func(i, j int) bool {
		if moves[i].TotalAllocAfter != moves[j].TotalAllocAfter {
			return moves[i].TotalAllocAfter > moves[j].TotalAllocAfter
		}
		return moves[i].FRN < moves[j].FRN
	})
	if len(growth) > n {
		growth = growth[:n]
	}
	if len(shrunk) > n {
		shrunk = shrunk[:n]
	}
	if len(moves) > n {
		moves = moves[:n]
	}
	return growth, shrunk, moves
}

// buildPath assembles a display path from the directory index. The scan root is
// stored with ParentFRN 0 and its absolute path as Name.
func buildPath(dirs map[uint64]model.SnapshotDir, parent uint64, name string) string {
	if parent == 0 {
		return name
	}
	d, ok := dirs[parent]
	if !ok {
		return name
	}
	parts := []string{name}
	for i := 0; i < maxAncestorDepth; i++ {
		parts = append(parts, d.Name)
		if d.ParentFRN == 0 {
			break
		}
		next, ok := dirs[d.ParentFRN]
		if !ok {
			break
		}
		d = next
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return filepath.Join(parts...)
}
