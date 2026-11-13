package snap

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Xeayy/FatTrace/internal/model"
	"github.com/Xeayy/FatTrace/internal/scan"
	"github.com/Xeayy/FatTrace/internal/store"
)

type Options struct {
	Root             string
	Kind             string // manual | weekly | daily
	MaxDepth         int
	NoDedup          bool
	Progress         func(files, dirs int64)
	ProgressInterval time.Duration
}

type Result struct {
	SnapshotID      int64           `json:"snapshot_id"`
	Root            string          `json:"root"`
	Volume          string          `json:"volume"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	BaselineUpdated bool            `json:"baseline_updated"`
	Files           int64           `json:"files"`
	Dirs            int64           `json:"dirs"`
	StagedRows      int64           `json:"staged_rows"`
	SnapshotDirs    int64           `json:"snapshot_dirs"`
	DurationMS      int64           `json:"duration_ms"`
	Stats           model.ScanStats `json:"stats"`
}

// Run performs one full snapshot: scan into staging, aggregate directory totals,
// then atomically publish snapshot rows and (for a complete volume-root scan)
// the new baseline.
func Run(ctx context.Context, st *store.Store, opts Options) (Result, error) {
	if opts.Kind == "" {
		opts.Kind = "manual"
	}
	meta, err := scan.VolumeMetadata(opts.Root)
	if err != nil {
		return Result{}, err
	}
	volID, err := st.EnsureVolume(ctx, meta.Serial, meta.FSType, meta.Label)
	if err != nil {
		return Result{}, err
	}
	snapID, err := st.BeginSnapshot(ctx, volID, opts.Kind, meta.Root)
	if err != nil {
		return Result{}, err
	}

	res := Result{SnapshotID: snapID, Root: meta.Root, Volume: meta.Volume, Kind: opts.Kind}
	start := time.Now()
	fail := func(stats model.ScanStats, cause error) (Result, error) {
		status := "failed"
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || ctx.Err() != nil {
			status = "partial"
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = st.ClearScan(cleanupCtx, volID)
		_ = st.FinishSnapshot(cleanupCtx, snapID, status, stats)
		res.Status = status
		res.Stats = stats
		res.DurationMS = time.Since(start).Milliseconds()
		return res, cause
	}

	writer, err := st.BeginScan(ctx, volID)
	if err != nil {
		return fail(model.ScanStats{}, err)
	}

	var countFiles, countDirs atomic.Int64
	emit := func(r model.FileRecord) error {
		if r.IsDir {
			countDirs.Add(1)
		} else {
			countFiles.Add(1)
		}
		return writer.Add(ctx, r)
	}

	unknown := make(map[uint64]bool)
	var scanErrs []model.ScanError
	stopProgress := startProgress(opts, &countFiles, &countDirs)
	stats, walkErr := scan.Walk(ctx, meta.Root, scan.Options{
		MaxDepth:         opts.MaxDepth,
		DedupeHardlinks:  !opts.NoDedup,
		IncludeDirs:      true,
		ErrorSampleLimit: 1000,
		OnError: func(e model.ScanError) {
			scanErrs = append(scanErrs, e)
			if e.FRN != 0 {
				unknown[e.FRN] = true
			}
		},
	}, emit)
	stopProgress()

	if walkErr != nil {
		writer.Rollback()
		return fail(stats, walkErr)
	}

	staged, err := writer.Commit(ctx)
	if err != nil {
		return fail(stats, err)
	}
	dirs, err := st.AggregateDirs(ctx, volID, unknown)
	if err != nil {
		return fail(stats, fmt.Errorf("aggregate directories: %w", err))
	}

	// Never publish a depth-limited scan as the volume baseline: it would look
	// like everything below the cut vanished.
	updateBaseline := meta.IsRoot && opts.MaxDepth == 0
	if err := st.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID:     snapID,
		VolumeID:       volID,
		Status:         "complete",
		Stats:          stats,
		Dirs:           dirs,
		Errors:         scanErrs,
		UpdateBaseline: updateBaseline,
	}); err != nil {
		return fail(stats, err)
	}
	_ = st.Checkpoint(ctx)

	res.Status = "complete"
	res.BaselineUpdated = updateBaseline
	res.Files = stats.Files
	res.Dirs = stats.Dirs
	res.StagedRows = staged
	res.SnapshotDirs = int64(len(dirs))
	res.DurationMS = time.Since(start).Milliseconds()
	res.Stats = stats
	return res, nil
}

func startProgress(opts Options, files, dirs *atomic.Int64) func() {
	if opts.Progress == nil {
		return func() {}
	}
	interval := opts.ProgressInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				opts.Progress(files.Load(), dirs.Load())
			}
		}
	}()
	return func() { close(done) }
}
