package model

import "time"

type FileRecord struct {
	FRN       uint64
	ParentFRN uint64
	Name      string
	Size      int64
	AllocSize int64
	MTime     time.Time
	Attrs     uint32
	IsDir     bool
	IsReparse bool
}

type ScanError struct {
	Path string `json:"path"`
	FRN  uint64 `json:"frn,omitempty"`
	Op   string `json:"op"`
	Msg  string `json:"msg"`
}

type ScanStats struct {
	Root             string      `json:"root"`
	Volume           string      `json:"volume"`
	VolumeSerial     uint32      `json:"volume_serial"`
	Files            int64       `json:"files"`
	Dirs             int64       `json:"dirs"`
	Bytes            int64       `json:"bytes"`
	AllocBytes       int64       `json:"alloc_bytes"`
	UniqueBytes      int64       `json:"unique_bytes"`
	UniqueAllocBytes int64       `json:"unique_alloc_bytes"`
	HardlinkDupes    int64       `json:"hardlink_dupes"`
	ReparseSkipped   int64       `json:"reparse_skipped"`
	Errors           int64       `json:"errors"`
	ErrorSamples     []ScanError `json:"error_samples,omitempty"`
	DurationMS       int64       `json:"duration_ms"`
	VolumeTotal      int64       `json:"volume_total_bytes"`
	VolumeFree       int64       `json:"volume_free_bytes"`
	VolumeUsed       int64       `json:"volume_used_bytes"`
	CoveragePct      float64     `json:"coverage_pct,omitempty"`
	HeapPeakBytes    uint64      `json:"heap_peak_bytes"`
	DedupeHardlinks  bool        `json:"dedupe_hardlinks"`
	IsVolumeRoot     bool        `json:"is_volume_root"`
}
