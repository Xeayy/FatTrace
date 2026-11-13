package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Xeayy/FatTrace/internal/config"
	"github.com/Xeayy/FatTrace/internal/model"
	"github.com/Xeayy/FatTrace/internal/store"
)

func TestAPIConfigDefaults(t *testing.T) {
	f := newFixture(t)
	var cfg config.Config
	if code := doGet(t, f, "/api/config", testToken, &cfg); code != http.StatusOK {
		t.Fatalf("config = %d", code)
	}
	if cfg.Exists {
		t.Fatal("fixture has no config file but API reports Exists=true")
	}
	if cfg.Schedule.DailyAt != "03:00" || cfg.Retention.DailySummaryDays != 90 {
		t.Fatalf("config = %+v", cfg)
	}
	if filepath.Base(cfg.Path) != config.FileName {
		t.Fatalf("config path = %q", cfg.Path)
	}
	if code := doGet(t, f, "/api/config", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("config without token = %d, want 401", code)
	}
}

func TestAPIConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Defaults()
	cfg.Schedule.Roots = []string{`D:\`}
	cfg.Alerts.GrowthBytes = 1234
	if err := config.Save(filepath.Join(filepath.Dir(path), config.FileName), cfg); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, Options{Token: testToken, TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	f := &testFixture{store: st, ts: ts}

	var got config.Config
	if code := doGet(t, f, "/api/config", testToken, &got); code != http.StatusOK {
		t.Fatalf("config = %d", code)
	}
	if !got.Exists || len(got.Schedule.Roots) != 1 || got.Schedule.Roots[0] != `D:\` || got.Alerts.GrowthBytes != 1234 {
		t.Fatalf("config = %+v", got)
	}
}

func TestAPIDailyAndAlerts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	err := f.store.SaveDailySummary(ctx, model.DailySummary{
		VolumeID: f.vol, Root: `D:\`, Day: "2026-10-28", SnapshotID: f.to,
		FromSnapshotID: f.from, UsedBytes: 200_000, DeltaBytes: 4_000,
		GrowthBytes: 2_000, ExplainedBytes: 1_000, ExplainedPct: 50,
		Files: 5, Errors: 1, CoveragePct: 99.5,
	}, []model.DailyTopDir{
		{VolumeID: f.vol, Day: "2026-10-28", Rank: 1, Path: `D:\sub`, DeltaBytes: 2_000},
		{VolumeID: f.vol, Day: "2026-10-28", Rank: 2, Path: `D:\`, DeltaBytes: 500},
	})
	if err != nil {
		t.Fatal(err)
	}

	var daily struct {
		Days []struct {
			model.DailySummary
			TopDirs []model.DailyTopDir `json:"top_dirs"`
		} `json:"days"`
	}
	if code := doGet(t, f, "/api/daily?limit=10", testToken, &daily); code != http.StatusOK {
		t.Fatalf("daily = %d", code)
	}
	if len(daily.Days) != 1 {
		t.Fatalf("days = %+v", daily.Days)
	}
	d := daily.Days[0]
	if d.Day != "2026-10-28" || d.Root != `D:\` || d.SnapshotID != f.to || d.DeltaBytes != 4_000 {
		t.Fatalf("day = %+v", d)
	}
	if d.ExplainedPct != 50 || d.GrowthBytes != 2_000 || d.CoveragePct != 99.5 {
		t.Fatalf("day explanation = %+v", d)
	}
	if len(d.TopDirs) != 2 || d.TopDirs[0].Path != `D:\sub` || d.TopDirs[0].DeltaBytes != 2_000 {
		t.Fatalf("top dirs = %+v", d.TopDirs)
	}

	var alerts struct {
		CheckedAt int64         `json:"checked_at_ms"`
		Alerts    []model.Alert `json:"alerts"`
	}
	if code := doGet(t, f, "/api/alerts", testToken, &alerts); code != http.StatusOK {
		t.Fatalf("alerts = %d", code)
	}
	if alerts.CheckedAt == 0 || len(alerts.Alerts) != 1 {
		t.Fatalf("alerts = %+v", alerts)
	}
	a := alerts.Alerts[0]
	if a.Key != "growth:"+itoa(f.vol) || a.Day != "2026-10-28" || a.ValueBytes != 4_000 {
		t.Fatalf("alert = %+v", a)
	}
	if a.Triggered {
		t.Fatalf("4 KiB growth must not cross the default thresholds: %+v", a)
	}
	if code := doGet(t, f, "/api/daily", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("daily without token = %d, want 401", code)
	}
	if code := doGet(t, f, "/api/alerts?x=1", "deadbeef", nil); code != http.StatusUnauthorized {
		t.Fatalf("alerts with a bad token = %d, want 401", code)
	}
}
