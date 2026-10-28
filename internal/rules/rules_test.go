package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fattrace/internal/model"
)

func TestBuiltinRulesLoad(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(set.Rules) < 60 {
		t.Fatalf("builtin rules = %d, want at least 60", len(set.Rules))
	}
	if set.RulesBuiltin != len(set.Rules) || set.RulesUser != 0 {
		t.Fatalf("counts builtin=%d user=%d total=%d", set.RulesBuiltin, set.RulesUser, len(set.Rules))
	}
	ids := make(map[string]bool, len(set.Rules))
	for _, r := range set.Rules {
		if ids[r.ID] {
			t.Fatalf("duplicate id %q", r.ID)
		}
		ids[r.ID] = true
		if r.Source != "builtin" {
			t.Fatalf("%s source = %q", r.ID, r.Source)
		}
		if !validConfidence[r.Confidence] {
			t.Fatalf("%s confidence = %q", r.ID, r.Confidence)
		}
		if len(r.Match.PathGlobs)+len(r.Match.NameGlobs) == 0 {
			t.Fatalf("%s has no match patterns", r.ID)
		}
	}
}

func TestMatchSpecificity(t *testing.T) {
	set := MustBuiltin()
	cases := []struct {
		path, name string
		want       string
	}{
		{`D:\Users\me\AppData\Local\Google\Chrome\User Data\Default\Cache\data_0`, "data_0", "browser.chrome.cache"},
		{`D:\Users\me\AppData\Local\Google\Chrome\User Data\Default\Code Cache\js\abc`, "abc", "browser.chrome.cache"},
		{`d:\USERS\me\appdata\local\temp\foo.tmp`, "foo.tmp", "temp.user"},
		{`D:\Work\proj\node_modules\react\index.js`, "index.js", "dev.node-modules"},
		{`D:\FatTraceDemo\.cache\gobuild\aa\bb`, "bb", "tool.go.build"},
		{`D:\FatTraceDemo\.cache\gomod\cache\download\x`, "x", "tool.go.mod"},
		{`D:\Work\proj\target\debug\deps\app.exe`, "app.exe", "dev.target-debug"},
		{`D:\Games\steamapps\common\Game\data.bin`, "data.bin", "games.steam"},
		{`D:\AppData\Local\SomeApp\Cache\blob`, "blob", "app.generic-cache"},
		{`D:\media\holiday.iso`, "holiday.iso", "images.iso"},
		{`D:\data\shop.db-wal`, "shop.db-wal", "database.app"},
		{`D:\notes\plain.txt`, "plain.txt", ""},
	}
	for _, tc := range cases {
		got := set.Match(tc.path, tc.name)
		gotID := ""
		if got != nil {
			gotID = got.ID
		}
		if gotID != tc.want {
			t.Errorf("Match(%q) = %q, want %q", tc.path, gotID, tc.want)
		}
	}
}

func TestMatchRequiresBothPathAndName(t *testing.T) {
	set := &Set{Rules: []Rule{{
		ID: "test.both", Name: "both", Category: "temp", Confidence: "high",
		Match: Match{
			PathGlobs: []string{`**\Temp\**`},
			NameGlobs: []string{"*.tmp"},
		},
	}}}
	set.compile()
	if r := set.Match(`D:\Temp\a.tmp`, "a.tmp"); r == nil {
		t.Fatal("expected both conditions to match")
	}
	if r := set.Match(`D:\Temp\a.log`, "a.log"); r != nil {
		t.Fatal("name condition should reject a.log")
	}
	if r := set.Match(`D:\Other\a.tmp`, "a.tmp"); r != nil {
		t.Fatal("path condition should reject Other")
	}
}

