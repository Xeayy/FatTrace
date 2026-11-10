// Package config loads the local FatTrace settings file.
//
// The file lives next to the database (<db_dir>/config.yaml) and is the only
// configuration entry point: the read-only server displays it, while schedule,
// maintain, daily, alerts and prune read it. Missing files fall back to
// defaults; malformed files fail loudly so a typo never silently disables an
// alert or retention rule.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const FileName = "config.yaml"

// Config is the complete local configuration.
type Config struct {
	Path      string    `yaml:"-" json:"path"`
	Exists    bool      `yaml:"-" json:"exists"`
	Schedule  Schedule  `yaml:"schedule" json:"schedule"`
	Retention Retention `yaml:"retention" json:"retention"`
	Alerts    Alerts    `yaml:"alerts" json:"alerts"`
}

// Schedule describes the Task Scheduler jobs.
type Schedule struct {
	Roots    []string `yaml:"roots" json:"roots"`
	DailyAt  string   `yaml:"daily_at" json:"daily_at"`
	WeeklyAt string   `yaml:"weekly_at" json:"weekly_at"`
	// ACOnly keeps planned scans from starting on battery power; they wait
	// until the machine is plugged in. A scan already running is allowed to
	// finish when power is unplugged. Manual fat snap is never affected.
	ACOnly bool `yaml:"ac_only" json:"ac_only"`
}

// Retention sets how long FatTrace keeps its own history. It never applies to
// scanned files.
type Retention struct {
	DailySnapshotsDays  int `yaml:"daily_snapshots_days" json:"daily_snapshots_days"`
	WeeklySnapshotsDays int `yaml:"weekly_snapshots_days" json:"weekly_snapshots_days"`
	// DailyFileDays and WeeklyFileWeeks control how long snap_files rows are
	// kept. Snapshots and snap_dirs stay for the longer snapshot retention, so
	// old points remain browsable at directory level without the full file list.
	DailyFileDays    int  `yaml:"daily_file_days" json:"daily_file_days"`
	WeeklyFileWeeks  int  `yaml:"weekly_file_weeks" json:"weekly_file_weeks"`
	DailySummaryDays int  `yaml:"daily_summary_days" json:"daily_summary_days"`
	FileEventsDays   int  `yaml:"file_events_days" json:"file_events_days"`
	AutoPrune        bool `yaml:"auto_prune" json:"auto_prune"`
}

// Alerts configures growth warnings. A zero threshold disables that dimension.
type Alerts struct {
	GrowthBytes int64   `yaml:"growth_bytes" json:"growth_bytes"`
	GrowthPct   float64 `yaml:"growth_pct" json:"growth_pct"`
	// GrowthMultiple flags a spike relative to the median positive net growth
	// of the previous up to 14 days. It stays disabled at 0.
	GrowthMultiple float64 `yaml:"growth_multiple" json:"growth_multiple"`
	// GrowthMultipleFloorBytes prevents tiny baselines from producing noisy
	// multiples; the day must also grow by at least this much.
	GrowthMultipleFloorBytes int64   `yaml:"growth_multiple_floor_bytes" json:"growth_multiple_floor_bytes"`
	SilenceHours             float64 `yaml:"silence_hours" json:"silence_hours"`
	// StaleHours flags a volume whose newest complete snapshot is older than
	// this. Zero disables the check at the call site; health falls back to 36.
	StaleHours float64 `yaml:"stale_hours" json:"stale_hours"`
}

// Defaults returns the built-in configuration. Roots stay empty: scans are
// only scheduled after the user names the volumes explicitly.
func Defaults() Config {
	return Config{
		Schedule: Schedule{
			Roots:    []string{},
			DailyAt:  "03:00",
			WeeklyAt: "Sun 04:00",
			ACOnly:   true,
		},
		Retention: Retention{
			DailySnapshotsDays:  30,
			WeeklySnapshotsDays: 365,
			DailyFileDays:       7,
			WeeklyFileWeeks:     3,
			DailySummaryDays:    90,
			FileEventsDays:      56,
			AutoPrune:           false,
		},
		Alerts: Alerts{
			GrowthBytes:              5 << 30,
			GrowthPct:                5,
			GrowthMultiple:           0,
			GrowthMultipleFloorBytes: 1 << 30,
			SilenceHours:             24,
			StaleHours:               36,
		},
	}
}

// PathFor returns <db_dir>/config.yaml.
func PathFor(dbPath string) (string, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(abs), FileName), nil
}

