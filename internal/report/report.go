package report

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Xeayy/FatTrace/internal/model"
)

const maxPathWalk = 128

//go:embed template.html
var templateHTML string

const dataMarker = `"__FATTRACE_DATA__"`

// ErrorRow is one aggregated scan error shown in the report.
type ErrorRow struct {
	Path string `json:"path"`
	Op   string `json:"op"`
	Msg  string `json:"msg"`
	Hits int64  `json:"hits"`
}

// Tile is one treemap rectangle: allocated growth owned by a directory.
type Tile struct {
	Path    string `json:"path"`
	Label   string `json:"label"`
	Delta   int64  `json:"delta_alloc"`
	Files   int64  `json:"files"`
	Unknown bool   `json:"unknown,omitempty"`
}

// Data is the JSON payload embedded in the report page.
type Data struct {
	GeneratedAtMS int64              `json:"generated_at_ms"`
	Root          string             `json:"root"`
	From          model.SnapshotRef  `json:"from_snapshot"`
	To            model.SnapshotRef  `json:"to_snapshot"`
	SpanDays      float64            `json:"span_days"`
	Totals        model.DiffTotals   `json:"totals"`
	Check         model.VolumeCheck  `json:"volume_check"`
	Growth        []model.FileChange `json:"top_growth"`
	Deleted       []model.FileChange `json:"top_deleted"`
	Moves         []model.FileChange `json:"file_moves"`
	GrowthDirs    []model.DirChange  `json:"top_growth_dirs"`
	ShrunkDirs    []model.DirChange  `json:"top_shrunk_dirs"`
	DirMoves      []model.DirChange  `json:"dir_moves"`
	Tiles         []Tile             `json:"tiles"`
	Explanation   *model.Explanation `json:"explanation,omitempty"`
	Errors        []ErrorRow         `json:"errors"`
	Warnings      []string           `json:"warnings"`
}

// Build renders the single-file report. All data is embedded as JSON; the page
// performs no network requests.
func Build(res model.DiffResult, errs []ErrorRow, generatedAt time.Time) ([]byte, error) {
	const dayMS = int64(24 * time.Hour / time.Millisecond)
	data := Data{
		GeneratedAtMS: generatedAt.UnixMilli(),
		Root:          res.Root,
		From:          res.FromSnapshot,
		To:            res.ToSnapshot,
		SpanDays:      float64(res.ToSnapshot.StartedAtMS-res.FromSnapshot.StartedAtMS) / float64(dayMS),
		Totals:        res.Totals,
		Check:         res.Check,
		Growth:        res.TopGrowth,
		Deleted:       res.TopDeleted,
		Moves:         res.Moves,
		GrowthDirs:    res.TopDirs,
		ShrunkDirs:    res.ShrunkDirs,
		DirMoves:      res.DirMoves,
		Tiles:         buildTiles(res.TopDirs, res.Totals.DeltaAlloc),
		Explanation:   res.Explanation,
		Errors:        errs,
		Warnings:      res.Warnings,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode report data: %w", err)
	}
	if !strings.Contains(templateHTML, dataMarker) {
		return nil, errors.New("report: template is missing the data marker")
	}
	// encoding/json escapes <, > and &, so file paths cannot close the script tag.
	page := strings.Replace(templateHTML, dataMarker, string(payload), 1)
	return []byte(page), nil
}

// buildTiles turns the ranked growth directories into non-overlapping treemap
// rectangles. A ranked directory's delta already contains its descendants, so
// each entry subtracts itself from its nearest ranked ancestor; only the
// resulting own-growth is drawn. "Other" covers net growth not represented by
// the positive tiles and is only added when positive; using the positive total
// (not the signed ranked total) keeps a shrinking directory from inflating it.
func buildTiles(dirs []model.DirChange, totalDelta int64) []Tile {
	index := make(map[string]int, len(dirs))
	for i, d := range dirs {
		if d.Path != "" {
			index[strings.ToLower(filepath.Clean(d.Path))] = i
		}
	}
	own := make([]int64, len(dirs))
	for i, d := range dirs {
		own[i] = d.DeltaAlloc
	}
	for i, d := range dirs {
		if d.Path == "" {
			continue
		}
		up := filepath.Dir(filepath.Clean(d.Path))
		for depth := 0; depth < maxPathWalk; depth++ {
			if p, ok := index[strings.ToLower(up)]; ok && p != i {
				own[p] -= d.DeltaAlloc
				break
			}
			next := filepath.Dir(up)
			if next == up {
				break
			}
			up = next
		}
	}

	var tiles []Tile
	var shown int64
	for i, d := range dirs {
		if own[i] <= 0 {
			continue
		}
		shown += own[i]
		label := d.Name
		if label == "" {
			label = d.Path
		}
		tiles = append(tiles, Tile{
			Path: d.Path, Label: label, Delta: own[i],
			Files: d.FilesAfter, Unknown: d.UnknownAfter,
		})
	}
	if other := totalDelta - shown; other > 0 {
		tiles = append(tiles, Tile{Label: "其他目录", Delta: other})
	}
	return tiles
}
