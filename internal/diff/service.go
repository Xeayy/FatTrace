package diff

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"

	"fattrace/internal/model"
	"fattrace/internal/store"
)

// Options selects the snapshot pair. Zero IDs resolve automatically: to is the
// latest complete volume-root snapshot, from is the previous one on the same
// volume and root.
type Options struct {
	FromID      int64
	ToID        int64
	RootPrefix  string
	TopN        int
	CrossVolume bool
	// OnFileChange, if set, receives every file change with its display path
	// resolved, before top-N truncation. It runs after the store stream ends and
	// must not query the store.
	OnFileChange func(model.FileChange)
}

// Run loads a snapshot pair, computes the diff and fills in snapshot metadata
// and the volume-level cross-check.
func Run(ctx context.Context, st *store.Store, opts Options) (model.DiffResult, error) {
	from, to, err := resolveSnapshots(ctx, st, opts)
	if err != nil {
		return model.DiffResult{}, err
	}
	if err := validatePair(from, to); err != nil {
		return model.DiffResult{}, err
	}

	fromFiles, err := st.LoadSnapshotFileIndex(ctx, from.ID)
	if err != nil {
		return model.DiffResult{}, fmt.Errorf("load snapshot #%d files: %w", from.ID, err)
	}
	fromDirs, err := st.LoadSnapshotDirs(ctx, from.ID)
	if err != nil {
		return model.DiffResult{}, fmt.Errorf("load snapshot #%d dirs: %w", from.ID, err)
	}
	toDirs, err := st.LoadSnapshotDirs(ctx, to.ID)
	if err != nil {
		return model.DiffResult{}, fmt.Errorf("load snapshot #%d dirs: %w", to.ID, err)
	}

	var cands []model.FileCandidate
	if opts.CrossVolume {
		cands, err = st.LoadCrossVolumeCandidates(ctx, to.VolumeID)
		if err != nil {
			return model.DiffResult{}, fmt.Errorf("load cross-volume candidates: %w", err)
		}
	}

	// The to side streams through the accumulator so only one file snapshot is
	// ever resident; the stream holds the store connection, so every other
	// query must complete first.
	c := newComputer(fromFiles, cands)
	if err := st.StreamSnapshotFiles(ctx, to.ID, c.addFile); err != nil {
		return model.DiffResult{}, fmt.Errorf("load snapshot #%d files: %w", to.ID, err)
	}
	res := c.finish(Input{
		FromDirs: fromDirs, ToDirs: toDirs, TopN: opts.TopN,
		OnFileChange: opts.OnFileChange,
	})
	res.VolumeID = to.VolumeID
	res.Root = to.Root
	res.FromSnapshot = snapshotRef(from)
	res.ToSnapshot = snapshotRef(to)
	applyVolumeCheck(&res, from, to, toDirs)
	appendCoverageWarnings(&res, from, to)
	return res, nil
}

func resolveSnapshots(ctx context.Context, st *store.Store, opts Options) (store.SnapshotInfo, store.SnapshotInfo, error) {
	var from, to store.SnapshotInfo
	var err error
	if opts.FromID > 0 {
		if from, err = st.GetSnapshot(ctx, opts.FromID); err != nil {
			return from, to, err
		}
	}
	if opts.ToID > 0 {
		if to, err = st.GetSnapshot(ctx, opts.ToID); err != nil {
			return from, to, err
		}
	}

	switch {
	case from.ID == 0 && to.ID == 0:
		list, err := st.RecentComplete(ctx, opts.RootPrefix, 50)
		if err != nil {
			return from, to, err
		}
		for _, sn := range list {
			if model.IsVolumeRoot(sn.Root) {
				to = sn
				break
			}
		}
		if to.ID == 0 {
			return from, to, errors.New("diff: no complete volume-root snapshot found; run fat snap first")
		}
		from, err = st.PreviousComplete(ctx, to.VolumeID, to.ID, to.Root)
		if err != nil {
			return from, to, err
		}
	case from.ID != 0 && to.ID == 0:
		to, err = st.NextComplete(ctx, from.VolumeID, from.ID, from.Root)
		if err != nil {
			return from, to, err
		}
	case from.ID == 0 && to.ID != 0:
		from, err = st.PreviousComplete(ctx, to.VolumeID, to.ID, to.Root)
		if err != nil {
			return from, to, err
		}
	}
	return from, to, nil
}

