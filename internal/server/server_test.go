package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fattrace/internal/diff"
	"fattrace/internal/model"
	"fattrace/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef"

func seedSnapshot(t *testing.T, st *store.Store, vol int64, root, status string, used int64, recs []model.FileRecord) int64 {
	t.Helper()
	ctx := context.Background()
	w, err := st.BeginScan(ctx, vol)
	if err != nil {
		t.Fatalf("begin scan: %v", err)
	}
	for _, r := range recs {
		if err := w.Add(ctx, r); err != nil {
			t.Fatalf("stage: %v", err)
		}
	}
	if _, err := w.Commit(ctx); err != nil {
		t.Fatalf("commit staging: %v", err)
	}
	agg, err := st.AggregateDirs(ctx, vol, nil)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	id, err := st.BeginSnapshot(ctx, vol, "weekly", root)
	if err != nil {
		t.Fatalf("begin snapshot: %v", err)
	}
	if status != "complete" {
		if err := st.FinishSnapshot(ctx, id, status, model.ScanStats{}); err != nil {
			t.Fatalf("finish snapshot: %v", err)
		}
		if err := st.ClearScan(ctx, vol); err != nil {
			t.Fatalf("clear staging: %v", err)
		}
		return id
	}
	var files, dirs, alloc int64
	for _, r := range recs {
		if r.IsDir {
			dirs++
		} else {
			files++
			alloc += r.AllocSize
		}
	}
	err = st.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID: id, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{
			Files: files, Dirs: dirs, Bytes: alloc, AllocBytes: alloc,
			UniqueBytes: alloc, UniqueAllocBytes: alloc,
			VolumeUsed: used, CoveragePct: 99.9,
		},
		Dirs: agg,
	})
	if err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	return id
}

func TestRejectsForeignHostAndOrigin(t *testing.T) {
	f := newFixture(t)

	send := func(mutate func(*http.Request)) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		mutate(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	cases := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{"foreign host", func(r *http.Request) { r.Host = "evil.example" }, http.StatusMisdirectedRequest},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }, http.StatusForbidden},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		{"wrong port", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:1") }, http.StatusForbidden},
		{"same origin", func(r *http.Request) { r.Header.Set("Origin", f.ts.URL) }, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(tc.mutate)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if resp.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("server must not emit CORS headers")
			}
		})
	}
}

