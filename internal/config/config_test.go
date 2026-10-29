package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValidate(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	if len(cfg.Schedule.Roots) != 0 {
		t.Fatalf("defaults must not schedule any root: %v", cfg.Schedule.Roots)
	}
	if cfg.Retention.AutoPrune {
		t.Fatal("auto_prune must default to false")
	}
	if cfg.Retention.DailyFileDays != 7 || cfg.Retention.WeeklyFileWeeks != 3 {
		t.Fatalf("file retention defaults = %d days / %d weeks", cfg.Retention.DailyFileDays, cfg.Retention.WeeklyFileWeeks)
	}
}

func TestValidateRootsTrimDedupeClean(t *testing.T) {
	cfg := Defaults()
	cfg.Schedule.Roots = []string{`D:\`, " d:\\ ", `D:\Data\`, ""}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{`D:\`, `D:\Data`}
	if len(cfg.Schedule.Roots) != len(want) {
		t.Fatalf("roots = %v, want %v", cfg.Schedule.Roots, want)
	}
	for i := range want {
		if cfg.Schedule.Roots[i] != want[i] {
			t.Fatalf("roots = %v, want %v", cfg.Schedule.Roots, want)
		}
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"relative root", func(c *Config) { c.Schedule.Roots = []string{"data"} }},
		{"daily hour", func(c *Config) { c.Schedule.DailyAt = "24:00" }},
		{"daily format", func(c *Config) { c.Schedule.DailyAt = "0300" }},
		{"weekly day", func(c *Config) { c.Schedule.WeeklyAt = "Funday 03:00" }},
		{"weekly format", func(c *Config) { c.Schedule.WeeklyAt = "Sun" }},
		{"negative retention", func(c *Config) { c.Retention.DailySummaryDays = -1 }},
		{"negative daily file retention", func(c *Config) { c.Retention.DailyFileDays = -1 }},
		{"negative weekly file retention", func(c *Config) { c.Retention.WeeklyFileWeeks = -1 }},
		{"negative alert", func(c *Config) { c.Alerts.SilenceHours = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
		})
	}
}

func TestLoadMissingUsesDefaults(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fattrace.db")
	cfg, err := Load(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exists {
		t.Fatal("missing config must report Exists=false")
	}
	if cfg.Path != filepath.Join(filepath.Dir(dbPath), FileName) {
		t.Fatalf("path = %q", cfg.Path)
	}
	if cfg.Schedule.DailyAt != "03:00" || cfg.Alerts.GrowthBytes != 5<<30 {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadUnknownKeyFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte("schedule:\n  daily_at: \"03:00\"\n  dailyatt: \"04:00\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(filepath.Join(dir, "fattrace.db"))
	if err == nil || !strings.Contains(err.Error(), "dailyatt") {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	cfg := Defaults()
	cfg.Schedule.Roots = []string{`D:\`, `E:\Data`}
	cfg.Schedule.DailyAt = "04:30"
	cfg.Retention.FileEventsDays = 14
	cfg.Alerts.GrowthPct = 2.5
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(filepath.Join(dir, "fattrace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Exists {
		t.Fatal("saved config reports Exists=false")
	}
	if got.Schedule.DailyAt != "04:30" || got.Retention.FileEventsDays != 14 || got.Alerts.GrowthPct != 2.5 {
		t.Fatalf("round trip = %+v", got)
	}
	if len(got.Schedule.Roots) != 2 || got.Schedule.Roots[0] != `D:\` || got.Schedule.Roots[1] != `E:\Data` {
		t.Fatalf("roots = %v", got.Schedule.Roots)
	}
}

func TestTemplateIsLoadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(Template([]string{`D:\`})), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(filepath.Join(dir, "fattrace.db"))
	if err != nil {
		t.Fatalf("template is not valid config: %v", err)
	}
	if len(cfg.Schedule.Roots) != 1 || cfg.Schedule.Roots[0] != `D:\` {
		t.Fatalf("template roots = %v", cfg.Schedule.Roots)
	}
	if cfg.Schedule.WeeklyAt != "Sun 04:00" {
		t.Fatalf("template weekly = %q", cfg.Schedule.WeeklyAt)
	}
	if cfg.Retention.DailyFileDays != 7 || cfg.Retention.WeeklyFileWeeks != 3 {
		t.Fatalf("template file retention = %d / %d", cfg.Retention.DailyFileDays, cfg.Retention.WeeklyFileWeeks)
	}
}

func TestNextDaily(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, loc)
	next, err := NextDaily(now, "03:00")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 30, 3, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	next, err = NextDaily(time.Date(2026, 10, 29, 2, 0, 0, 0, loc), "03:00")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 29, 3, 0, 0, 0, loc); !next.Equal(want) {
		t.Fatalf("same-day next = %v, want %v", next, want)
	}
}

func TestNextWeekly(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, loc)
	next, err := NextWeekly(now, "Sun 04:00")
	if err != nil {
		t.Fatal(err)
	}
	if next.Weekday() != time.Sunday || next.Hour() != 4 || next.Minute() != 0 {
		t.Fatalf("next = %v", next)
	}
	if !next.After(now) || next.Sub(now) > 7*24*time.Hour {
		t.Fatalf("next = %v is outside the next week", next)
	}
	onSunday := time.Date(next.Year(), next.Month(), next.Day(), 3, 0, 0, 0, loc)
	same, err := NextWeekly(onSunday, "Sun 04:00")
	if err != nil {
		t.Fatal(err)
	}
	if !same.Equal(time.Date(onSunday.Year(), onSunday.Month(), onSunday.Day(), 4, 0, 0, 0, loc)) {
		t.Fatalf("same-day weekly = %v", same)
	}
	after := time.Date(next.Year(), next.Month(), next.Day(), 5, 0, 0, 0, loc)
	weekLater, err := NextWeekly(after, "Sun 04:00")
	if err != nil {
		t.Fatal(err)
	}
	if weekLater.Weekday() != time.Sunday || weekLater.Sub(after) < 6*24*time.Hour {
		t.Fatalf("following week = %v (from %v)", weekLater, after)
	}
}
