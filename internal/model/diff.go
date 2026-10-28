package model

// ChangeKind classifies one file or directory difference between snapshots.
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"
	ChangeDeleted ChangeKind = "deleted"
	ChangeGrown   ChangeKind = "grown"
	ChangeShrunk  ChangeKind = "shrunk"
	ChangeMoved   ChangeKind = "moved"
	ChangeRenamed ChangeKind = "renamed"
)

// SnapshotDir mirrors one snap_dirs row: identity, position and recursive totals.
type SnapshotDir struct {
	FRN             uint64
	ParentFRN       uint64
	Name            string
	Depth           int
	TotalBytes      int64
	TotalAllocBytes int64
	TotalFiles      int64
	Unknown         bool
}

// FileChange describes a single file-level difference. DeltaBytes/DeltaAlloc
// are signed; alloc is the primary accounting unit (compression, sparse files).
type FileChange struct {
	FRN                 uint64     `json:"frn"`
	Kind                ChangeKind `json:"kind"`
	ParentFRN           uint64     `json:"parent_frn"`
	OldParentFRN        uint64     `json:"old_parent_frn,omitempty"`
	Name                string     `json:"name"`
	OldName             string     `json:"old_name,omitempty"`
	Path                string     `json:"path,omitempty"`
	OldPath             string     `json:"old_path,omitempty"`
	SizeBefore          int64      `json:"size_before"`
	SizeAfter           int64      `json:"size_after"`
	AllocBefore         int64      `json:"alloc_before"`
	AllocAfter          int64      `json:"alloc_after"`
	DeltaBytes          int64      `json:"delta_bytes"`
	DeltaAlloc          int64      `json:"delta_alloc"`
	MTimeBeforeMS       int64      `json:"mtime_before_ms,omitempty"`
	MTimeAfterMS        int64      `json:"mtime_after_ms,omitempty"`
	SuspectedFromVolume uint32     `json:"suspected_from_volume,omitempty"`
}

// DirChange describes one directory difference. DeltaAlloc is the move-adjusted
// intrinsic growth: ownership shifts from moving a subtree are removed from
// ancestors, so a pure move contributes zero everywhere.
type DirChange struct {
	FRN              uint64     `json:"frn"`
	Kind             ChangeKind `json:"kind"`
	ParentFRN        uint64     `json:"parent_frn"`
	OldParentFRN     uint64     `json:"old_parent_frn,omitempty"`
	Name             string     `json:"name"`
	OldName          string     `json:"old_name,omitempty"`
	Path             string     `json:"path,omitempty"`
	OldPath          string     `json:"old_path,omitempty"`
	Depth            int        `json:"depth"`
	TotalBytesBefore int64      `json:"total_bytes_before"`
	TotalBytesAfter  int64      `json:"total_bytes_after"`
	TotalAllocBefore int64      `json:"total_alloc_before"`
	TotalAllocAfter  int64      `json:"total_alloc_after"`
	DeltaBytes       int64      `json:"delta_bytes"`
	DeltaAlloc       int64      `json:"delta_alloc"`
	FilesBefore      int64      `json:"files_before"`
	FilesAfter       int64      `json:"files_after"`
	UnknownBefore    bool       `json:"unknown_before,omitempty"`
	UnknownAfter     bool       `json:"unknown_after,omitempty"`
	NewlyUnknown     bool       `json:"newly_unknown,omitempty"`
}

// FileCandidate identifies a file by name, size and mtime on another volume.
// Used to annotate added files as suspected cross-volume moves.
type FileCandidate struct {
	Name         string
	Size         int64
	MTimeMS      int64
	VolumeSerial uint32
}

// DiffTotals summarizes a snapshot diff. Added and grown are positive;
// deleted and shrunk are negative, so Delta* equals their plain sum.
type DiffTotals struct {
	FilesAdded   int64 `json:"files_added"`
	FilesDeleted int64 `json:"files_deleted"`
	FilesGrown   int64 `json:"files_grown"`
	FilesShrunk  int64 `json:"files_shrunk"`
	FilesMoved   int64 `json:"files_moved"`
	FilesRenamed int64 `json:"files_renamed"`
	DirsAdded    int64 `json:"dirs_added"`
	DirsDeleted  int64 `json:"dirs_deleted"`
	DirsMoved    int64 `json:"dirs_moved"`
	DirsRenamed  int64 `json:"dirs_renamed"`
	BytesAdded   int64 `json:"bytes_added"`
	BytesDeleted int64 `json:"bytes_deleted"`
	BytesGrown   int64 `json:"bytes_grown"`
	BytesShrunk  int64 `json:"bytes_shrunk"`
	AllocAdded   int64 `json:"alloc_added"`
	AllocDeleted int64 `json:"alloc_deleted"`
	AllocGrown   int64 `json:"alloc_grown"`
	AllocShrunk  int64 `json:"alloc_shrunk"`
	DeltaBytes   int64 `json:"delta_bytes"`
	DeltaAlloc   int64 `json:"delta_alloc"`
}

// VolumeCheck compares the root directory delta with the volume-used delta
// recorded by the two scans. Percentages outside tolerance indicate blind spots
// (protected directories, unincluded roots, non-file-system usage).
type VolumeCheck struct {
	DiskDeltaAlloc int64   `json:"disk_delta_alloc"`
	RootDeltaAlloc int64   `json:"root_delta_alloc"`
	DiffPct        float64 `json:"diff_pct"`
	OK             bool    `json:"ok"`
	Skipped        bool    `json:"skipped,omitempty"`
	UnknownDirs    int64   `json:"unknown_dirs_after"`
	Message        string  `json:"message,omitempty"`
}

// SnapshotRef is the subset of a snapshot row needed to describe a diff side.
type SnapshotRef struct {
	ID               int64   `json:"id"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	StartedAtMS      int64   `json:"started_at_ms"`
	FinishedAtMS     int64   `json:"finished_at_ms,omitempty"`
	Files            int64   `json:"files"`
	Dirs             int64   `json:"dirs"`
	UniqueAllocBytes int64   `json:"unique_alloc_bytes"`
	Errors           int64   `json:"errors"`
	CoveragePct      float64 `json:"coverage_pct"`
}

// DiffResult is the complete, serializable result of comparing two snapshots.
type DiffResult struct {
	VolumeID     int64        `json:"volume_id"`
	Root         string       `json:"root"`
	FromSnapshot SnapshotRef  `json:"from_snapshot"`
	ToSnapshot   SnapshotRef  `json:"to_snapshot"`
	Totals       DiffTotals   `json:"totals"`
	TopGrowth    []FileChange `json:"top_growth,omitempty"`
	TopDeleted   []FileChange `json:"top_deleted,omitempty"`
	Moves        []FileChange `json:"moves,omitempty"`
	TopDirs      []DirChange  `json:"top_growth_dirs,omitempty"`
	ShrunkDirs   []DirChange  `json:"top_shrunk_dirs,omitempty"`
	DirMoves     []DirChange  `json:"dir_moves,omitempty"`
	Check        VolumeCheck  `json:"volume_check"`
	Warnings     []string     `json:"warnings,omitempty"`
}
