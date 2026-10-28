package server

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"fattrace/internal/diff"
	"fattrace/internal/store"
)

// TestRealDBLatencyP95 is the M5 performance gate. It is skipped unless
// FATTRACE_DB points at a populated database, because the timing depends on
// real snapshot size and the host disk.
func TestRealDBLatencyP95(t *testing.T) {
	db := os.Getenv("FATTRACE_DB")
	if db == "" {
		t.Skip("set FATTRACE_DB to a real database to run the latency gate")
	}
	st, err := store.OpenReader(db)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	pair, err := diff.Run(ctx, st, diff.Options{TopN: 20})
	if err != nil {
		t.Fatalf("resolve default pair: %v", err)
	}
	listing, err := st.BrowseDir(ctx, pair.ToSnapshot.ID, 0, 500, 200)
	if err != nil {
		t.Fatalf("browse root: %v", err)
	}

	srv, err := New(st, Options{Token: testToken, TopN: 20})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	client := ts.Client()
	get := func(path string) time.Duration {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(tokenHeader, testToken)
		started := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		elapsed := time.Since(started)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		return elapsed
	}

	pairPath := fmt.Sprintf("/api/diff?from=%d&to=%d", pair.FromSnapshot.ID, pair.ToSnapshot.ID)
	endpoints := []struct{ name, path string }{
		{"status", "/api/status"},
		{"snapshots", "/api/snapshots?limit=50"},
		{"trend", fmt.Sprintf("/api/trend?volume=%d&limit=12", pair.VolumeID)},
		{"dir-root", fmt.Sprintf("/api/dir?snapshot=%d", pair.ToSnapshot.ID)},
		{"diff-warm", pairPath},
	}
	if len(listing.Dirs) > 0 {
		endpoints = append(endpoints,
			struct{ name, path string }{
				"dir-deep",
				fmt.Sprintf("/api/dir?snapshot=%d&frn=%d", pair.ToSnapshot.ID, listing.Dirs[0].FRN),
			})
	}

	cold := get(pairPath)
	t.Logf("cold diff call: %s", cold.Round(time.Millisecond))
	for _, ep := range endpoints {
		get(ep.path)
	}

	const rounds = 30
	for _, ep := range endpoints {
		samples := make([]time.Duration, rounds)
		for i := range samples {
			samples[i] = get(ep.path)
		}
		p95 := percentile(samples, 95)
		maxDur := samples[0]
		for _, s := range samples {
			if s > maxDur {
				maxDur = s
			}
		}
		t.Logf("%-10s p95=%s max=%s", ep.name, p95.Round(time.Millisecond), maxDur.Round(time.Millisecond))
		if p95 > 200*time.Millisecond {
			t.Errorf("%s p95 %s exceeds the 200ms gate", ep.name, p95.Round(time.Millisecond))
		}
	}
}

func percentile(samples []time.Duration, pct float64) time.Duration {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(pct/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
