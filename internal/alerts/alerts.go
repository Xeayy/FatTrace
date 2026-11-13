// Package alerts evaluates daily growth thresholds against persisted alert
// state. The read-only server uses Preview (no writes); CLI and maintain use
// Evaluate to record fires and silence windows.
package alerts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/model"
	"github.com/Xeayy/FatTrace/internal/store"
)

// Options controls evaluation time and persistence.
type Options struct {
	Now     time.Time
	Persist bool
}

// Report lists evaluated alerts newest key first.
type Report struct {
	CheckedAt int64         `json:"checked_at_ms"`
	Alerts    []model.Alert `json:"alerts"`
}

// Evaluate compares the latest daily summary of each volume with the configured
// thresholds. Firing advances the silence window so repeated runs stay quiet.
func Evaluate(ctx context.Context, st *store.Store, cfg config.Alerts, opts Options) (Report, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	nowMS := opts.Now.UnixMilli()
	rep := Report{CheckedAt: nowMS, Alerts: []model.Alert{}}
	if cfg.GrowthBytes == 0 && cfg.GrowthPct == 0 && cfg.GrowthMultiple == 0 {
		return rep, nil
	}
	summaries, err := st.ListDailySummaries(ctx, 0, 1000)
	if err != nil {
		return rep, err
	}
	states, err := st.AlertStates(ctx)
	if err != nil {
		return rep, err
	}
	byKey := make(map[string]model.AlertState, len(states))
	for _, s := range states {
		byKey[s.Key] = s
	}

	latest := make(map[int64]model.DailySummary, 8)
	history := make(map[int64][]model.DailySummary, 8)
	for _, s := range summaries {
		if _, ok := latest[s.VolumeID]; !ok {
			latest[s.VolumeID] = s
		}
		history[s.VolumeID] = append(history[s.VolumeID], s)
	}
	silence := time.Duration(cfg.SilenceHours * float64(time.Hour))

	for _, sum := range latest {
		value := sum.DeltaBytes
		pct := 0.0
		if sum.UsedBytes > 0 {
			pct = float64(value) / float64(sum.UsedBytes) * 100
		}
		baseline, samples := medianPriorGrowth(history[sum.VolumeID], sum.Day)
		multiple := 0.0
		if baseline > 0 && value > 0 {
			multiple = float64(value) / float64(baseline)
		}
		multipleHit := cfg.GrowthMultiple > 0 && samples >= 3 &&
			baseline >= 1<<20 && value > 0 &&
			(cfg.GrowthMultipleFloorBytes == 0 || value >= cfg.GrowthMultipleFloorBytes) &&
			multiple > cfg.GrowthMultiple
		triggered := (cfg.GrowthBytes > 0 && value > cfg.GrowthBytes) ||
			(cfg.GrowthPct > 0 && value > 0 && pct > cfg.GrowthPct) ||
			multipleHit
		key := fmt.Sprintf("growth:%d", sum.VolumeID)
		state := byKey[key]
		state.Key = key
		a := model.Alert{
			Key: key, VolumeID: sum.VolumeID, Root: sum.Root, Day: sum.Day,
			Kind: "growth", ValueBytes: value, UsedBytes: sum.UsedBytes,
			ThresholdBytes: cfg.GrowthBytes, ThresholdPct: cfg.GrowthPct,
			ThresholdMultiple: cfg.GrowthMultiple, BaselineBytes: baseline,
			Multiple: multiple, Triggered: triggered, LastFiredAt: state.LastFiredAt,
			MutedUntil: state.MutedUntil, Hits: state.Hits,
		}
		a.Muted = triggered && state.MutedUntil > nowMS
		if triggered && !a.Muted {
			a.Fired = true
			state.LastFiredAt = nowMS
			state.LastValue = float64(value)
			state.MutedUntil = nowMS + silence.Milliseconds()
			state.Hits++
			a.LastFiredAt = state.LastFiredAt
			a.MutedUntil = state.MutedUntil
			a.Hits = state.Hits
		} else if triggered {
			state.LastValue = float64(value)
		}
		if triggered {
			a.Message = alertMessage(sum, value, pct, multiple, baseline, samples, cfg)
		}
		if opts.Persist && (a.Fired || triggered) {
			if err := st.SaveAlertState(ctx, state); err != nil {
				return rep, fmt.Errorf("save alert state %s: %w", key, err)
			}
		}
		rep.Alerts = append(rep.Alerts, a)
	}
	sort.Slice(rep.Alerts, func(i, j int) bool { return rep.Alerts[i].Key < rep.Alerts[j].Key })
	return rep, nil
}

