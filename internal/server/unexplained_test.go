package server

import (
	"net/http"
	"strconv"
	"testing"

	"fattrace/internal/model"
	"fattrace/internal/rules"
)

func TestUnexplainedEndpoint(t *testing.T) {
	f := newFixture(t)
	base := "/api/unexplained?from=" + strconv.FormatInt(f.from, 10) + "&to=" + strconv.FormatInt(f.to, 10)

	var rep struct {
		Items            []model.UnexplainedItem `json:"items"`
		Total            int                     `json:"total"`
		UnexplainedTotal int                     `json:"unexplained_total"`
		ExplainedPct     float64                 `json:"explained_pct"`
	}
	if code := doGet(t, f, base, testToken, &rep); code != http.StatusOK {
		t.Fatalf("unexplained status = %d", code)
	}
	if rep.Total == 0 || len(rep.Items) == 0 {
		t.Fatalf("expected unexplained items, got %+v", rep)
	}
	found := false
	for _, it := range rep.Items {
		if it.Path == `D:\new.bin` {
			found = true
		}
	}
	if !found {
		t.Fatalf("new.bin not in unexplained: %+v", rep.Items)
	}

	var filtered struct {
		Items []model.UnexplainedItem `json:"items"`
		Total int                     `json:"total"`
	}
	if code := doGet(t, f, base+"&q=new.bin", testToken, &filtered); code != http.StatusOK {
		t.Fatalf("filtered status = %d", code)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 {
		t.Fatalf("filter q=new.bin = %+v", filtered)
	}

	var paged struct {
		Items  []model.UnexplainedItem `json:"items"`
		Offset int                     `json:"offset"`
	}
	if code := doGet(t, f, base+"&limit=1&offset=0", testToken, &paged); code != http.StatusOK {
		t.Fatalf("paged status = %d", code)
	}
	if len(paged.Items) != 1 {
		t.Fatalf("limit=1 returned %d items", len(paged.Items))
	}

	if code := doGet(t, f, "/api/unexplained", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", code)
	}
}

func TestRulePreviewEndpoint(t *testing.T) {
	f := newFixture(t)
	base := "/api/rules/preview?from=" + strconv.FormatInt(f.from, 10) + "&to=" + strconv.FormatInt(f.to, 10)

	var rep rules.PreviewResult
	q := base + "&id=user.new&name=New&category=dev&confidence=medium&name_globs=new.bin"
	if code := doGet(t, f, q, testToken, &rep); code != http.StatusOK {
		t.Fatalf("preview status = %d", code)
	}
	if !rep.Valid || rep.MatchedFiles != 1 || rep.MatchedBytes != 2000 {
		t.Fatalf("preview = %+v", rep)
	}

	var bad rules.PreviewResult
	qBad := base + "&id=user.new&name=New&category=dev&confidence=bogus&name_globs=new.bin"
	if code := doGet(t, f, qBad, testToken, &bad); code != http.StatusOK {
		t.Fatalf("invalid preview status = %d", code)
	}
	if bad.Valid {
		t.Fatal("invalid confidence must report valid=false")
	}
}

func TestCategoriesEndpoint(t *testing.T) {
	f := newFixture(t)
	var rep struct {
		Categories []rules.CategoryInfo `json:"categories"`
	}
	if code := doGet(t, f, "/api/categories", testToken, &rep); code != http.StatusOK {
		t.Fatalf("categories status = %d", code)
	}
	if len(rep.Categories) == 0 {
		t.Fatal("no categories returned")
	}
}
