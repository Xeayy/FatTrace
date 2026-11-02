// Package daily turns volume-root snapshots into per-day summaries, ranked
// growing directories and retained top file events. It only reads snapshots and
// writes FatTrace's own summary tables; scanned files are never touched.
package daily

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"fattrace/internal/diff"
	"fattrace/internal/model"
	"fattrace/internal/rules"
	"fattrace/internal/store"
)

// Options controls which days are recomputed.
type Options struct {
	// Days is the size of the recent window ending today. 0 recomputes every
	// day that has snapshots.
	Days int
	// TopN caps ranked directories and retained events per kind.
	TopN int
	// Now is the reference time; tests set it explicitly.
	Now time.Time
	// Progress, if set, is called before each day diff.
	Progress func(day string, from, to int64)
}

// DayResult is one computed volume-day.
type DayResult struct {
	Summary    model.DailySummary  `json:"summary"`
	TopDirs    []model.DailyTopDir `json:"top_dirs,omitempty"`
	FromID     int64               `json:"from_snapshot_id"`
	ToID       int64               `json:"to_snapshot_id"`
	SingleShot bool                `json:"single_snapshot,omitempty"`
	Events     []model.FileEvent   `json:"-"`
}

// Report lists computed days newest first plus non-fatal warnings.
type Report struct {
	Days     []DayResult `json:"days"`
	Warnings []string    `json:"warnings,omitempty"`
}

// Compute fills daily_summary, daily_top_dirs and file_events.
//
// For a day with earlier snapshots, the baseline is the last snapshot of the
// previous day that has data; otherwise it is the first snapshot of the same
// day (intraday delta). A day with a single snapshot records zero delta.
func Compute(ctx context.Context, st *store.Store, set *rules.Set, opts Options) (Report, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.Days < 0 {
		opts.Days = 0
	}
	if opts.TopN <= 0 {
		opts.TopN = 10
	}
	if set == nil {
		set = rules.MustBuiltin()
	}
	snaps, err := st.CompleteVolumeRootSnapshots(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("load snapshots: %w", err)
	}
	groups := groupSnapshots(snaps)
	cutoff := ""
	if opts.Days > 0 {
		cutoff = opts.Now.AddDate(0, 0, -(opts.Days - 1)).Format("2006-01-02")
	}

	rep := Report{}
	for _, group := range groups {
		days := daysOf(group)
		ordered := make([]string, 0, len(days))
		for day := range days {
			ordered = append(ordered, day)
		}
		sort.Strings(ordered)
		var prev store.SnapshotInfo
		hasPrev := false
		for _, day := range ordered {
			first, last := days[day].first, days[day].last
			if cutoff != "" && day < cutoff {
				prev, hasPrev = last, true
				continue
			}
			from := first
			if hasPrev {
				from = prev
			}
			prev, hasPrev = last, true
			if opts.Progress != nil {
				opts.Progress(day, from.ID, last.ID)
			}
			res, err := computeDay(ctx, st, set, group.root, day, from, last, opts.TopN)
			if err != nil {
				rep.Warnings = append(rep.Warnings,
					fmt.Sprintf("%s %s: %v", group.root, day, err))
				continue
			}
			if err := st.SaveDailySummary(ctx, res.Summary, res.TopDirs); err != nil {
				return rep, fmt.Errorf("save %s %s: %w", group.root, day, err)
			}
			// Always replace, including single-snapshot days: a previous run may
			// have stored events for this snapshot before its older pair was
			// pruned, and those events no longer describe a daily delta.
			if err := st.ReplaceFileEvents(ctx, res.ToID, res.Events); err != nil {
				return rep, fmt.Errorf("save events %s %s: %w", group.root, day, err)
			}
			rep.Days = append(rep.Days, res)
		}
	}
	sort.Slice(rep.Days, func(i, j int) bool {
		if rep.Days[i].Summary.Day != rep.Days[j].Summary.Day {
			return rep.Days[i].Summary.Day > rep.Days[j].Summary.Day
		}
		return rep.Days[i].Summary.VolumeID < rep.Days[j].Summary.VolumeID
	})
	return rep, nil
}

type snapshotGroup struct {
	volumeID int64
	root     string
	snaps    []store.SnapshotInfo
}

type daySnaps struct {
	first, last store.SnapshotInfo
}

