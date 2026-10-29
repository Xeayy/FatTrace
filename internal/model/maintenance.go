package model

// DailySummary is one local-calendar-day delta for a volume root. Deltas compare
// the last complete snapshot of the day with the previous available day (or the
// first snapshot of the same day when there is no earlier one).
type DailySummary struct {
	VolumeID       int64   `json:"volume_id"`
	Root           string  `json:"root,omitempty"`
	Day            string  `json:"day"` // YYYY-MM-DD local time
	SnapshotID     int64   `json:"snapshot_id"`
	FromSnapshotID int64   `json:"from_snapshot_id,omitempty"`
	UsedBytes      int64   `json:"used_bytes"`
	DeltaBytes     int64   `json:"delta_bytes"`
	GrowthBytes    int64   `json:"growth_bytes"`
	ExplainedBytes int64   `json:"explained_bytes"`
	ExplainedPct   float64 `json:"explained_pct"`
	Files          int64   `json:"files"`
	Errors         int64   `json:"errors"`
	CoveragePct    float64 `json:"coverage_pct"`
}

// DailyTopDir is one ranked growth directory for a day.
type DailyTopDir struct {
	VolumeID   int64  `json:"volume_id"`
	Day        string `json:"day"`
	Rank       int    `json:"rank"`
	Path       string `json:"path"`
	DeltaBytes int64  `json:"delta_bytes"`
}

// FileEvent is one retained top event (new/grew/shrunk/deleted/moved).
type FileEvent struct {
	VolumeID   int64  `json:"volume_id"`
	SnapshotID int64  `json:"snapshot_id"`
	Kind       string `json:"kind"`
	FRN        uint64 `json:"frn"`
	Path       string `json:"path,omitempty"`
	OldPath    string `json:"old_path,omitempty"`
	SizeBefore int64  `json:"size_before"`
	SizeAfter  int64  `json:"size_after"`
	DeltaBytes int64  `json:"delta_bytes"`
	DetectedAt int64  `json:"detected_at_ms"`
}

// AlertState is the persisted dedup/silence state for one alert key.
type AlertState struct {
	Key         string  `json:"key"`
	LastFiredAt int64   `json:"last_fired_at_ms"`
	LastValue   float64 `json:"last_value"`
	MutedUntil  int64   `json:"muted_until_ms"`
	Hits        int64   `json:"hits"`
}

// Alert is the evaluated view of one growth threshold.
type Alert struct {
	Key            string  `json:"key"`
	VolumeID       int64   `json:"volume_id"`
	Root           string  `json:"root,omitempty"`
	Day            string  `json:"day,omitempty"`
	Kind           string  `json:"kind"`
	Message        string  `json:"message"`
	ValueBytes     int64   `json:"value_bytes"`
	UsedBytes      int64   `json:"used_bytes"`
	ThresholdBytes int64   `json:"threshold_bytes"`
	ThresholdPct   float64 `json:"threshold_pct"`
	Triggered      bool    `json:"triggered"`
	Fired          bool    `json:"fired"`
	Muted          bool    `json:"muted"`
	LastFiredAt    int64   `json:"last_fired_at_ms,omitempty"`
	MutedUntil     int64   `json:"muted_until_ms,omitempty"`
	Hits           int64   `json:"hits"`
}
