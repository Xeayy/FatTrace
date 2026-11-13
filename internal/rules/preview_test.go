package rules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xeayy/FatTrace/internal/model"
)

func TestUnexplainedStoreLimit(t *testing.T) {
	agg := newAggregator(MustBuiltin())
	const n = 6000
	for i := 0; i < n; i++ {
		agg.observe(model.FileChange{
			Kind: model.ChangeAdded,
			Path: `D:\nomatch\file` + itoa(i) + `.bin`,
			Name: "file.bin", DeltaAlloc: int64(i + 1),
		})
	}
	var res model.DiffResult
	agg.finalize(&res)
	ex := res.Explanation
	if ex.UnexplainedTotal != n {
		t.Fatalf("total = %d, want %d", ex.UnexplainedTotal, n)
	}
	if len(ex.AllUnexplained) != unexplainedStoreLimit {
		t.Fatalf("store sample = %d, want %d", len(ex.AllUnexplained), unexplainedStoreLimit)
	}
	if len(ex.Unexplained) != unexplainedLimit {
		t.Fatalf("public top = %d, want %d", len(ex.Unexplained), unexplainedLimit)
	}
	if ex.AllUnexplained[0].DeltaAlloc != n {
		t.Fatalf("largest = %d, want %d", ex.AllUnexplained[0].DeltaAlloc, n)
	}
	data, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "AllUnexplained") {
		t.Fatal("AllUnexplained must not be serialized into reports/diff JSON")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestPreviewMatchesUnexplained(t *testing.T) {
	base := MustBuiltin()
	candidate := Rule{
		ID: "user.bin", Name: "Bin", Category: "dev", Confidence: "medium",
		Match: Match{NameGlobs: []string{"*.bin"}},
	}
	items := []model.UnexplainedItem{
		{Path: `D:\a\x.bin`, Kind: model.ChangeAdded, DeltaAlloc: 1000},
		{Path: `D:\b\y.bin`, Kind: model.ChangeGrown, DeltaAlloc: 2000},
		{Path: `D:\c\z.log`, Kind: model.ChangeAdded, DeltaAlloc: 500},
	}
	rep := Preview(base, candidate, items, 3500, 0)
	if !rep.Valid {
		t.Fatalf("preview invalid: %v", rep.Errors)
	}
	if rep.MatchedFiles != 2 || rep.MatchedBytes != 3000 {
		t.Fatalf("matched = %d files / %d bytes", rep.MatchedFiles, rep.MatchedBytes)
	}
	if rep.ProjectedExplainedPct <= 0 {
		t.Fatalf("projected pct = %v", rep.ProjectedExplainedPct)
	}
}

func TestPreviewValidationAndWarnings(t *testing.T) {
	base := MustBuiltin()
	bad := Rule{ID: "x", Name: "X", Category: "dev", Confidence: "bogus",
		Match: Match{NameGlobs: []string{"*.bin"}}}
	if rep := Preview(base, bad, nil, 100, 0); rep.Valid {
		t.Fatal("invalid confidence must not be valid")
	}
	broad := Rule{ID: "b", Name: "B", Category: "dev", Confidence: "low",
		Match: Match{PathGlobs: []string{"**"}}}
	rep := Preview(base, broad, []model.UnexplainedItem{
		{Path: `D:\a\x.bin`, DeltaAlloc: 900},
	}, 1000, 0)
	if !rep.Valid || len(rep.Warnings) == 0 {
		t.Fatalf("broad rule should warn: %+v", rep)
	}
}

func TestKnownCategories(t *testing.T) {
	cats := KnownCategories()
	if len(cats) == 0 {
		t.Fatal("no categories")
	}
	for _, c := range cats {
		if c.Label == "" || c.Category == "" {
			t.Fatalf("bad category %+v", c)
		}
	}
}
