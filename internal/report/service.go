package report

import (
	"context"
	"fmt"
	"time"

	"github.com/Xeayy/FatTrace/internal/diff"
	"github.com/Xeayy/FatTrace/internal/rules"
	"github.com/Xeayy/FatTrace/internal/store"
)

// Options selects the snapshot pair and report size. Snapshot resolution,
// validation and the volume check are shared with `fat diff`.
type Options struct {
	FromID      int64
	ToID        int64
	RootPrefix  string
	TopN        int
	CrossVolume bool
	Week        bool
	ErrorLimit  int
}

// Run computes the diff, loads the errors that belong to the to-snapshot and
// renders the single-file HTML report.
func Run(ctx context.Context, st *store.Store, opts Options) ([]byte, error) {
	set, err := rules.Load(st.Path())
	if err != nil {
		return nil, err
	}
	res, err := rules.Diff(ctx, st, diff.Options{
		FromID: opts.FromID, ToID: opts.ToID, RootPrefix: opts.RootPrefix,
		TopN: opts.TopN, CrossVolume: opts.CrossVolume,
	}, set)
	if err != nil {
		return nil, err
	}
	if opts.Week {
		const dayMS = int64(24 * time.Hour / time.Millisecond)
		span := res.ToSnapshot.StartedAtMS - res.FromSnapshot.StartedAtMS
		if span < 5*dayMS || span > 9*dayMS {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"--week: snapshots are %.1f days apart", float64(span)/float64(dayMS)))
		}
	}

	limit := opts.ErrorLimit
	if limit <= 0 {
		limit = 20
	}
	rows, err := st.SnapshotErrors(ctx, res.ToSnapshot.ID, limit)
	if err != nil {
		return nil, fmt.Errorf("load snapshot #%d errors: %w", res.ToSnapshot.ID, err)
	}
	errs := make([]ErrorRow, len(rows))
	for i, e := range rows {
		errs[i] = ErrorRow{Path: e.Path, Op: e.Op, Msg: e.Msg, Hits: e.Hits}
	}
	return Build(res, errs, time.Now())
}
