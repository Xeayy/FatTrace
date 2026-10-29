package alerts

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fattrace/internal/config"
	"fattrace/internal/model"
	"fattrace/internal/store"
)

const (
	mib = int64(1 << 20)
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedDay(t *testing.T, st *store.Store, vol int64, day string, used, delta int64) {
	t.Helper()
	err := st.SaveDailySummary(context.Background(), model.DailySummary{
		VolumeID: vol, Root: `D:\`, Day: day, UsedBytes: used, DeltaBytes: delta,
	}, nil)
	if err != nil {
		t.Fatalf("seed %s: %v", day, err)
	}
}

func TestFiresOnceThenSilences(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0xA1, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	seedDay(t, st, vol, "2026-10-28", 100*mib, 2*mib)
	cfg := config.Alerts{GrowthBytes: mib, GrowthPct: 5, SilenceHours: 24}
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

	rep, err := Evaluate(ctx, st, cfg, Options{Now: t0, Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Alerts) != 1 {
		t.Fatalf("alerts = %+v", rep.Alerts)
	}
	a := rep.Alerts[0]
	if !a.Triggered || !a.Fired || a.Muted || a.Hits != 1 {
		t.Fatalf("first evaluation = %+v", a)
	}
	if a.Key != "growth:"+itoa(vol) {
		t.Fatalf("key = %q", a.Key)
	}
	if !strings.Contains(a.Message, `D:\`) || !strings.Contains(a.Message, "2.00 MiB") {
		t.Fatalf("message = %q", a.Message)
	}

	states, err := st.AlertStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Key != a.Key {
		t.Fatalf("persisted states = %+v (regression: key must not be empty)", states)
	}
	if states[0].Hits != 1 || states[0].MutedUntil != t0.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("state = %+v", states[0])
	}

	rep, err = Evaluate(ctx, st, cfg, Options{Now: t0.Add(time.Hour), Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if a = rep.Alerts[0]; a.Fired || !a.Muted || a.Hits != 1 {
		t.Fatalf("second evaluation = %+v", a)
	}
	if a.MutedUntil != t0.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("mute window moved: %+v", a)
	}

	rep, err = Evaluate(ctx, st, cfg, Options{Now: t0.Add(25 * time.Hour), Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if a = rep.Alerts[0]; !a.Fired || a.Muted || a.Hits != 2 {
		t.Fatalf("after silence = %+v", a)
	}
	if a.MutedUntil != t0.Add(49*time.Hour).UnixMilli() {
		t.Fatalf("new mute window = %+v", a)
	}
}

func TestPreviewDoesNotWrite(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0xA2, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	seedDay(t, st, vol, "2026-10-28", 100*mib, 2*mib)
	cfg := config.Alerts{GrowthBytes: mib, SilenceHours: 1}
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

	rep, err := Evaluate(ctx, st, cfg, Options{Now: t0, Persist: false})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Alerts[0].Fired {
		t.Fatalf("preview = %+v", rep.Alerts[0])
	}
	states, err := st.AlertStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("preview wrote state: %+v", states)
	}
}

func TestThresholdDimensions(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0xA3, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	seedDay(t, st, vol, "2026-10-28", 100*mib, 2*mib)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)

	cases := []struct {
		name string
		cfg  config.Alerts
		want bool
	}{
		{"bytes only", config.Alerts{GrowthBytes: mib, SilenceHours: 1}, true},
		{"bytes too high", config.Alerts{GrowthBytes: 3 * mib, SilenceHours: 1}, false},
		{"pct too high", config.Alerts{GrowthPct: 5, SilenceHours: 1}, false},
		{"pct hit", config.Alerts{GrowthPct: 1, SilenceHours: 1}, true},
		{"negative growth", config.Alerts{GrowthBytes: -1, GrowthPct: -1, SilenceHours: 1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Evaluate(ctx, st, tc.cfg, Options{Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if tc.cfg.GrowthBytes == 0 && tc.cfg.GrowthPct == 0 {
				if len(rep.Alerts) != 0 {
					t.Fatalf("disabled alerts returned %+v", rep.Alerts)
				}
				return
			}
			if len(rep.Alerts) != 1 {
				t.Fatalf("alerts = %+v", rep.Alerts)
			}
			if rep.Alerts[0].Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v (%+v)", rep.Alerts[0].Triggered, tc.want, rep.Alerts[0])
			}
		})
	}

	rep, err := Evaluate(ctx, st, config.Alerts{}, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Alerts) != 0 {
		t.Fatalf("zero thresholds returned %+v", rep.Alerts)
	}
}

func TestMuteAndClear(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0xA4, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	seedDay(t, st, vol, "2026-10-28", 100*mib, 2*mib)
	cfg := config.Alerts{GrowthBytes: mib, SilenceHours: 1}
	t0 := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	key := "growth:" + itoa(vol)

	if _, err := Evaluate(ctx, st, cfg, Options{Now: t0, Persist: true}); err != nil {
		t.Fatal(err)
	}
	if err := ClearMutes(ctx, st, []string{key}); err != nil {
		t.Fatal(err)
	}
	rep, err := Evaluate(ctx, st, cfg, Options{Now: t0.Add(time.Minute), Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Alerts[0].Fired || rep.Alerts[0].Hits != 2 {
		t.Fatalf("after clear = %+v", rep.Alerts[0])
	}

	if err := SetMute(ctx, st, []string{key}, t0.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rep, err = Evaluate(ctx, st, cfg, Options{Now: t0.Add(2 * time.Hour), Persist: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Alerts[0].Fired || !rep.Alerts[0].Muted || rep.Alerts[0].Hits != 2 {
		t.Fatalf("after manual mute = %+v", rep.Alerts[0])
	}
	if rep.Alerts[0].MutedUntil != t0.Add(48*time.Hour).UnixMilli() {
		t.Fatalf("muted until = %d", rep.Alerts[0].MutedUntil)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
