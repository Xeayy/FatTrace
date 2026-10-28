-- FatTrace schema v2: staging area for an in-progress scan.

CREATE TABLE IF NOT EXISTS scan_files (
    volume_id  INTEGER NOT NULL REFERENCES volumes(id) ON DELETE CASCADE,
    frn        INTEGER NOT NULL,
    parent_frn INTEGER NOT NULL,
    name       TEXT    NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    alloc_size INTEGER NOT NULL DEFAULT 0,
    mtime_ms   INTEGER NOT NULL DEFAULT 0,
    attrs      INTEGER NOT NULL DEFAULT 0,
    is_dir     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (volume_id, frn)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_scan_files_parent ON scan_files(volume_id, parent_frn);
