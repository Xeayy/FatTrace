package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql schema_v2.sql
var schemaFS embed.FS

const schemaVersion = 2

// migrations[i] upgrades the schema from version i to i+1.
var migrations = []string{mustRead("schema.sql"), mustRead("schema_v2.sql")}

var (
	// ErrLocked means another process holds the writer lock for this database.
	ErrLocked = errors.New("store: database is locked by another process")
	// ErrNotInitialized means the database file exists but has no FatTrace schema yet.
	ErrNotInitialized = errors.New("store: database has no FatTrace schema; run \"fat db init\"")
)

type Store struct {
	db     *sql.DB
	path   string
	lock   *fileLock
	reader bool
}

// DefaultHome returns the directory holding the default database.
// FATTRACE_HOME overrides it (used by tests and portable installs).
func DefaultHome() (string, error) {
	if h := os.Getenv("FATTRACE_HOME"); h != "" {
		abs, err := filepath.Abs(h)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	base, err := os.UserCacheDir() // %LocalAppData% on Windows
	if err != nil || base == "" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if base == "" {
		return "", errors.New("store: cannot determine LOCALAPPDATA; set FATTRACE_HOME")
	}
	return filepath.Join(base, "FatTrace"), nil
}

func DefaultPath() (string, error) {
	home, err := DefaultHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "fattrace.db"), nil
}

// Open opens the database for writing, takes the process-wide writer lock and
// applies pending migrations.
func Open(path string) (*Store, error) { return open(path, false) }

// OpenReader opens the database read-only (no writer lock, no migration). It is
// used by status/UI paths so they keep working while a scan owns the writer lock.
func OpenReader(path string) (*Store, error) { return open(path, true) }

func open(path string, reader bool) (*Store, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if reader {
		if _, err := os.Stat(abs); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	var lk *fileLock
	if !reader {
		lk, err = acquireLock(abs + ".lock")
		if err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", dsn(abs, reader))
	if err != nil {
		if lk != nil {
			lk.Close()
		}
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: abs, lock: lk, reader: reader}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("open %s: %w", abs, err)
	}
	if reader {
		v, err := s.SchemaVersion(ctx)
		if err != nil {
			s.Close()
			return nil, err
		}
		if v == 0 {
			s.Close()
			return nil, ErrNotInitialized
		}
		if v > schemaVersion {
			s.Close()
			return nil, fmt.Errorf("store: schema v%d is newer than supported v%d", v, schemaVersion)
		}
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		s.Close()
		return nil, fmt.Errorf("read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		s.Close()
		return nil, fmt.Errorf("store: WAL mode unavailable on %s (got %q); network drives are unsupported", abs, mode)
	}
	if !reader {
		if err := s.Migrate(ctx); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
		s.db = nil
	}
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
	return err
}

func (s *Store) Path() string { return s.path }

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

// Migrate applies all pending schema migrations. It is idempotent and safe to
// call repeatedly; each step runs in its own transaction together with the
// user_version bump, so an interrupted migration leaves no partial schema.
func (s *Store) Migrate(ctx context.Context) error {
	if s.reader {
		return errors.New("store: migration requires a writable store")
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("store: schema v%d is newer than supported v%d", v, len(migrations))
	}
	for v < len(migrations) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate to v%d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", v+1, err)
		}
		v++
	}
	return nil
}

func dsn(path string, reader bool) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "temp_store(MEMORY)")
	q.Add("_pragma", "cache_size(-32768)")
	if reader {
		q.Add("_pragma", "query_only(ON)")
	}
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path), RawQuery: q.Encode()}
	if filepath.VolumeName(path) == "" {
		u.Path = filepath.ToSlash(path)
	}
	return u.String()
}

