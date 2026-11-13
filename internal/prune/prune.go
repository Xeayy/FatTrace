// Package prune applies the retention policy to FatTrace's own database
// history. Scanned files are never touched: only snapshots and aggregate rows
// recorded by earlier runs can be deleted.
package prune

import (
	"context"
	"fmt"
	"time"

	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/store"
)

// Snapshot is one candidate shown in the dry-run plan.
type Snapshot struct {
	ID               int64  `json:"id"`
	Kind             string `json:"kind"`
	Root             string `json:"root"`
	StartedAtMS      int64  `json:"started_at_ms"`
	Files            int64  `json:"files"`
	Dirs             int64  `json:"dirs"`
	UniqueAllocBytes int64  `json:"unique_alloc_bytes"`
}

// Report describes what a prune run found or deleted.
type Report struct {
	Applied          bool       `json:"applied"`
	DailyCutoff      string     `json:"daily_snapshot_cutoff,omitempty"`
	WeeklyCutoff     string     `json:"weekly_snapshot_cutoff,omitempty"`
	DailyFileCutoff  string     `json:"daily_file_cutoff,omitempty"`
	WeeklyFileCutoff string     `json:"weekly_file_cutoff,omitempty"`
	SummaryCutoff    string     `json:"daily_summary_cutoff,omitempty"`
	EventsCutoffMS   int64      `json:"file_events_cutoff_ms,omitempty"`
	Snapshots        []Snapshot `json:"snapshots"`
	FileSnapshotIDs  []int64    `json:"file_snapshot_ids,omitempty"`
	FileRows         int64      `json:"file_rows"`
	Summaries        int64      `json:"daily_summaries"`
	TopDirs          int64      `json:"daily_top_dirs"`
	FileEvents       int64      `json:"file_events"`
	Deleted          struct {
		Snapshots  int64 `json:"snapshots"`
		FileRows   int64 `json:"file_rows"`
		Summaries  int64 `json:"daily_summaries"`
		TopDirs    int64 `json:"daily_top_dirs"`
		FileEvents int64 `json:"file_events"`
	} `json:"deleted"`
}

// Run computes the retention plan and optionally applies it. Days values of 0
// disable that retention dimension; manual snapshots are never candidates.
func Run(ctx context.Context, st *store.Store, cfg config.Retention, apply bool, now time.Time) (Report, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rep := Report{Applied: apply, Snapshots: []Snapshot{}}
	dailyBefore := cutoff(now, cfg.DailySnapshotsDays)
	weeklyBefore := cutoff(now, cfg.WeeklySnapshotsDays)
	dailyFileBefore := cutoff(now, cfg.DailyFileDays)
	weeklyFileBefore := cutoff(now, cfg.WeeklyFileWeeks*7)
	if dailyBefore > 0 {
		rep.DailyCutoff = time.UnixMilli(dailyBefore).Local().Format("2006-01-02 15:04")
	}
	if weeklyBefore > 0 {
		rep.WeeklyCutoff = time.UnixMilli(weeklyBefore).Local().Format("2006-01-02 15:04")
	}
	if dailyFileBefore > 0 {
		rep.DailyFileCutoff = time.UnixMilli(dailyFileBefore).Local().Format("2006-01-02 15:04")
	}
	if weeklyFileBefore > 0 {
		rep.WeeklyFileCutoff = time.UnixMilli(weeklyFileBefore).Local().Format("2006-01-02 15:04")
	}

	cands, err := st.PruneSnapshotCandidates(ctx, dailyBefore, weeklyBefore)
	if err != nil {
		return rep, err
	}
	var ids []int64
	fullDelete := make(map[int64]bool, len(cands))
	for _, sn := range cands {
		ids = append(ids, sn.ID)
		fullDelete[sn.ID] = true
		rep.Snapshots = append(rep.Snapshots, Snapshot{
			ID: sn.ID, Kind: sn.Kind, Root: sn.Root, StartedAtMS: sn.StartedAt,
			Files: sn.Files, Dirs: sn.Dirs, UniqueAllocBytes: sn.UniqueAllocBytes,
		})
	}

	fileCands, err := st.SnapshotFilePruneCandidates(ctx, dailyFileBefore, weeklyFileBefore)
	if err != nil {
		return rep, err
	}
	var fileIDs []int64
	for _, sn := range fileCands {
		if fullDelete[sn.ID] {
			continue
		}
		fileIDs = append(fileIDs, sn.ID)
	}
	rep.FileSnapshotIDs = fileIDs
	if len(fileIDs) > 0 {
		rep.FileRows, err = st.CountSnapshotFiles(ctx, fileIDs)
		if err != nil {
			return rep, err
		}
	}

	summaryCutoff := ""
	if cfg.DailySummaryDays > 0 {
		summaryCutoff = now.AddDate(0, 0, -cfg.DailySummaryDays).Format("2006-01-02")
		rep.SummaryCutoff = summaryCutoff
	}
	eventsBefore := cutoff(now, cfg.FileEventsDays)
	rep.EventsCutoffMS = eventsBefore
	if summaryCutoff != "" || eventsBefore > 0 {
		rep.Summaries, rep.TopDirs, rep.FileEvents, err =
			st.CountDailyHistoryBefore(ctx, summaryCutoff, eventsBefore)
		if err != nil {
			return rep, err
		}
	}
	if !apply {
		return rep, nil
	}

	if len(fileIDs) > 0 {
		n, err := st.DeleteSnapshotFiles(ctx, fileIDs)
		if err != nil {
			return rep, fmt.Errorf("delete snapshot files: %w", err)
		}
		rep.Deleted.FileRows = n
	}
	if len(ids) > 0 {
		n, err := st.DeleteSnapshots(ctx, ids)
		if err != nil {
			return rep, fmt.Errorf("delete snapshots: %w", err)
		}
		rep.Deleted.Snapshots = n
	}
	if summaryCutoff != "" {
		s, d, err := st.DeleteDailyHistoryBefore(ctx, summaryCutoff)
		if err != nil {
			return rep, fmt.Errorf("delete daily history: %w", err)
		}
		rep.Deleted.Summaries, rep.Deleted.TopDirs = s, d
	}
	if eventsBefore > 0 {
		n, err := st.DeleteFileEventsBefore(ctx, eventsBefore)
		if err != nil {
			return rep, fmt.Errorf("delete file events: %w", err)
		}
		rep.Deleted.FileEvents = n
	}
	_ = st.Checkpoint(ctx)
	return rep, nil
}

func cutoff(now time.Time, days int) int64 {
	if days <= 0 {
		return 0
	}
	return now.AddDate(0, 0, -days).UnixMilli()
}
