package report

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"fattrace/internal/model"
	"fattrace/internal/store"
)

var dataRE = regexp.MustCompile(`(?s)<script id="report-data" type="application/json">(.*?)</script>`)

func sampleDiff() model.DiffResult {
	return model.DiffResult{
		Root: `D:\`,
		FromSnapshot: model.SnapshotRef{
			ID: 1, Kind: "weekly", Files: 10, Dirs: 3, UniqueAllocBytes: 4000,
			CoveragePct: 99.5, StartedAtMS: 1_700_000_000_000,
		},
		ToSnapshot: model.SnapshotRef{
			ID: 2, Kind: "weekly", Files: 11, Dirs: 3, UniqueAllocBytes: 8096,
			CoveragePct: 78.73, StartedAtMS: 1_700_000_100_000,
		},
		Totals: model.DiffTotals{
			FilesAdded: 1, BytesAdded: 100, AllocAdded: 4096,
			DeltaBytes: 100, DeltaAlloc: 10_500,
		},
		TopGrowth: []model.FileChange{{
			FRN: 1, Kind: model.ChangeAdded,
			Name: `x</script><script>alert(1)</script>.bin`, Path: `D:\x.bin`,
			SizeAfter: 100, AllocAfter: 4096, DeltaBytes: 100, DeltaAlloc: 4096,
		}},
		TopDirs: []model.DirChange{
			{FRN: 10, Name: "FatTraceDemo", Path: `D:\FatTraceDemo`, DeltaAlloc: 10_000, FilesAfter: 5},
			{FRN: 11, Name: "cache", Path: `D:\FatTraceDemo\.cache`, DeltaAlloc: 9_000, FilesAfter: 2},
			{FRN: 12, Name: "Data", Path: `D:\Data`, DeltaAlloc: 500, FilesAfter: 1, UnknownAfter: true},
		},
		Check: model.VolumeCheck{
			OK: true, RootDeltaAlloc: 10_500, DiskDeltaAlloc: 10_500, UnknownDirs: 1,
		},
		Warnings: []string{"coverage is low"},
	}
}

func parseData(t *testing.T, page []byte) Data {
	t.Helper()
	m := dataRE.FindSubmatch(page)
	if m == nil {
		t.Fatal("report data block not found")
	}
	var d Data
	if err := json.Unmarshal(m[1], &d); err != nil {
		t.Fatalf("report data is not valid JSON: %v", err)
	}
	return d
}

func TestBuildSingleFileReport(t *testing.T) {
	page, err := Build(sampleDiff(),
		[]ErrorRow{{Path: `D:\secret`, Op: "open", Msg: "access denied", Hits: 2}},
		time.UnixMilli(1_700_000_200_000))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	html := string(page)
	for _, bad := range []string{"<script src", "<link ", "http://", "https://"} {
		if strings.Contains(html, bad) {
			t.Fatalf("report references an external resource: %q", bad)
		}
	}
	if strings.Contains(html, "</script><script>alert(1)</script>") {
		t.Fatal("a file name escaped the JSON script block")
	}

	d := parseData(t, page)
	if d.Root != `D:\` || d.From.ID != 1 || d.To.ID != 2 {
		t.Fatalf("header data = %+v", d)
	}
	if len(d.Errors) != 1 || d.Errors[0].Path != `D:\secret` || d.Errors[0].Hits != 2 {
		t.Fatalf("errors = %+v", d.Errors)
	}
	if len(d.Warnings) != 1 || d.Warnings[0] != "coverage is low" {
		t.Fatalf("warnings = %+v", d.Warnings)
	}

	// Treemap rectangles must not overlap: parents already contain their ranked
	// descendants, so only own growth is drawn and the tiles sum to the net.
	got := map[string]int64{}
	var sum int64
	for _, tile := range d.Tiles {
		got[tile.Label] = tile.Delta
		sum += tile.Delta
	}
	if got["FatTraceDemo"] != 1_000 || got["cache"] != 9_000 || got["Data"] != 500 {
		t.Fatalf("tiles = %+v", d.Tiles)
	}
	if sum != d.Totals.DeltaAlloc {
		t.Fatalf("tile sum = %d, want net %d", sum, d.Totals.DeltaAlloc)
	}
	var dataTile *Tile
	for i := range d.Tiles {
		if d.Tiles[i].Label == "Data" {
			dataTile = &d.Tiles[i]
		}
	}
	if dataTile == nil || !dataTile.Unknown {
		t.Fatalf("unknown tile flag was lost: %+v", d.Tiles)
	}
}

func TestBuildTilesAddsOtherOnlyWhenPositive(t *testing.T) {
	dirs := []model.DirChange{
		{Name: "A", Path: `D:\A`, DeltaAlloc: 1000},
		{Name: "B", Path: `D:\A\B`, DeltaAlloc: 600},
		{Name: "C", Path: `D:\A\B\C`, DeltaAlloc: 600},
		{Name: "D", Path: `D:\D`, DeltaAlloc: 200},
	}
	tiles := buildTiles(dirs, 1200)
	got := map[string]int64{}
	for _, tile := range tiles {
		got[tile.Label] = tile.Delta
	}
	if got["A"] != 400 || got["C"] != 600 || got["D"] != 200 || len(tiles) != 3 {
		t.Fatalf("tiles = %+v", tiles)
	}
	if _, ok := got["其他目录"]; ok {
		t.Fatalf("unexpected remainder tile: %+v", tiles)
	}

	tiles = buildTiles([]model.DirChange{{Name: "A", Path: `D:\A`, DeltaAlloc: 500}}, 800)
	if len(tiles) != 2 || tiles[1].Label != "其他目录" || tiles[1].Delta != 300 {
		t.Fatalf("remainder tiles = %+v", tiles)
	}
}

func TestRunEndToEndWithSnapshotErrors(t *testing.T) {
	st := newStore(t)
	ctx := context.Background()
	vol, err := st.EnsureVolume(ctx, 0x3333, "NTFS", "")
	if err != nil {
		t.Fatal(err)
	}
	base := []model.FileRecord{
		{FRN: 1, ParentFRN: 0, Name: `D:\`, IsDir: true, Attrs: 0x10},
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 100, AllocSize: 4096},
	}
	from := seedReportSnapshot(t, st, vol, `D:\`, 10_000, base, nil)
	grown := []model.FileRecord{
		base[0],
		{FRN: 10, ParentFRN: 1, Name: "keep.bin", Size: 200, AllocSize: 8192},
	}
	to := seedReportSnapshot(t, st, vol, `D:\`, 14_096, grown,
		[]model.ScanError{{Path: `D:\secret`, FRN: 7, Op: "open", Msg: "access denied"}})

	page, err := Run(ctx, st, Options{TopN: 10})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	d := parseData(t, page)
	if d.From.ID != from || d.To.ID != to {
		t.Fatalf("pair = #%d → #%d, want #%d → #%d", d.From.ID, d.To.ID, from, to)
	}
	if d.Totals.FilesGrown != 1 || d.Totals.DeltaAlloc != 4096 {
		t.Fatalf("totals = %+v", d.Totals)
	}
	if !d.Check.OK || d.Check.Skipped {
		t.Fatalf("volume check = %+v", d.Check)
	}
	if len(d.Errors) != 1 || d.Errors[0].Path != `D:\secret` {
		t.Fatalf("errors = %+v", d.Errors)
	}
	if len(d.Tiles) == 0 || d.Tiles[0].Delta != 4096 {
		t.Fatalf("tiles = %+v", d.Tiles)
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "report.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedReportSnapshot(t *testing.T, st *store.Store, vol int64, root string, used int64,
	recs []model.FileRecord, errs []model.ScanError) int64 {
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
	var files, dirs, alloc int64
	for _, r := range recs {
		if r.IsDir {
			dirs++
		} else {
			files++
			alloc += r.AllocSize
		}
	}
	if err := st.CommitSnapshot(ctx, store.SnapshotCommit{
		SnapshotID: id, VolumeID: vol, Status: "complete",
		Stats: model.ScanStats{
			Files: files, Dirs: dirs, Bytes: alloc, AllocBytes: alloc,
			UniqueBytes: alloc, UniqueAllocBytes: alloc,
			VolumeUsed: used, CoveragePct: 99.9, Errors: int64(len(errs)),
		},
		Dirs: agg, Errors: errs,
	}); err != nil {
		t.Fatalf("commit snapshot: %v", err)
	}
	return id
}