type testFixture struct {
	store *store.Store
	ts    *httptest.Server
	vol   int64
	from  int64
	to    int64
	sub   int64
	part  int64
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0x5151, "NTFS", "test")
	if err != nil {
		t.Fatal(err)
	}
	base := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "sub", IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 1000},
		{FRN: 11, ParentFRN: 2, Name: "inner.bin", Size: 50, AllocSize: 4096},
	}
	from := seedSnapshot(t, st, vol, `D:\`, "complete", 100_000, base)
	withNew := append(append([]model.FileRecord{}, base...),
		model.FileRecord{FRN: 12, ParentFRN: 1, Name: "new.bin", Size: 100, AllocSize: 2000})
	to := seedSnapshot(t, st, vol, `D:\`, "complete", 103_000, withNew)
	sub := seedSnapshot(t, st, vol, `D:\sub`, "complete", 0, []model.FileRecord{
		{FRN: 2, ParentFRN: 0, Name: `D:\sub`, IsDir: true},
		{FRN: 11, ParentFRN: 2, Name: "inner.bin", Size: 50, AllocSize: 4096},
	})
	part := seedSnapshot(t, st, vol, `D:\`, "partial", 0, base)

	srv, err := New(st, Options{Token: testToken, TopN: 5})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &testFixture{store: st, ts: ts, vol: vol, from: from, to: to, sub: sub, part: part}
}

func doGet(t *testing.T, f *testFixture, path, token string, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	resp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestAuthTokenRequired(t *testing.T) {
	f := newFixture(t)

	var e map[string]string
	if code := doGet(t, f, "/api/status", "", &e); code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", code)
	}
	if e["error"] == "" {
		t.Fatalf("missing error body: %v", e)
	}
	if code := doGet(t, f, "/api/status", "deadbeef", nil); code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", code)
	}
	if code := doGet(t, f, "/api/status", testToken, nil); code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", code)
	}

	resp, err := http.Get(f.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("index = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("missing CSP: %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}

	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/status", nil)
	req.Header.Set(tokenHeader, testToken)
	presp, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	presp.Body.Close()
	if presp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/status = %d, want 405", presp.StatusCode)
	}
}

func TestAPIStatusSnapshotsTrend(t *testing.T) {
	f := newFixture(t)

	var st store.Status
	if code := doGet(t, f, "/api/status", testToken, &st); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if st.SchemaVersion == 0 || len(st.Volumes) != 1 || len(st.Tables) == 0 {
		t.Fatalf("status payload = %+v", st)
	}
	if st.Volumes[0].ID != f.vol || st.Volumes[0].FSType != "NTFS" {
		t.Fatalf("volume = %+v", st.Volumes[0])
	}

	var list struct {
		Snapshots []store.SnapshotInfo `json:"snapshots"`
	}
	if code := doGet(t, f, "/api/snapshots?limit=2", testToken, &list); code != http.StatusOK {
		t.Fatalf("snapshots = %d", code)
	}
	if len(list.Snapshots) != 2 || list.Snapshots[0].ID != f.part || list.Snapshots[1].ID != f.sub {
		t.Fatalf("snapshot order = %+v", list.Snapshots)
	}

	var trend struct {
		VolumeID int64                `json:"volume_id"`
		Points   []store.SnapshotInfo `json:"points"`
	}
	if code := doGet(t, f, "/api/trend?volume="+itoa(f.vol), testToken, &trend); code != http.StatusOK {
		t.Fatalf("trend = %d", code)
	}
	if trend.VolumeID != f.vol || len(trend.Points) != 2 ||
		trend.Points[0].ID != f.from || trend.Points[1].ID != f.to {
		t.Fatalf("trend = %+v (subdir #%d and partial #%d must be excluded)", trend, f.sub, f.part)
	}
}

func TestAPIDiffMatchesService(t *testing.T) {
	f := newFixture(t)
	path := "/api/diff?from=" + itoa(f.from) + "&to=" + itoa(f.to) + "&top=5"

	var got model.DiffResult
	if code := doGet(t, f, path, testToken, &got); code != http.StatusOK {
		t.Fatalf("diff = %d", code)
	}
	want, err := diff.Run(context.Background(), f.store, diff.Options{FromID: f.from, ToID: f.to, TopN: 5})
	if err != nil {
		t.Fatalf("service diff: %v", err)
	}
	if got.FromSnapshot.ID != f.from || got.ToSnapshot.ID != f.to || got.Root != `D:\` {
		t.Fatalf("diff pair = %+v", got)
	}
	if got.Totals != want.Totals {
		t.Fatalf("totals = %+v, want %+v", got.Totals, want.Totals)
	}
	if len(got.TopGrowth) != len(want.TopGrowth) || got.TopGrowth[0].Name != want.TopGrowth[0].Name {
		t.Fatalf("top growth = %+v, want %+v", got.TopGrowth, want.TopGrowth)
	}
	if got.Check.RootDeltaAlloc != want.Check.RootDeltaAlloc || got.Check.OK != want.Check.OK {
		t.Fatalf("check = %+v, want %+v", got.Check, want.Check)
	}
	if got.Explanation == nil || got.Explanation.GrowthBytes != 2000 || got.Explanation.UnexplainedTotal != 1 {
		t.Fatalf("explanation = %+v", got.Explanation)
	}

	// A second call is served from the cache and must be byte-for-byte stable.
	var again model.DiffResult
	if code := doGet(t, f, path, testToken, &again); code != http.StatusOK {
		t.Fatalf("cached diff = %d", code)
	}
	first, _ := json.Marshal(got)
	second, _ := json.Marshal(again)
	if string(first) != string(second) {
		t.Fatal("cached diff differs from the first response")
	}

	var e map[string]string
	if code := doGet(t, f, "/api/diff?from=9999&to="+itoa(f.to), testToken, &e); code != http.StatusNotFound {
		t.Fatalf("unknown snapshot = %d, want 404", code)
	}
	if code := doGet(t, f, "/api/diff?top=abc", testToken, &e); code != http.StatusBadRequest {
		t.Fatalf("bad top = %d, want 400", code)
	}
}

func TestDiffCacheInvalidatesWhenRunningScanCompletes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var before model.DiffResult
	if code := doGet(t, f, "/api/diff", testToken, &before); code != http.StatusOK {
		t.Fatalf("diff before scan = %d", code)
	}
	if before.FromSnapshot.ID != f.from || before.ToSnapshot.ID != f.to {
		t.Fatalf("auto pair before scan = %d->%d, want %d->%d",
			before.FromSnapshot.ID, before.ToSnapshot.ID, f.from, f.to)
	}

	// The running row exists before any data is published, and the UI keeps
	// polling /api/diff while the scan runs.
	w, err := f.store.BeginScan(ctx, f.vol)
	if err != nil {
		t.Fatal(err)
	}
	records := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "sub", IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 1000},
		{FRN: 11, ParentFRN: 2, Name: "inner.bin", Size: 50, AllocSize: 4096},
		{FRN: 12, ParentFRN: 1, Name: "new.bin", Size: 100, AllocSize: 2000},
		{FRN: 13, ParentFRN: 1, Name: "fresh.bin", Size: 100, AllocSize: 3000},
	}
	for _, r := range records {
		if err := w.Add(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := f.store.BeginSnapshot(ctx, f.vol, "weekly", `D:\`)
	if err != nil {
		t.Fatal(err)
	}

	var during model.DiffResult
	if code := doGet(t, f, "/api/diff", testToken, &during); code != http.StatusOK {
		t.Fatalf("diff during scan = %d", code)
	}
	if during.ToSnapshot.ID != f.to {
		t.Fatalf("running snapshot must not become the auto-pair target: %+v", during.ToSnapshot)
	}

	agg, err := f.store.AggregateDirs(ctx, f.vol, nil)
	if err != nil {
		t.Fatal(err)
	}
	var files, dirs, alloc int64
	for _, r := range records {
		if r.IsDir {
			dirs++
		} else {
			files++
			alloc += r.AllocSize
		}
	}
	err = f.store.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID: id, VolumeID: f.vol, Status: "complete",
		Stats: model.ScanStats{
			Files: files, Dirs: dirs, Bytes: alloc, AllocBytes: alloc,
			UniqueBytes: alloc, UniqueAllocBytes: alloc,
			VolumeUsed: 107_000, CoveragePct: 99.9,
		},
		Dirs: agg,
	})
	if err != nil {
		t.Fatal(err)
	}

	var after model.DiffResult
	if code := doGet(t, f, "/api/diff", testToken, &after); code != http.StatusOK {
		t.Fatalf("diff after scan = %d", code)
	}
	if after.FromSnapshot.ID != f.to || after.ToSnapshot.ID != id {
		t.Fatalf("auto pair after scan = %d->%d, want %d->%d (stale cache)",
			after.FromSnapshot.ID, after.ToSnapshot.ID, f.to, id)
	}
	if len(after.TopGrowth) == 0 || after.TopGrowth[0].Name != "fresh.bin" {
		t.Fatalf("after-scan growth = %+v, want fresh.bin", after.TopGrowth)
	}
}

func TestAPIRulesAndExplanation(t *testing.T) {
	f := newFixture(t)

	var set struct {
		Rules []struct {
			ID string `json:"id"`
		} `json:"rules"`
		RulesBuiltin int    `json:"rules_builtin"`
		UserPath     string `json:"user_path"`
	}
	if code := doGet(t, f, "/api/rules", testToken, &set); code != http.StatusOK {
		t.Fatalf("rules = %d", code)
	}
	if set.RulesBuiltin < 60 || len(set.Rules) != set.RulesBuiltin || set.UserPath != "" {
		t.Fatalf("rules payload = %d builtin, %d total, user %q", set.RulesBuiltin, len(set.Rules), set.UserPath)
	}

	base := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true},
		{FRN: 2, ParentFRN: 1, Name: "sub", IsDir: true},
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 1000},
	}
	from := seedSnapshot(t, f.store, f.vol, `D:\`, "complete", 200_000, base)
	withLog := append(append([]model.FileRecord{}, base...),
		model.FileRecord{FRN: 14, ParentFRN: 2, Name: "setup.log", Size: 512, AllocSize: 4096})
	to := seedSnapshot(t, f.store, f.vol, `D:\`, "complete", 204_000, withLog)

	var res model.DiffResult
	if code := doGet(t, f, "/api/diff?from="+itoa(from)+"&to="+itoa(to), testToken, &res); code != http.StatusOK {
		t.Fatalf("diff = %d", code)
	}
	ex := res.Explanation
	if ex == nil || ex.GrowthBytes != 4096 || ex.ExplainedBytes != 4096 ||
		ex.UnexplainedBytes != 0 || ex.ExplainedPct != 100 || ex.UnexplainedTotal != 0 {
		t.Fatalf("explanation = %+v", ex)
	}
	if len(ex.Categories) != 1 || ex.Categories[0].Category != "logs" || ex.Categories[0].Bytes != 4096 {
		t.Fatalf("categories = %+v", ex.Categories)
	}
	if len(res.TopGrowth) != 1 || res.TopGrowth[0].Rule == nil || res.TopGrowth[0].Rule.ID != "logs.files" {
		t.Fatalf("top growth rule = %+v", res.TopGrowth)
	}
}

func TestAPIDirDrilldown(t *testing.T) {
	f := newFixture(t)

	var root store.DirListing
	if code := doGet(t, f, "/api/dir?snapshot="+itoa(f.to), testToken, &root); code != http.StatusOK {
		t.Fatalf("root dir = %d", code)
	}
	if root.Dir.FRN != 1 || root.Path != `D:\` || len(root.Breadcrumb) != 1 {
		t.Fatalf("root listing = %+v", root)
	}
	if len(root.Dirs) != 1 || root.Dirs[0].FRN != 2 || len(root.Files) != 2 ||
		root.Files[0].Name != "new.bin" {
		t.Fatalf("root children = dirs %+v files %+v", root.Dirs, root.Files)
	}

	var sub store.DirListing
	if code := doGet(t, f, "/api/dir?snapshot="+itoa(f.to)+"&frn=2", testToken, &sub); code != http.StatusOK {
		t.Fatalf("sub dir = %d", code)
	}
	if sub.Path != `D:\sub` || len(sub.Breadcrumb) != 2 || sub.Breadcrumb[1].FRN != 2 {
		t.Fatalf("sub listing = %+v", sub)
	}
	if len(sub.Files) != 1 || sub.Files[0].Name != "inner.bin" {
		t.Fatalf("sub files = %+v", sub.Files)
	}

	var e map[string]string
	if code := doGet(t, f, "/api/dir?snapshot="+itoa(f.to)+"&frn=999", testToken, &e); code != http.StatusNotFound {
		t.Fatalf("missing frn = %d, want 404", code)
	}
	if code := doGet(t, f, "/api/dir?snapshot=9999", testToken, &e); code != http.StatusNotFound {
		t.Fatalf("missing snapshot = %d, want 404", code)
	}
	if code := doGet(t, f, "/api/dir?snapshot=abc", testToken, &e); code != http.StatusBadRequest {
		t.Fatalf("bad snapshot = %d, want 400", code)
	}
	if code := doGet(t, f, "/api/dir", testToken, &e); code != http.StatusBadRequest {
		t.Fatalf("missing snapshot = %d, want 400", code)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