func mustRead(name string) string {
	b, err := schemaFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

type TableCount struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

type VolumeInfo struct {
	ID        int64  `json:"id"`
	Serial    uint32 `json:"serial"`
	FSType    string `json:"fs_type"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at_ms"`
	LastSeen  int64  `json:"last_seen_at_ms"`
}

type SnapshotInfo struct {
	ID               int64   `json:"id"`
	VolumeID         int64   `json:"volume_id"`
	Kind             string  `json:"kind"`
	Status           string  `json:"status"`
	Root             string  `json:"root"`
	StartedAt        int64   `json:"started_at_ms"`
	FinishedAt       *int64  `json:"finished_at_ms,omitempty"`
	DurationMS       int64   `json:"duration_ms"`
	Files            int64   `json:"files"`
	Dirs             int64   `json:"dirs"`
	UniqueAllocBytes int64   `json:"unique_alloc_bytes"`
	Errors           int64   `json:"errors"`
	CoveragePct      float64 `json:"coverage_pct"`
}

type Status struct {
	Path            string         `json:"path"`
	SizeBytes       int64          `json:"size_bytes"`
	WALBytes        int64          `json:"wal_bytes"`
	SchemaVersion   int            `json:"schema_version"`
	Tables          []TableCount   `json:"tables"`
	Volumes         []VolumeInfo   `json:"volumes"`
	RecentSnapshots []SnapshotInfo `json:"recent_snapshots"`
}

var tableNames = []string{
	"volumes",
	"snapshots",
	"scan_errors",
	"current_files",
	"current_dirs",
	"scan_files",
	"snap_files",
	"snap_dirs",
	"file_events",
	"daily_summary",
	"daily_top_dirs",
	"alert_state",
}

// Status returns database metadata, row counts, known volumes and recent snapshots.
func (s *Store) Status(ctx context.Context) (*Status, error) {
	st := &Status{Path: s.path}
	if fi, err := os.Stat(s.path); err == nil {
		st.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(s.path + "-wal"); err == nil {
		st.WALBytes = fi.Size()
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	st.SchemaVersion = v

	existing := make(map[string]bool, len(tableNames))
	trows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var name string
		if err := trows.Scan(&name); err != nil {
			trows.Close()
			return nil, err
		}
		existing[name] = true
	}
	if err := trows.Err(); err != nil {
		trows.Close()
		return nil, err
	}
	trows.Close()

	for _, name := range tableNames {
		// Readers do not migrate, so an older schema may lack newer tables.
		if !existing[name] {
			continue
		}
		var n int64
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+name).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", name, err)
		}
		st.Tables = append(st.Tables, TableCount{Name: name, Rows: n})
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, serial, fs_type, label, created_at, last_seen_at FROM volumes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v VolumeInfo
		if err := rows.Scan(&v.ID, &v.Serial, &v.FSType, &v.Label, &v.CreatedAt, &v.LastSeen); err != nil {
			return nil, err
		}
		st.Volumes = append(st.Volumes, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	srows, err := s.db.QueryContext(ctx, `
		SELECT id, volume_id, kind, status, root, started_at, finished_at, duration_ms,
		       files, dirs, unique_alloc_bytes, errors, coverage_pct
		FROM snapshots ORDER BY started_at DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var sn SnapshotInfo
		var finished sql.NullInt64
		if err := srows.Scan(&sn.ID, &sn.VolumeID, &sn.Kind, &sn.Status, &sn.Root, &sn.StartedAt,
			&finished, &sn.DurationMS, &sn.Files, &sn.Dirs, &sn.UniqueAllocBytes, &sn.Errors,
			&sn.CoveragePct); err != nil {
			return nil, err
		}
		if finished.Valid {
			sn.FinishedAt = &finished.Int64
		}
		st.RecentSnapshots = append(st.RecentSnapshots, sn)
	}
	return st, srows.Err()
}

// EnsureVolume returns the row id for serial, inserting or refreshing metadata.
func (s *Store) EnsureVolume(ctx context.Context, serial uint32, fsType, label string) (int64, error) {
	now := time.Now().UnixMilli()
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO volumes (serial, fs_type, label, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(serial) DO UPDATE SET
			fs_type = excluded.fs_type,
			label = excluded.label,
			last_seen_at = excluded.last_seen_at
		RETURNING id`, int64(serial), fsType, label, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("ensure volume 0x%08X: %w", serial, err)
	}
	return id, nil
}