func TestLoadUserRulesAndOverride(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fattrace.db")
	userFile := filepath.Join(dir, UserFileName)
	body := `version: 1
rules:
  - id: browser.chrome.cache
    name: 自定义 Chrome 缓存
    category: browser
    confidence: high
    description: 覆盖内置规则的测试规则
    match:
      path_globs:
        - '**\AppData\Local\Google\Chrome\User Data\**\Cache\**'
  - id: user.scratch
    name: 临时工作区
    category: app-cache
    confidence: low
    description: 用户自定义
    match:
      path_globs:
        - 'D:\scratch\**'
`
	if err := os.WriteFile(userFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := Load(dbPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.UserPath != userFile {
		t.Fatalf("UserPath = %q, want %q", set.UserPath, userFile)
	}
	if set.RulesUser != 2 {
		t.Fatalf("RulesUser = %d, want 2", set.RulesUser)
	}
	if set.RulesBuiltin < 60 {
		t.Fatalf("RulesBuiltin = %d, want at least 60", set.RulesBuiltin)
	}
	if len(set.Rules) != set.RulesBuiltin+1 {
		t.Fatalf("merged rules = %d, want builtin+1", len(set.Rules))
	}
	if set.Rules[0].Source != "user" {
		t.Fatalf("first rule source = %q, want user", set.Rules[0].Source)
	}
	if got := set.Match(`D:\Users\me\AppData\Local\Google\Chrome\User Data\Default\Cache\x`, "x"); got == nil || got.Name != "自定义 Chrome 缓存" {
		t.Fatalf("override not applied: %+v", got)
	}
	if got := set.Match(`D:\scratch\a.bin`, "a.bin"); got == nil || got.ID != "user.scratch" {
		t.Fatalf("user rule not applied: %+v", got)
	}
}

func TestLoadUserRulesErrors(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fattrace.db")
	userFile := filepath.Join(dir, UserFileName)

	bad := []string{
		"version: 1\nrules:\n  - id: x\n    name: x\n    category: temp\n    confidence: high\n    match:\n      path_globs: ['**']\n    typo: 1\n",
		"version: 1\nrules:\n  - id: x\n    name: x\n    category: temp\n    confidence: high\n    match:\n      path_globs: ['**']\n  - id: x\n    name: y\n    category: temp\n    confidence: high\n    match:\n      path_globs: ['**']\n",
		"version: 99\nrules: []\n",
		"version: 1\nrules:\n  - id: x\n    name: x\n    category: temp\n    confidence: urgent\n    match:\n      path_globs: ['**']\n",
	}
	for i, body := range bad {
		if err := os.WriteFile(userFile, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dbPath); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestAggregatorExplanation(t *testing.T) {
	set := MustBuiltin()
	agg := newAggregator(set)
	const mib = 1 << 20
	agg.observe(model.FileChange{Kind: model.ChangeAdded, Path: `D:\Users\me\AppData\Local\Google\Chrome\User Data\Default\Cache\data_0`, Name: "data_0", DeltaAlloc: 6 * mib})
	agg.observe(model.FileChange{Kind: model.ChangeGrown, Path: `D:\Work\proj\node_modules\react\index.js`, Name: "index.js", DeltaAlloc: 3 * mib})
	agg.observe(model.FileChange{Kind: model.ChangeAdded, Path: `D:\random\blob.bin`, Name: "blob.bin", DeltaAlloc: 4 * mib})
	agg.observe(model.FileChange{Kind: model.ChangeDeleted, Path: `D:\random\old.bin`, Name: "old.bin", DeltaAlloc: -10 * mib})
	agg.observe(model.FileChange{Kind: model.ChangeMoved, Path: `D:\random\moved.bin`, Name: "moved.bin", DeltaAlloc: 5 * mib})
	agg.observe(model.FileChange{Kind: model.ChangeAdded, Path: `D:\random\empty.bin`, Name: "empty.bin", DeltaAlloc: 0})

	res := model.DiffResult{Totals: model.DiffTotals{DeltaAlloc: 13 * mib}}
	agg.finalize(&res)
	e := res.Explanation
	if e == nil {
		t.Fatal("explanation is nil")
	}
	if e.GrowthBytes != 13*mib || e.ExplainedBytes != 9*mib || e.UnexplainedBytes != 4*mib {
		t.Fatalf("bytes: growth=%d explained=%d unexplained=%d", e.GrowthBytes, e.ExplainedBytes, e.UnexplainedBytes)
	}
	if e.ExplainedPct != 69.23 {
		t.Fatalf("pct = %v, want 69.23", e.ExplainedPct)
	}
	if e.UnexplainedTotal != 1 || len(e.Unexplained) != 1 || e.Unexplained[0].Path != `D:\random\blob.bin` {
		t.Fatalf("unexplained = %+v", e.Unexplained)
	}
	if len(e.Categories) != 2 {
		t.Fatalf("categories = %+v", e.Categories)
	}
	if e.NetGrowthBytes != 13*mib {
		t.Fatalf("net growth = %d", e.NetGrowthBytes)
	}
	if e.RulesLoaded != len(set.Rules) {
		t.Fatalf("rules loaded = %d", e.RulesLoaded)
	}
}

func TestUnexplainedTopIsBounded(t *testing.T) {
	agg := newAggregator(MustBuiltin())
	for i := 0; i < 80; i++ {
		name := fmt.Sprintf("file%02d.bin", i)
		agg.observe(model.FileChange{
			Kind: model.ChangeAdded, Path: `D:\random\` + name, Name: name, DeltaAlloc: int64(i + 1),
		})
	}
	res := model.DiffResult{}
	agg.finalize(&res)
	e := res.Explanation
	if e.UnexplainedTotal != 80 {
		t.Fatalf("total = %d, want 80", e.UnexplainedTotal)
	}
	if len(e.Unexplained) != unexplainedLimit {
		t.Fatalf("retained = %d, want %d", len(e.Unexplained), unexplainedLimit)
	}
	if e.Unexplained[0].DeltaAlloc != 80 || e.Unexplained[len(e.Unexplained)-1].DeltaAlloc != 31 {
		t.Fatalf("top range = %d..%d, want 80..31",
			e.Unexplained[0].DeltaAlloc, e.Unexplained[len(e.Unexplained)-1].DeltaAlloc)
	}
	for i := 1; i < len(e.Unexplained); i++ {
		if e.Unexplained[i-1].DeltaAlloc < e.Unexplained[i].DeltaAlloc {
			t.Fatalf("not sorted at %d: %+v", i, e.Unexplained)
		}
	}
}

func TestAnnotateRankedLists(t *testing.T) {
	set := MustBuiltin()
	res := model.DiffResult{
		TopGrowth: []model.FileChange{
			{Path: `D:\Users\me\AppData\Local\Temp\a.bin`, Name: "a.bin", DeltaAlloc: 1},
			{Path: `D:\random\b.bin`, Name: "b.bin", DeltaAlloc: 1},
		},
		TopDirs: []model.DirChange{
			{Path: `D:\Work\proj\node_modules`, Name: "node_modules", DeltaAlloc: 1},
			{Path: `D:\random`, Name: "random", DeltaAlloc: 1},
		},
	}
	annotate(&res, set)
	if res.TopGrowth[0].Rule == nil || res.TopGrowth[0].Rule.ID != "temp.user" {
		t.Fatalf("file rule = %+v", res.TopGrowth[0].Rule)
	}
	if res.TopGrowth[1].Rule != nil {
		t.Fatalf("unmatched file got rule %+v", res.TopGrowth[1].Rule)
	}
	if res.TopDirs[0].Rule == nil || res.TopDirs[0].Rule.ID != "dev.node-modules" {
		t.Fatalf("dir rule = %+v", res.TopDirs[0].Rule)
	}
	if !strings.Contains(CategoryLabel("browser"), "浏览器") {
		t.Fatalf("category label = %q", CategoryLabel("browser"))
	}
}
