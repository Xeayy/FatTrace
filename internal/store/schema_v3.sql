-- FatTrace schema v3: daily summaries gain explanation coverage and audit links.

ALTER TABLE daily_summary ADD COLUMN root TEXT NOT NULL DEFAULT '';
ALTER TABLE daily_summary ADD COLUMN from_snapshot_id INTEGER REFERENCES snapshots(id) ON DELETE SET NULL;
ALTER TABLE daily_summary ADD COLUMN growth_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE daily_summary ADD COLUMN explained_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE daily_summary ADD COLUMN explained_pct REAL NOT NULL DEFAULT 0;
