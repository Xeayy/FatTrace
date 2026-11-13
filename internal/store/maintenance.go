package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Xeayy/FatTrace/internal/model"
)

func (s *Store) requireWriter() error {
	if s.reader {
		return errors.New("store: write requires a writable store")
	}
	return nil
}

// CompleteVolumeRootSnapshots returns every complete volume-root snapshot in
// chronological order, grouped by volume and root. Subdirectory scans are
// excluded because their deltas are not comparable to volume totals.
func (s *Store) CompleteVolumeRootSnapshots(ctx context.Context) ([]SnapshotInfo, error) {
	rows, err := s.db.QueryContext(ctx, snapshotSelectSQL+`
		WHERE status = 'complete'
		ORDER BY volume_id, lower(root), started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotInfo
	for rows.Next() {
		sn, err := scanSnapshotInfo(rows)
		if err != nil {
			return nil, err
		}
		if model.IsVolumeRoot(sn.Root) {
			out = append(out, sn)
		}
	}
	return out, rows.Err()
}

const dailySummaryColumns = `volume_id, root, day, snapshot_id, from_snapshot_id, used_bytes,
	delta_bytes, growth_bytes, explained_bytes, explained_pct, files, errors, coverage_pct`

// SaveDailySummary replaces one volume-day summary and its top directories.
func (s *Store) SaveDailySummary(ctx context.Context, sum model.DailySummary, dirs []model.DailyTopDir) error {
	if err := s.requireWriter(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM daily_summary WHERE volume_id = ? AND day = ?`, sum.VolumeID, sum.Day); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO daily_summary (`+dailySummaryColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sum.VolumeID, sum.Root, sum.Day, nullID(sum.SnapshotID), nullID(sum.FromSnapshotID),
		sum.UsedBytes, sum.DeltaBytes, sum.GrowthBytes, sum.ExplainedBytes, sum.ExplainedPct,
		sum.Files, sum.Errors, sum.CoveragePct); err != nil {
		return fmt.Errorf("insert daily summary %d/%s: %w", sum.VolumeID, sum.Day, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM daily_top_dirs WHERE volume_id = ? AND day = ?`, sum.VolumeID, sum.Day); err != nil {
		return err
	}
	if len(dirs) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			buildInsertSQL("daily_top_dirs", "volume_id, day, rank, path, delta_bytes", len(dirs), 5))
		if err != nil {
			return err
		}
		defer stmt.Close()
		args := make([]any, 0, len(dirs)*5)
		for _, d := range dirs {
			args = append(args, d.VolumeID, d.Day, d.Rank, d.Path, d.DeltaBytes)
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert daily top dirs %d/%s: %w", sum.VolumeID, sum.Day, err)
		}
	}
	return tx.Commit()
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// ListDailySummaries returns the newest days first; volumeID 0 means all volumes.
func (s *Store) ListDailySummaries(ctx context.Context, volumeID int64, limit int) ([]model.DailySummary, error) {
	if limit <= 0 {
		limit = 90
	}
	query := `SELECT ` + dailySummaryColumns + ` FROM daily_summary`
	var args []any
	if volumeID > 0 {
		query += ` WHERE volume_id = ?`
		args = append(args, volumeID)
	}
	query += ` ORDER BY day DESC, volume_id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DailySummary
	for rows.Next() {
		d, err := scanDailySummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func scanDailySummary(row rowScanner) (model.DailySummary, error) {
	var d model.DailySummary
	var snapshot, from sql.NullInt64
	err := row.Scan(&d.VolumeID, &d.Root, &d.Day, &snapshot, &from, &d.UsedBytes,
		&d.DeltaBytes, &d.GrowthBytes, &d.ExplainedBytes, &d.ExplainedPct,
		&d.Files, &d.Errors, &d.CoveragePct)
	if err != nil {
		return model.DailySummary{}, err
	}
	d.SnapshotID = snapshot.Int64
	d.FromSnapshotID = from.Int64
	return d, nil
}

// DailyTopDirs returns the ranked growth directories for one volume-day.
func (s *Store) DailyTopDirs(ctx context.Context, volumeID int64, day string, limit int) ([]model.DailyTopDir, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT volume_id, day, rank, path, delta_bytes FROM daily_top_dirs
		WHERE volume_id = ? AND day = ? ORDER BY rank LIMIT ?`, volumeID, day, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.DailyTopDir
	for rows.Next() {
		var d model.DailyTopDir
		if err := rows.Scan(&d.VolumeID, &d.Day, &d.Rank, &d.Path, &d.DeltaBytes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ReplaceFileEvents rewrites the retained top events of one snapshot.
func (s *Store) ReplaceFileEvents(ctx context.Context, snapshotID int64, events []model.FileEvent) error {
	if err := s.requireWriter(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM file_events WHERE snapshot_id = ?`, snapshotID); err != nil {
		return err
	}
	if len(events) > 0 {
		const cols = 10
		stmt, err := tx.PrepareContext(ctx, buildInsertSQL("file_events",
			"volume_id, snapshot_id, kind, frn, path, old_path, size_before, size_after, delta_bytes, detected_at",
			len(events), cols))
		if err != nil {
			return err
		}
		defer stmt.Close()
		args := make([]any, 0, len(events)*cols)
		for _, e := range events {
			args = append(args, e.VolumeID, e.SnapshotID, e.Kind, int64(e.FRN), e.Path, e.OldPath,
				e.SizeBefore, e.SizeAfter, e.DeltaBytes, e.DetectedAt)
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("insert file events for #%d: %w", snapshotID, err)
		}
	}
	return tx.Commit()
}

// AlertStates returns every persisted alert state, sorted by key.
func (s *Store) AlertStates(ctx context.Context) ([]model.AlertState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT alert_key, last_fired_at, last_value, muted_until, hits FROM alert_state ORDER BY alert_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AlertState
	for rows.Next() {
		var a model.AlertState
		if err := rows.Scan(&a.Key, &a.LastFiredAt, &a.LastValue, &a.MutedUntil, &a.Hits); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveAlertState upserts one alert state row.
func (s *Store) SaveAlertState(ctx context.Context, a model.AlertState) error {
	if err := s.requireWriter(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_state (alert_key, last_fired_at, last_value, muted_until, hits)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(alert_key) DO UPDATE SET
			last_fired_at = excluded.last_fired_at,
			last_value = excluded.last_value,
			muted_until = excluded.muted_until,
			hits = excluded.hits`,
		a.Key, a.LastFiredAt, a.LastValue, a.MutedUntil, a.Hits)
	return err
}

// PruneSnapshotCandidates returns expired daily/weekly snapshots while always
// keeping the newest complete snapshot of every volume-root group. manual and
// running snapshots are never candidates. A zero cutoff disables that kind.
func (s *Store) PruneSnapshotCandidates(ctx context.Context, dailyBefore, weeklyBefore int64) ([]SnapshotInfo, error) {
	rows, err := s.db.QueryContext(ctx, snapshotSelectSQL+` ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []SnapshotInfo
	for rows.Next() {
		sn, err := scanSnapshotInfo(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	latest := make(map[string]int64, len(all))
	groupKey := func(sn SnapshotInfo) string {
		return fmt.Sprintf("%d|%s", sn.VolumeID, strings.ToLower(sn.Root))
	}
	// Only complete rows count as latest: a newer failed or partial run must
	// not leave the newest complete baseline unprotected.
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Status != "complete" {
			continue
		}
		key := groupKey(all[i])
		if _, ok := latest[key]; !ok {
			latest[key] = all[i].ID
		}
	}

	var out []SnapshotInfo
	for _, sn := range all {
		if sn.Status == "running" || latest[groupKey(sn)] == sn.ID {
			continue
		}
		switch sn.Kind {
		case "daily":
			if dailyBefore > 0 && sn.StartedAt < dailyBefore {
				out = append(out, sn)
			}
		case "weekly":
			if weeklyBefore > 0 && sn.StartedAt < weeklyBefore {
				out = append(out, sn)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out, nil
}

// SnapshotFilePruneCandidates returns complete daily/weekly snapshots whose
// file rows are older than the cutoffs. The newest two complete snapshots of
// every volume/root/kind group are protected so default diff pairing and weekly
// reports keep working; manual snapshots are never candidates. A zero cutoff
// disables that kind.
func (s *Store) SnapshotFilePruneCandidates(ctx context.Context, dailyBefore, weeklyBefore int64) ([]SnapshotInfo, error) {
	rows, err := s.db.QueryContext(ctx, snapshotSelectSQL+` ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []SnapshotInfo
	for rows.Next() {
		sn, err := scanSnapshotInfo(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	groupKey := func(sn SnapshotInfo) string {
		return fmt.Sprintf("%d|%s|%s", sn.VolumeID, strings.ToLower(sn.Root), sn.Kind)
	}
	groups := make(map[string][]SnapshotInfo)
	for _, sn := range all {
		if sn.Status == "complete" {
			groups[groupKey(sn)] = append(groups[groupKey(sn)], sn)
		}
	}
	protected := make(map[int64]bool)
	for _, list := range groups {
		for i := len(list) - 1; i >= 0 && i >= len(list)-2; i-- {
			protected[list[i].ID] = true
		}
	}

	var out []SnapshotInfo
	for _, sn := range all {
		if sn.Status != "complete" || protected[sn.ID] {
			continue
		}
		switch sn.Kind {
		case "daily":
			if dailyBefore > 0 && sn.StartedAt < dailyBefore {
				out = append(out, sn)
			}
		case "weekly":
			if weeklyBefore > 0 && sn.StartedAt < weeklyBefore {
				out = append(out, sn)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out, nil
}

// CountSnapshotFiles counts snap_files rows for the given snapshots.
func (s *Store) CountSnapshotFiles(ctx context.Context, ids []int64) (int64, error) {
	var total int64
	const batch = 400
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		var n int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM snap_files WHERE snapshot_id IN (`+placeholders+`)`,
			args...).Scan(&n); err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// DeleteSnapshotFiles removes only the file-level rows of the given snapshots.
// The snapshot metadata, directory aggregates and daily summaries stay intact.
func (s *Store) DeleteSnapshotFiles(ctx context.Context, ids []int64) (int64, error) {
	if err := s.requireWriter(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var deleted int64
	const batch = 400
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		res, err := tx.ExecContext(ctx,
			`DELETE FROM snap_files WHERE snapshot_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// DeleteSnapshots removes snapshots and everything cascading from them. It only
// touches FatTrace's own history tables.
func (s *Store) DeleteSnapshots(ctx context.Context, ids []int64) (int64, error) {
	if err := s.requireWriter(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var deleted int64
	const batch = 400
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		part := ids[start:end]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		res, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// DeleteDailyHistoryBefore removes summaries and top directories older than day
// (YYYY-MM-DD).
func (s *Store) DeleteDailyHistoryBefore(ctx context.Context, day string) (summaries, dirs int64, err error) {
	if err := s.requireWriter(); err != nil {
		return 0, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM daily_summary WHERE day < ?`, day)
	if err != nil {
		return 0, 0, err
	}
	if summaries, err = res.RowsAffected(); err != nil {
		return 0, 0, err
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM daily_top_dirs WHERE day < ?`, day)
	if err != nil {
		return 0, 0, err
	}
	if dirs, err = res.RowsAffected(); err != nil {
		return 0, 0, err
	}
	return summaries, dirs, tx.Commit()
}

// CountDailyHistoryBefore counts summaries, top directories and retained events
// that a prune would delete. An empty day skips the summary counts.
func (s *Store) CountDailyHistoryBefore(ctx context.Context, day string, eventsBeforeMS int64) (summaries, dirs, events int64, err error) {
	if day != "" {
		if err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM daily_summary WHERE day < ?`, day).Scan(&summaries); err != nil {
			return 0, 0, 0, err
		}
		if err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM daily_top_dirs WHERE day < ?`, day).Scan(&dirs); err != nil {
			return 0, 0, 0, err
		}
	}
	if eventsBeforeMS > 0 {
		if err = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM file_events WHERE detected_at < ?`, eventsBeforeMS).Scan(&events); err != nil {
			return 0, 0, 0, err
		}
	}
	return summaries, dirs, events, nil
}

// DeleteFileEventsBefore removes retained events older than beforeMS.
func (s *Store) DeleteFileEventsBefore(ctx context.Context, beforeMS int64) (int64, error) {
	if err := s.requireWriter(); err != nil {
		return 0, err
	}
	if beforeMS <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM file_events WHERE detected_at < ?`, beforeMS)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