func validatePair(from, to store.SnapshotInfo) error {
	if from.Status != "complete" || to.Status != "complete" {
		return fmt.Errorf("diff: both snapshots must be complete (from #%d is %s, to #%d is %s)",
			from.ID, from.Status, to.ID, to.Status)
	}
	if from.VolumeID != to.VolumeID {
		return fmt.Errorf("diff: snapshots belong to different volumes (#%d volume %d, #%d volume %d)",
			from.ID, from.VolumeID, to.ID, to.VolumeID)
	}
	if !strings.EqualFold(filepath.Clean(from.Root), filepath.Clean(to.Root)) {
		return fmt.Errorf("diff: snapshots have different roots (%s and %s)", from.Root, to.Root)
	}
	if from.ID == to.ID {
		return errors.New("diff: from and to are the same snapshot")
	}
	if from.StartedAt > to.StartedAt {
		return fmt.Errorf("diff: from snapshot #%d is newer than to snapshot #%d", from.ID, to.ID)
	}
	return nil
}

func snapshotRef(sn store.SnapshotInfo) model.SnapshotRef {
	r := model.SnapshotRef{
		ID: sn.ID, Kind: sn.Kind, Status: sn.Status, StartedAtMS: sn.StartedAt,
		Files: sn.Files, Dirs: sn.Dirs, UniqueAllocBytes: sn.UniqueAllocBytes,
		Errors: sn.Errors, CoveragePct: sn.CoveragePct,
	}
	if sn.FinishedAt != nil {
		r.FinishedAtMS = *sn.FinishedAt
	}
	return r
}

// applyVolumeCheck compares the root directory delta with the volume-used delta
// recorded by the two scans. The tolerance base is floored at 1% of used space
// so ordinary metadata churn on a quiet week does not trip the check.
func applyVolumeCheck(res *model.DiffResult, from, to store.SnapshotInfo, toDirs []model.SnapshotDir) {
	for _, d := range toDirs {
		if d.Unknown {
			res.Check.UnknownDirs++
		}
	}
	if !model.IsVolumeRoot(to.Root) {
		res.Check.OK = true
		res.Check.Skipped = true
		res.Check.Message = "snapshot root is not a volume root"
		return
	}
	if from.VolumeUsed <= 0 || to.VolumeUsed <= 0 {
		res.Check.OK = true
		res.Check.Skipped = true
		res.Check.Message = "volume usage was not recorded"
		return
	}
	diskDelta := to.VolumeUsed - from.VolumeUsed
	res.Check.DiskDeltaAlloc = diskDelta
	denom := math.Max(math.Abs(float64(diskDelta)), float64(to.VolumeUsed)*0.01)
	pct := 0.0
	if denom > 0 {
		pct = math.Abs(float64(res.Check.RootDeltaAlloc-diskDelta)) / denom * 100
	}
	res.Check.DiffPct = math.Round(pct*100) / 100
	res.Check.OK = pct < 2.0
	if res.Check.OK {
		res.Check.Message = "root delta agrees with volume-used delta within tolerance"
	} else {
		res.Check.Message = "root delta differs from volume-used delta; deltas may miss protected or excluded data"
	}
}

func appendCoverageWarnings(res *model.DiffResult, from, to store.SnapshotInfo) {
	check := func(label string, sn store.SnapshotInfo) {
		if sn.CoveragePct > 0 && sn.CoveragePct < 95 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s snapshot #%d coverage is %.2f%% (<95%%); deltas can underreport",
				label, sn.ID, sn.CoveragePct))
		}
	}
	check("from", from)
	check("to", to)
}