func groupSnapshots(snaps []store.SnapshotInfo) []snapshotGroup {
	var groups []snapshotGroup
	index := map[string]int{}
	for _, sn := range snaps {
		key := fmt.Sprintf("%d|%s", sn.VolumeID, strings.ToLower(sn.Root))
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, snapshotGroup{volumeID: sn.VolumeID, root: sn.Root})
		}
		groups[i].snaps = append(groups[i].snaps, sn)
	}
	return groups
}

func daysOf(group snapshotGroup) map[string]daySnaps {
	out := make(map[string]daySnaps, len(group.snaps))
	for _, sn := range group.snaps {
		day := time.UnixMilli(sn.StartedAt).Local().Format("2006-01-02")
		d := out[day]
		if d.first.ID == 0 {
			d.first = sn
		}
		d.last = sn
		out[day] = d
	}
	return out
}

func computeDay(ctx context.Context, st *store.Store, set *rules.Set, root, day string,
	from, to store.SnapshotInfo, topN int) (DayResult, error) {

	res := DayResult{FromID: from.ID, ToID: to.ID}
	sum := model.DailySummary{
		VolumeID:       to.VolumeID,
		Root:           root,
		Day:            day,
		SnapshotID:     to.ID,
		FromSnapshotID: from.ID,
		UsedBytes:      to.VolumeUsed,
		Files:          to.Files,
		Errors:         to.Errors,
		CoveragePct:    to.CoveragePct,
	}
	if sum.UsedBytes <= 0 {
		sum.UsedBytes = to.UniqueAllocBytes
	}
	if from.ID == to.ID {
		res.SingleShot = true
		res.Summary = sum
		return res, nil
	}

	d, err := rules.Diff(ctx, st, diff.Options{
		FromID: from.ID, ToID: to.ID, TopN: topN,
	}, set)
	if err != nil {
		return DayResult{}, err
	}
	sum.DeltaBytes = d.Totals.DeltaAlloc
	if d.Explanation != nil {
		sum.GrowthBytes = d.Explanation.GrowthBytes
		sum.ExplainedBytes = d.Explanation.ExplainedBytes
		sum.ExplainedPct = d.Explanation.ExplainedPct
	}
	res.Summary = sum
	res.TopDirs = topDirs(d.TopDirs, to.VolumeID, day, root, topN)
	res.Events = buildEvents(d, to, topN)
	return res, nil
}

func topDirs(changes []model.DirChange, volumeID int64, day, root string, limit int) []model.DailyTopDir {
	sorted := append([]model.DirChange(nil), changes...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].DeltaAlloc > sorted[j].DeltaAlloc
	})
	out := make([]model.DailyTopDir, 0, limit)
	for _, c := range sorted {
		if len(out) >= limit || c.DeltaAlloc <= 0 {
			break
		}
		path := c.Path
		if path == "" {
			path = root
		}
		out = append(out, model.DailyTopDir{
			VolumeID: volumeID, Day: day, Rank: len(out) + 1,
			Path: path, DeltaBytes: c.DeltaAlloc,
		})
	}
	return out
}

// buildEvents keeps the same ranked file lists the report shows. The schema
// only allows the coarse kinds below; a rename is stored as "moved".
func buildEvents(d model.DiffResult, to store.SnapshotInfo, limit int) []model.FileEvent {
	var out []model.FileEvent
	add := func(list []model.FileChange) {
		for i, c := range list {
			if i >= limit {
				break
			}
			kind := ""
			switch c.Kind {
			case model.ChangeAdded:
				kind = "new"
			case model.ChangeGrown:
				kind = "grew"
			case model.ChangeDeleted:
				kind = "deleted"
			case model.ChangeMoved, model.ChangeRenamed:
				kind = "moved"
			default:
				continue
			}
			out = append(out, model.FileEvent{
				VolumeID: to.VolumeID, SnapshotID: to.ID, Kind: kind, FRN: c.FRN,
				Path: c.Path, OldPath: c.OldPath,
				SizeBefore: c.SizeBefore, SizeAfter: c.SizeAfter,
				DeltaBytes: c.DeltaAlloc, DetectedAt: to.StartedAt,
			})
		}
	}
	add(d.TopGrowth)
	add(d.TopDeleted)
	add(d.Moves)
	return out
}