func alertMessage(sum model.DailySummary, value int64, pct, multiple float64,
	baseline int64, samples int, cfg config.Alerts) string {
	root := sum.Root
	if root == "" {
		root = fmt.Sprintf("卷 %d", sum.VolumeID)
	}
	msg := fmt.Sprintf("%s %s 净增长 %s", root, sum.Day, formatBytes(value))
	var parts []string
	if cfg.GrowthBytes > 0 && value > cfg.GrowthBytes {
		parts = append(parts, "阈值 "+formatBytes(cfg.GrowthBytes))
	}
	if cfg.GrowthPct > 0 && value > 0 && pct > cfg.GrowthPct {
		parts = append(parts, fmt.Sprintf("阈值 %.1f%%（实际 %.2f%%）", cfg.GrowthPct, pct))
	}
	if cfg.GrowthMultiple > 0 && samples >= 3 && baseline >= 1<<20 && value > 0 &&
		(cfg.GrowthMultipleFloorBytes == 0 || value >= cfg.GrowthMultipleFloorBytes) &&
		multiple > cfg.GrowthMultiple {
		parts = append(parts, fmt.Sprintf("%.1f× 前 14 天正增长日中位 %s（阈值 %.1f×）",
			multiple, formatBytes(baseline), cfg.GrowthMultiple))
	}
	if len(parts) > 0 {
		msg += "（" + strings.Join(parts, "；") + "）"
	}
	return msg
}

// medianPriorGrowth returns the median positive net growth within the 14
// calendar days before currentDay. Negative and zero days are ignored so
// deletions do not lower the spike baseline; fewer than three samples means
// "not enough history".
func medianPriorGrowth(history []model.DailySummary, currentDay string) (int64, int) {
	current, err := time.ParseInLocation("2006-01-02", currentDay, time.Local)
	if err != nil {
		return 0, 0
	}
	cutoff := current.AddDate(0, 0, -14)
	vals := make([]int64, 0, 14)
	for _, h := range history {
		if h.DeltaBytes <= 0 {
			continue
		}
		day, err := time.ParseInLocation("2006-01-02", h.Day, time.Local)
		if err != nil || !day.Before(current) || day.Before(cutoff) {
			continue
		}
		vals = append(vals, h.DeltaBytes)
	}
	if len(vals) == 0 {
		return 0, 0
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	mid := len(vals) / 2
	if len(vals)%2 == 1 {
		return vals[mid], len(vals)
	}
	return (vals[mid-1] + vals[mid]) / 2, len(vals)
}

// SetMute writes a silence window for the given keys, creating state rows as
// needed. Existing fire counters are preserved.
func SetMute(ctx context.Context, st *store.Store, keys []string, until time.Time) error {
	states, err := st.AlertStates(ctx)
	if err != nil {
		return err
	}
	byKey := make(map[string]model.AlertState, len(states))
	for _, s := range states {
		byKey[s.Key] = s
	}
	for _, key := range keys {
		state := byKey[key]
		state.Key = key
		state.MutedUntil = until.UnixMilli()
		if err := st.SaveAlertState(ctx, state); err != nil {
			return err
		}
	}
	return nil
}

// ClearMutes removes silence windows without touching fire counters.
func ClearMutes(ctx context.Context, st *store.Store, keys []string) error {
	states, err := st.AlertStates(ctx)
	if err != nil {
		return err
	}
	want := make(map[string]bool, len(keys))
	for _, k := range keys {
		want[k] = true
	}
	for _, s := range states {
		if !want[s.Key] {
			continue
		}
		s.MutedUntil = 0
		if err := st.SaveAlertState(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit && n > -unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	v := n
	if v < 0 {
		v = -v
	}
	for v/unit >= unit {
		div *= unit
		exp++
		v /= unit
	}
	sign := ""
	if n < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%.2f %ciB", sign, float64(abs(n))/float64(div), "KMGTPE"[exp])
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
