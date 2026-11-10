// Package health summarizes whether scheduled snapshots are succeeding and how
// fresh the newest complete snapshot is. It is read-only and shared by doctor,
// the local server and the tray launcher.
package health

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fattrace/internal/store"
)

// Level is the worst state of one root.
type Level string

const (
	LevelOK     Level = "ok"
	LevelWarn   Level = "warn"
	LevelDanger Level = "danger"

	// A running snapshot older than this is treated as interrupted. Normal
	// whole-volume scans finish in minutes.
	runningStaleAfter = 6 * time.Hour
)

// Snapshot is the health-relevant view of one snapshot row.
type Snapshot struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at_ms"`
	FinishedAt *int64 `json:"finished_at_ms,omitempty"`
	Files      int64  `json:"files"`
	Dirs       int64  `json:"dirs"`
	Errors     int64  `json:"errors"`
}

// Status is the health summary for one configured root.
type Status struct {
	Root                string    `json:"root"`
	Level               Level     `json:"level"`
	Summary             string    `json:"summary"`
	Reason              string    `json:"reason,omitempty"`
	LastAttempt         *Snapshot `json:"last_attempt,omitempty"`
	LastComplete        *Snapshot `json:"last_complete,omitempty"`
	LastFailure         *Snapshot `json:"last_failure,omitempty"`
	LastFailureReason   string    `json:"last_failure_reason,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	Stale               bool      `json:"stale"`
	AgeHours            float64   `json:"age_hours"`
	StaleAfterHours     float64   `json:"stale_after_hours"`
}

// Report is the health view of every configured root.
type Report struct {
	CheckedAt       int64    `json:"checked_at_ms"`
	StaleAfterHours float64  `json:"stale_after_hours"`
	Level           Level    `json:"level"`
	Roots           []Status `json:"roots"`
}

// Evaluate summarizes snapshots (newest first or unsorted) for the given roots.
// An empty or non-positive staleAfter falls back to 36 hours.
func Evaluate(snapshots []store.SnapshotInfo, roots []string, staleAfter time.Duration, now time.Time) Report {
	if now.IsZero() {
		now = time.Now()
	}
	if staleAfter <= 0 {
		staleAfter = 36 * time.Hour
	}
	rep := Report{
		CheckedAt:       now.UnixMilli(),
		StaleAfterHours: staleAfter.Hours(),
		Level:           LevelOK,
		Roots:           make([]Status, 0, len(roots)),
	}
	for _, root := range roots {
		list := snapshotsForRoot(snapshots, root)
		hs := evaluateRoot(root, list, staleAfter, now)
		rep.Roots = append(rep.Roots, hs)
		if worse(rep.Level, hs.Level) {
			rep.Level = hs.Level
		}
	}
	return rep
}

func evaluateRoot(root string, list []store.SnapshotInfo, staleAfter time.Duration, now time.Time) Status {
	hs := Status{
		Root:            root,
		Level:           LevelOK,
		StaleAfterHours: staleAfter.Hours(),
	}
	if len(list) == 0 {
		hs.Level = LevelDanger
		hs.Summary = "尚无快照"
		hs.Reason = "没有可用于判断的扫描记录"
		return hs
	}
	hs.LastAttempt = snapshotRef(&list[0])
	for i := range list {
		if strings.EqualFold(list[i].Status, "complete") {
			hs.LastComplete = snapshotRef(&list[i])
			break
		}
	}
	for i := range list {
		status := strings.ToLower(list[i].Status)
		if status == "failed" || status == "partial" {
			hs.LastFailure = snapshotRef(&list[i])
			break
		}
	}
	inProgress := false
	for i := range list {
		sn := &list[i]
		status := strings.ToLower(sn.Status)
		if status == "complete" {
			break
		}
		if status == "running" && now.Sub(time.UnixMilli(sn.StartedAt)) < runningStaleAfter {
			inProgress = true
			break
		}
		hs.ConsecutiveFailures++
		if hs.LastFailure == nil || sn.ID > hs.LastFailure.ID {
			hs.LastFailure = snapshotRef(sn)
		}
	}
	if hs.LastFailure != nil {
		hs.LastFailureReason = failureReason(hs.LastFailure)
	}
	if hs.LastComplete != nil {
		age := now.Sub(time.UnixMilli(hs.LastComplete.StartedAt))
		if age < 0 {
			age = 0
		}
		hs.AgeHours = age.Hours()
		hs.Stale = age > staleAfter
	}
	switch {
	case hs.LastComplete == nil:
		hs.Level = LevelDanger
	case hs.Stale:
		hs.Level = LevelDanger
	case hs.ConsecutiveFailures >= 2:
		hs.Level = LevelDanger
	case hs.ConsecutiveFailures == 1:
		hs.Level = LevelWarn
	}
	hs.Summary = statusSummary(hs)
	hs.Reason = statusReason(hs, inProgress)
	return hs
}

func statusSummary(hs Status) string {
	var parts []string
	if hs.LastComplete != nil {
		parts = append(parts, fmt.Sprintf("#%d %s · %.1f 小时前",
			hs.LastComplete.ID, hs.LastComplete.Kind, hs.AgeHours))
	} else {
		parts = append(parts, "尚无完整快照")
	}
	if hs.LastAttempt != nil && hs.LastAttempt.Status != "complete" {
		parts = append(parts, fmt.Sprintf("最近尝试 #%d %s", hs.LastAttempt.ID, hs.LastAttempt.Status))
	}
	if hs.ConsecutiveFailures > 0 {
		parts = append(parts, fmt.Sprintf("连续失败 %d 次", hs.ConsecutiveFailures))
	}
	if hs.Stale {
		parts = append(parts, fmt.Sprintf("已超过 %.0f 小时阈值", hs.StaleAfterHours))
	}
	return strings.Join(parts, " · ")
}

func statusReason(hs Status, inProgress bool) string {
	switch {
	case hs.LastComplete == nil && hs.LastFailure != nil:
		return fmt.Sprintf("还没有完成过一次整卷扫描；最近尝试 #%d %s：%s",
			hs.LastFailure.ID, hs.LastFailure.Status, failureReason(hs.LastFailure))
	case hs.LastComplete == nil:
		return "还没有完成过一次整卷扫描"
	case hs.ConsecutiveFailures > 0 && hs.LastFailure != nil:
		return fmt.Sprintf("#%d %s：%s", hs.LastFailure.ID, hs.LastFailure.Status, failureReason(hs.LastFailure))
	case hs.Stale:
		return fmt.Sprintf("最近完整快照 #%d 已 %.1f 小时未更新（阈值 %.0f 小时）",
			hs.LastComplete.ID, hs.AgeHours, hs.StaleAfterHours)
	case inProgress:
		return "扫描进行中"
	default:
		return ""
	}
}

func failureReason(sn *Snapshot) string {
	switch strings.ToLower(sn.Status) {
	case "partial":
		return "扫描被取消或超时"
	case "running":
		return "快照卡在 running，进程可能已退出"
	case "failed":
		if sn.Files == 0 && sn.Dirs == 0 {
			return "进程在完成前退出或被系统终止，未写入文件记录"
		}
		if sn.Errors > 0 {
			return fmt.Sprintf("扫描失败，结束前记录了 %d 个错误", sn.Errors)
		}
		return "扫描失败"
	default:
		return "扫描未完成"
	}
}

func snapshotsForRoot(snapshots []store.SnapshotInfo, root string) []store.SnapshotInfo {
	out := make([]store.SnapshotInfo, 0, 8)
	for _, sn := range snapshots {
		if sameRoot(sn.Root, root) {
			out = append(out, sn)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func sameRoot(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func snapshotRef(sn *store.SnapshotInfo) *Snapshot {
	if sn == nil {
		return nil
	}
	return &Snapshot{
		ID:         sn.ID,
		Kind:       sn.Kind,
		Status:     sn.Status,
		StartedAt:  sn.StartedAt,
		FinishedAt: sn.FinishedAt,
		Files:      sn.Files,
		Dirs:       sn.Dirs,
		Errors:     sn.Errors,
	}
}

func worse(a, b Level) bool {
	rank := func(l Level) int {
		switch l {
		case LevelDanger:
			return 2
		case LevelWarn:
			return 1
		default:
			return 0
		}
	}
	return rank(b) > rank(a)
}
