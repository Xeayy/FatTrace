//go:build windows

package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Xeayy/FatTrace/internal/store"
)

type weeklyFixture struct {
	st  *store.Store
	raw *sql.DB
	vol int64
}

func newWeeklyFixture(t *testing.T, serial uint32) *weeklyFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "weekly.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	raw, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		st.Close()
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() {
		raw.Close()
		st.Close()
	})
	vol, err := st.EnsureVolume(context.Background(), serial, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	return &weeklyFixture{st: st, raw: raw, vol: vol}
}

func (f *weeklyFixture) snapshot(t *testing.T, kind string, at time.Time) int64 {
	t.Helper()
	res, err := f.raw.Exec(`INSERT INTO snapshots
		(volume_id, kind, status, root, started_at, finished_at, files, dirs)
		VALUES (?, ?, 'complete', ?, ?, ?, 0, 0)`,
		f.vol, kind, `D:\`, at.UnixMilli(), at.UnixMilli())
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("snapshot id: %v", err)
	}
	return id
}

func TestWeeklyReportNameDistinguishesRoots(t *testing.T) {
	at := time.Date(2026, 11, 8, 4, 0, 0, 0, time.Local)
	rootName := weeklyReportName(1, `D:\`, at)
	workName := weeklyReportName(1, `D:\Work`, at)
	otherName := weeklyReportName(1, `D:\Other`, at)
	if rootName == workName || workName == otherName || rootName == otherName {
		t.Fatalf("report names collide: %q %q %q", rootName, workName, otherName)
	}
	if weeklyReportName(1, `D:\Work`, at) != workName {
		t.Fatal("report name is not stable")
	}
	for _, name := range []string{rootName, workName, otherName} {
		if !strings.HasPrefix(name, "weekly-1-2026-11-08-") || !strings.HasSuffix(name, ".html") {
			t.Fatalf("unexpected report name %q", name)
		}
		if strings.ContainsAny(name, `\/:*?"<>|`) {
			t.Fatalf("report name %q contains invalid filename characters", name)
		}
	}
}

func TestWeeklyPairPicksTwoNewestWeekly(t *testing.T) {
	f := newWeeklyFixture(t, 0xD101)
	now := time.Date(2026, 11, 8, 4, 0, 0, 0, time.Local)
	f.snapshot(t, "weekly", now.AddDate(0, 0, -14))
	fromID := f.snapshot(t, "weekly", now.AddDate(0, 0, -7))
	toID := f.snapshot(t, "weekly", now)
	f.snapshot(t, "daily", now.AddDate(0, 0, -3))

	from, to, reason, err := weeklyPair(context.Background(), f.st, `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Fatalf("unexpected skip: %s", reason)
	}
	if from.ID != fromID || to.ID != toID {
		t.Fatalf("pair = #%d → #%d, want #%d → #%d", from.ID, to.ID, fromID, toID)
	}
}

func TestWeeklyPairSkipsCloseSnapshots(t *testing.T) {
	f := newWeeklyFixture(t, 0xD102)
	now := time.Date(2026, 11, 8, 4, 0, 0, 0, time.Local)
	f.snapshot(t, "weekly", now.Add(-time.Hour))
	f.snapshot(t, "weekly", now)

	from, to, reason, err := weeklyPair(context.Background(), f.st, `D:\`)
	if err != nil {
		t.Fatal(err)
	}
	if from.ID == 0 || to.ID == 0 || !strings.Contains(reason, "days apart") {
		t.Fatalf("pair = #%d → #%d reason %q", from.ID, to.ID, reason)
	}
}
