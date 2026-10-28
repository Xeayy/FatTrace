-- FatTrace schema v1. Applied by store.Migrate; version tracked in PRAGMA user_version.

CREATE TABLE IF NOT EXISTS volumes (
    id           INTEGER PRIMARY KEY,
    serial       INTEGER NOT NULL UNIQUE,          -- NTFS volume serial number (uint32)
    fs_type      TEXT    NOT NULL DEFAULT '',
    label        TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,                 -- unix ms
    last_seen_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS snapshots (
    id                 INTEGER PRIMARY KEY,
    volume_id          INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    kind               TEXT    NOT NULL CHECK (kind IN ('daily', 'weekly', 'manual')),
    status             TEXT    NOT NULL DEFAULT 'running'
                       CHECK (status IN ('running', 'complete', 'partial', 'failed')),
    root               TEXT    NOT NULL,
    started_at         INTEGER NOT NULL,
    finished_at        INTEGER,
    duration_ms        INTEGER NOT NULL DEFAULT 0,
    files              INTEGER NOT NULL DEFAULT 0,
    dirs               INTEGER NOT NULL DEFAULT 0,
    bytes              INTEGER NOT NULL DEFAULT 0,
    alloc_bytes        INTEGER NOT NULL DEFAULT 0,
    unique_bytes       INTEGER NOT NULL DEFAULT 0,
    unique_alloc_bytes INTEGER NOT NULL DEFAULT 0,
    hardlink_dupes     INTEGER NOT NULL DEFAULT 0,
    reparse_skipped    INTEGER NOT NULL DEFAULT 0,
    errors             INTEGER NOT NULL DEFAULT 0,
    volume_total       INTEGER NOT NULL DEFAULT 0,
    volume_free        INTEGER NOT NULL DEFAULT 0,
    volume_used        INTEGER NOT NULL DEFAULT 0,
    coverage_pct       REAL    NOT NULL DEFAULT 0,
    heap_peak_bytes    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_snapshots_volume_time ON snapshots(volume_id, started_at DESC);

-- Kept forever, deduplicated across scans (same path/op/msg collapses into one row).
CREATE TABLE IF NOT EXISTS scan_errors (
    id               INTEGER PRIMARY KEY,
    path             TEXT    NOT NULL,
    op               TEXT    NOT NULL,
    msg              TEXT    NOT NULL,
    first_seen_at    INTEGER NOT NULL,
    last_seen_at     INTEGER NOT NULL,
    last_snapshot_id INTEGER REFERENCES snapshots(id) ON DELETE SET NULL,
    hits             INTEGER NOT NULL DEFAULT 1,
    UNIQUE (path, op, msg)
);
CREATE INDEX IF NOT EXISTS idx_scan_errors_last_seen ON scan_errors(last_seen_at DESC);

-- Single baseline per volume; rewritten by each successful scan.
CREATE TABLE IF NOT EXISTS current_files (
    volume_id  INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    frn        INTEGER NOT NULL,
    parent_frn INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    alloc_size INTEGER NOT NULL DEFAULT 0,
    mtime_ms   INTEGER NOT NULL DEFAULT 0,
    attrs      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (volume_id, frn)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_current_files_parent ON current_files(volume_id, parent_frn);

CREATE TABLE IF NOT EXISTS current_dirs (
    volume_id        INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    frn              INTEGER NOT NULL,
    parent_frn       INTEGER NOT NULL,
    name             TEXT    NOT NULL,
    attrs            INTEGER NOT NULL DEFAULT 0,
    total_bytes      INTEGER NOT NULL DEFAULT 0,
    total_alloc_bytes INTEGER NOT NULL DEFAULT 0,
    total_files      INTEGER NOT NULL DEFAULT 0,
    unknown          INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (volume_id, frn)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_current_dirs_parent ON current_dirs(volume_id, parent_frn);

-- Weekly file-level snapshots (retention: last 3 weeks).
CREATE TABLE IF NOT EXISTS snap_files (
    snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    frn         INTEGER NOT NULL,
    parent_frn  INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    alloc_size  INTEGER NOT NULL DEFAULT 0,
    mtime_ms    INTEGER NOT NULL DEFAULT 0,
    attrs       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (snapshot_id, frn)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_snap_files_parent ON snap_files(snapshot_id, parent_frn);

-- Weekly directory aggregates (retention: 52 weeks, older rows downsampled).
CREATE TABLE IF NOT EXISTS snap_dirs (
    snapshot_id       INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    frn               INTEGER NOT NULL,
    parent_frn        INTEGER NOT NULL,
    name              TEXT    NOT NULL,
    depth             INTEGER NOT NULL DEFAULT 0,
    total_bytes       INTEGER NOT NULL DEFAULT 0,
    total_alloc_bytes INTEGER NOT NULL DEFAULT 0,
    total_files       INTEGER NOT NULL DEFAULT 0,
    unknown           INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (snapshot_id, frn)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_snap_dirs_parent ON snap_dirs(snapshot_id, parent_frn);

-- Top-N new/grew/moved events (retention: 8 weeks).
CREATE TABLE IF NOT EXISTS file_events (
    id          INTEGER PRIMARY KEY,
    volume_id   INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    snapshot_id INTEGER NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    kind        TEXT    NOT NULL
                CHECK (kind IN ('new', 'grew', 'shrank', 'deleted', 'moved_in', 'moved_out', 'moved')),
    frn         INTEGER NOT NULL,
    path        TEXT    NOT NULL DEFAULT '',
    old_path    TEXT    NOT NULL DEFAULT '',
    size_before INTEGER NOT NULL DEFAULT 0,
    size_after  INTEGER NOT NULL DEFAULT 0,
    delta_bytes INTEGER NOT NULL DEFAULT 0,
    detected_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_file_events_snap ON file_events(snapshot_id, kind, delta_bytes DESC);
CREATE INDEX IF NOT EXISTS idx_file_events_time ON file_events(volume_id, detected_at DESC);

-- Daily volume totals (retention: 90 days).
CREATE TABLE IF NOT EXISTS daily_summary (
    volume_id    INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    day          TEXT    NOT NULL,                 -- YYYY-MM-DD, local time
    snapshot_id  INTEGER REFERENCES snapshots(id) ON DELETE SET NULL,
    used_bytes   INTEGER NOT NULL DEFAULT 0,
    delta_bytes  INTEGER NOT NULL DEFAULT 0,
    files        INTEGER NOT NULL DEFAULT 0,
    errors       INTEGER NOT NULL DEFAULT 0,
    coverage_pct REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY (volume_id, day)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS daily_top_dirs (
    volume_id   INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    day         TEXT    NOT NULL,
    rank        INTEGER NOT NULL,
    path        TEXT    NOT NULL,
    delta_bytes INTEGER NOT NULL,
    PRIMARY KEY (volume_id, day, rank)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS alert_state (
    alert_key     TEXT PRIMARY KEY,
    last_fired_at INTEGER NOT NULL DEFAULT 0,
    last_value    REAL    NOT NULL DEFAULT 0,
    muted_until   INTEGER NOT NULL DEFAULT 0,
    hits          INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
