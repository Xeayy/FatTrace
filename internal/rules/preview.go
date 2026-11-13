package rules

import (
	"math"
	"sort"
	"strings"

	"github.com/Xeayy/FatTrace/internal/model"
)

// CategoryInfo is one selectable rule category for the read-only UI.
type CategoryInfo struct {
	Category string `json:"category"`
	Label    string `json:"label"`
}

// KnownCategories returns the built-in category list, sorted by key.
func KnownCategories() []CategoryInfo {
	out := make([]CategoryInfo, 0, len(categoryLabels))
	for k, v := range categoryLabels {
		out = append(out, CategoryInfo{Category: k, Label: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// ValidateRule exposes rule validation for the read-only preview API.
func ValidateRule(r *Rule) error { return validateRule(r) }

// PreviewSet returns a compiled set with candidate added as a user rule on top
// of base (same ID overrides). It never mutates base.
func PreviewSet(base *Set, candidate Rule) (*Set, error) {
	if err := validateRule(&candidate); err != nil {
		return nil, err
	}
	candidate.Source = "user"
	list := make([]Rule, 0, len(base.Rules)+1)
	list = append(list, candidate)
	for _, r := range base.Rules {
		if r.ID != candidate.ID {
			list = append(list, r)
		}
	}
	s := &Set{Rules: list, RulesBuiltin: base.RulesBuiltin, RulesUser: base.RulesUser + 1}
	s.compile()
	return s, nil
}

// PreviewResult reports how a candidate rule would explain the current
// unexplained growth sample. It is an estimate over unexplained items only.
type PreviewResult struct {
	Valid                 bool     `json:"valid"`
	Errors                []string `json:"errors,omitempty"`
	MatchedFiles          int      `json:"matched_files"`
	MatchedBytes          int64    `json:"matched_bytes"`
	ProjectedExplainedPct float64  `json:"projected_explained_pct"`
	Sample                []string `json:"sample,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

// Preview matches candidate against the unexplained sample using the real
// matcher and reports hits, bytes and the projected explanation rate.
func Preview(base *Set, candidate Rule, unexplained []model.UnexplainedItem, growth, explained int64) PreviewResult {
	var res PreviewResult
	set, err := PreviewSet(base, candidate)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	res.Valid = true
	for _, it := range unexplained {
		m := set.Match(it.Path, "")
		if m != nil && m.ID == candidate.ID {
			res.MatchedFiles++
			res.MatchedBytes += it.DeltaAlloc
			if len(res.Sample) < 10 {
				res.Sample = append(res.Sample, it.Path)
			}
		}
	}
	if growth > 0 {
		res.ProjectedExplainedPct = math.Round(float64(explained+res.MatchedBytes)/float64(growth)*10000) / 100
	}
	for _, g := range candidate.Match.PathGlobs {
		cg := strings.ToLower(strings.ReplaceAll(g, "/", `\`))
		cg = strings.TrimSuffix(cg, `\`)
		if cg == "**" || cg == "*" || (len(cg) == 2 && cg[1] == ':') {
			res.Warnings = append(res.Warnings, "路径模式过宽（"+g+"），可能把大量无关增长也算作已解释")
		}
	}
	for _, g := range candidate.Match.NameGlobs {
		if strings.TrimSpace(g) == "*" {
			res.Warnings = append(res.Warnings, "文件名模式为 *，会匹配所有文件，建议收窄")
		}
	}
	if growth > 0 && float64(res.MatchedBytes) > 0.5*float64(growth) {
		res.Warnings = append(res.Warnings, "命中超过一半观测增长，请确认是通用模式而非为单个应用硬编码")
	}
	return res
}