// Load reads the config next to dbPath. A missing file yields defaults with
// Exists=false; unknown keys and invalid values are errors.
func Load(dbPath string) (Config, error) {
	path, err := PathFor(dbPath)
	if err != nil {
		return Config{}, err
	}
	cfg := Defaults()
	cfg.Path = path
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Exists = true
	cfg.Path = path
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks values that would otherwise fail silently at runtime.
func (c *Config) Validate() error {
	if err := ValidateDaily(c.Schedule.DailyAt); err != nil {
		return fmt.Errorf("schedule.daily_at: %w", err)
	}
	if _, _, _, err := ParseWeekly(c.Schedule.WeeklyAt); err != nil {
		return fmt.Errorf("schedule.weekly_at: %w", err)
	}
	seen := make(map[string]bool, len(c.Schedule.Roots))
	roots := c.Schedule.Roots[:0]
	for _, r := range c.Schedule.Roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) {
			return fmt.Errorf("schedule.roots: %q is not an absolute path", r)
		}
		key := strings.ToLower(filepath.Clean(r))
		if seen[key] {
			continue
		}
		seen[key] = true
		roots = append(roots, filepath.Clean(r))
	}
	c.Schedule.Roots = roots

	if c.Retention.DailySnapshotsDays < 0 || c.Retention.WeeklySnapshotsDays < 0 ||
		c.Retention.DailyFileDays < 0 || c.Retention.WeeklyFileWeeks < 0 ||
		c.Retention.DailySummaryDays < 0 || c.Retention.FileEventsDays < 0 {
		return errors.New("retention values must be >= 0")
	}
	if c.Alerts.GrowthBytes < 0 || c.Alerts.GrowthPct < 0 ||
		c.Alerts.GrowthMultiple < 0 || c.Alerts.GrowthMultipleFloorBytes < 0 ||
		c.Alerts.SilenceHours < 0 {
		return errors.New("alerts values must be >= 0")
	}
	return nil
}

// ValidateDaily parses an HH:MM local time.
func ValidateDaily(v string) error {
	_, _, err := parseClock(v)
	return err
}

func parseClock(v string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(v), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", v)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", v)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", v)
	}
	return h, m, nil
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// ParseWeekly parses "Mon HH:MM" (weekday name + local clock).
func ParseWeekly(v string) (time.Weekday, int, int, error) {
	fields := strings.Fields(v)
	if len(fields) != 2 {
		return 0, 0, 0, fmt.Errorf("expected \"Mon HH:MM\", got %q", v)
	}
	wd, ok := weekdays[strings.ToLower(fields[0])]
	if !ok {
		return 0, 0, 0, fmt.Errorf("unknown weekday %q", fields[0])
	}
	h, m, err := parseClock(fields[1])
	if err != nil {
		return 0, 0, 0, err
	}
	return wd, h, m, nil
}

// NextDaily returns the next local occurrence of HH:MM after now.
func NextDaily(now time.Time, clock string) (time.Time, error) {
	h, m, err := parseClock(clock)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next, nil
}

// NextWeekly returns the next local occurrence of "Mon HH:MM" after now.
func NextWeekly(now time.Time, spec string) (time.Time, error) {
	wd, h, m, err := ParseWeekly(spec)
	if err != nil {
		return time.Time{}, err
	}
	next := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	delta := (int(wd) - int(next.Weekday()) + 7) % 7
	next = next.AddDate(0, 0, delta)
	if !next.After(now) {
		next = next.AddDate(0, 0, 7)
	}
	return next, nil
}

// Marshal renders the canonical YAML form used by "fat config show".
func (c Config) Marshal() ([]byte, error) {
	out := c
	out.Path = ""
	out.Exists = false
	return yaml.Marshal(out)
}

// Save writes the config atomically. The file contains no secrets, but it is
// still created owner-only to avoid leaking paths to other local users.
func Save(path string, c Config) error {
	data, err := c.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Template is written by "fat config init". It documents every field.
func Template(roots []string) string {
	var b strings.Builder
	b.WriteString("# FatTrace local configuration\n")
	b.WriteString("# The read-only UI only displays these values; scans run from Task Scheduler.\n")
	b.WriteString("# All retention limits apply to FatTrace's own database history, never to scanned files.\n\n")
	b.WriteString("schedule:\n")
	if len(roots) == 0 {
		b.WriteString("  roots: []            # absolute scan roots, e.g. [\"D:\\\\\"]\n")
	} else {
		b.WriteString("  roots:\n")
		for _, r := range roots {
			fmt.Fprintf(&b, "    - %q\n", r)
		}
	}
	b.WriteString("  daily_at: \"03:00\"    # daily snapshot time (HH:MM, local)\n")
	b.WriteString("  weekly_at: \"Sun 04:00\"  # weekly snapshot time (weekday HH:MM, local)\n")
	b.WriteString("  ac_only: true         # scheduled scans wait for AC power; manual snap is unaffected\n\n")
	b.WriteString("retention:\n")
	b.WriteString("  daily_snapshots_days: 30\n")
	b.WriteString("  weekly_snapshots_days: 365\n")
	b.WriteString("  daily_file_days: 7    # daily snap_files rows; 0 disables file-row pruning\n")
	b.WriteString("  weekly_file_weeks: 3  # weekly snap_files rows; directories stay longer\n")
	b.WriteString("  daily_summary_days: 90\n")
	b.WriteString("  file_events_days: 56\n")
	b.WriteString("  auto_prune: false     # maintain tasks delete expired history when true\n\n")
	b.WriteString("alerts:\n")
	b.WriteString("  growth_bytes: 5368709120  # per-day net growth threshold; 0 disables\n")
	b.WriteString("  growth_pct: 5             # percent of used space; 0 disables\n")
	b.WriteString("  growth_multiple: 0        # spike vs median of prior 14 days; 0 disables\n")
	b.WriteString("  growth_multiple_floor_bytes: 1073741824  # ignore multiples below this growth\n")
	b.WriteString("  silence_hours: 24         # suppress repeats for this long after firing\n")
	b.WriteString("  stale_hours: 36           # warn when the newest complete snapshot is older\n")
	return b.String()
}
