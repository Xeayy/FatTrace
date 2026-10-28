// Package rules loads path/name explanation rules and annotates diff results.
// Rules never influence scan data or deltas: they only label growth that the
// user can recognise (caches, logs, stores, system data).
package rules

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"fattrace/internal/model"
)

//go:embed builtin.yaml
var builtinYAML []byte

const (
	// UserFileName is the optional extension file searched next to the database.
	UserFileName = "rules.yaml"
	rulesVersion = 1
)

var validConfidence = map[string]bool{"high": true, "medium": true, "low": true}

var categoryLabels = map[string]string{
	"browser":      "浏览器缓存",
	"package":      "包管理器缓存",
	"toolchain":    "语言工具链缓存",
	"container":    "虚拟化与容器",
	"system":       "系统缓存与更新",
	"logs":         "日志与诊断",
	"security":     "安全软件",
	"recycle":      "回收站",
	"temp":         "临时文件",
	"app-cache":    "应用缓存",
	"app-data":     "应用数据",
	"dev":          "开发依赖与产物",
	"backup":       "系统备份",
	"crash":        "崩溃转储",
	"user-content": "用户文件",
	"database":     "应用数据库",
	"images":       "磁盘镜像",
	"games":        "游戏安装",
}

// CategoryLabel returns the human label for a rule category.
func CategoryLabel(category string) string {
	if label, ok := categoryLabels[category]; ok {
		return label
	}
	return category
}

// Match selects a path or file name. When both lists are present both must
// match; patterns support '*' and '?' within one segment and '**' across
// segments. Matching is case-insensitive and uses backslash separators.
type Match struct {
	PathGlobs []string `yaml:"path_globs" json:"path_globs,omitempty"`
	NameGlobs []string `yaml:"name_globs" json:"name_globs,omitempty"`
}

// Rule is one explanation rule. Rules are matched independently and the most
// specific matching rule wins; user rules win ties against built-ins.
type Rule struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Category    string `yaml:"category" json:"category"`
	Confidence  string `yaml:"confidence" json:"confidence"`
	Description string `yaml:"description" json:"description"`
	Match       Match  `yaml:"match" json:"match"`
	Source      string `yaml:"-" json:"source"`
}

type ruleFile struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}

type compiledRule struct {
	rule      *Rule
	pathSegs  [][]string
	nameGlobs []string
}

// Set is an immutable, compiled rule set. The exported fields are suitable for
// serving to the read-only UI.
type Set struct {
	Rules        []Rule `json:"rules"`
	UserPath     string `json:"user_path,omitempty"`
	RulesBuiltin int    `json:"rules_builtin"`
	RulesUser    int    `json:"rules_user"`
	compiled     []compiledRule
}

// Load loads the embedded rules plus an optional <db_dir>/rules.yaml. Missing
// user files are fine; an unreadable or invalid file is an error so typos do
// not silently fall back to built-ins.
func Load(dbPath string) (*Set, error) {
	builtin, err := parseRules(builtinYAML, "builtin")
	if err != nil {
		return nil, fmt.Errorf("rules: built-in rules: %w", err)
	}
	if len(builtin) == 0 {
		return nil, errors.New("rules: built-in rules are empty")
	}
	set := &Set{Rules: builtin, RulesBuiltin: len(builtin)}
	if dbPath != "" {
		path := filepath.Join(filepath.Dir(dbPath), UserFileName)
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			user, err := parseRules(data, "user")
			if err != nil {
				return nil, fmt.Errorf("rules: %s: %w", path, err)
			}
			set.UserPath = path
			set.RulesUser = len(user)
			set.Rules = mergeRules(user, builtin)
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, fmt.Errorf("rules: read %s: %w", path, err)
		}
	}
	set.compile()
	return set, nil
}

var (
	builtinOnce sync.Once
	builtinSet  *Set
)

// MustBuiltin returns a shared set containing only the embedded rules. It
// panics only if the compiled-in YAML is invalid, which the test suite catches.
func MustBuiltin() *Set {
	builtinOnce.Do(func() {
		list, err := parseRules(builtinYAML, "builtin")
		if err != nil {
			panic("rules: built-in rules: " + err.Error())
		}
		s := &Set{Rules: list, RulesBuiltin: len(list)}
		s.compile()
		builtinSet = s
	})
	return builtinSet
}

func parseRules(data []byte, source string) ([]Rule, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f ruleFile
	if err := dec.Decode(&f); err != nil {
		return nil, err
	}
	if f.Version != rulesVersion {
		return nil, fmt.Errorf("unsupported rules version %d (want %d)", f.Version, rulesVersion)
	}
	seen := make(map[string]bool, len(f.Rules))
	for i := range f.Rules {
		r := &f.Rules[i]
		r.Source = source
		if err := validateRule(r); err != nil {
			return nil, fmt.Errorf("rule %d (%s): %w", i+1, r.ID, err)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("duplicate rule id %q", r.ID)
		}
		seen[r.ID] = true
	}
	return f.Rules, nil
}

func validateRule(r *Rule) error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("id is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(r.Category) == "" {
		return errors.New("category is required")
	}
	if !validConfidence[r.Confidence] {
		return fmt.Errorf("confidence must be high, medium or low (got %q)", r.Confidence)
	}
	if len(r.Match.PathGlobs)+len(r.Match.NameGlobs) == 0 {
		return errors.New("match needs path_globs or name_globs")
	}
	for _, g := range append(append([]string{}, r.Match.PathGlobs...), r.Match.NameGlobs...) {
		if strings.TrimSpace(g) == "" {
			return errors.New("empty match pattern")
		}
	}
	return nil
}

func mergeRules(user, builtin []Rule) []Rule {
	userIDs := make(map[string]bool, len(user))
	out := make([]Rule, 0, len(user)+len(builtin))
	for _, r := range user {
		userIDs[r.ID] = true
		out = append(out, r)
	}
	for _, r := range builtin {
		if !userIDs[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func (s *Set) compile() {
	s.compiled = make([]compiledRule, 0, len(s.Rules))
	for i := range s.Rules {
		r := &s.Rules[i]
		cr := compiledRule{rule: r}
		for _, g := range r.Match.PathGlobs {
			cr.pathSegs = append(cr.pathSegs, splitGlob(g))
		}
		for _, g := range r.Match.NameGlobs {
			cr.nameGlobs = append(cr.nameGlobs, strings.ToLower(g))
		}
		s.compiled = append(s.compiled, cr)
	}
}

// Match returns the most specific rule matching the change. nil means no rule
// matched and the growth stays unexplained.
func (s *Set) Match(path, name string) *Rule {
	if s == nil {
		return nil
	}
	clean := cleanPath(path)
	lowerName := strings.ToLower(strings.TrimSpace(name))
	if lowerName == "" {
		lowerName = baseName(clean)
	}
	segs := splitPath(clean)

	bestScore := -1
	var best *Rule
	for i := range s.compiled {
		cr := &s.compiled[i]
		score, ok := cr.match(segs, lowerName)
		if ok && score > bestScore {
			bestScore = score
			best = cr.rule
		}
	}
	return best
}

func (cr *compiledRule) match(pathSegs []string, name string) (int, bool) {
	pathScore := 0
	if len(cr.pathSegs) > 0 {
		pathScore = -1
		for _, pat := range cr.pathSegs {
			if matchPath(pat, pathSegs) {
				if score := literalCount(pat); score > pathScore {
					pathScore = score
				}
			}
		}
		if pathScore < 0 {
			return 0, false
		}
	}
	nameScore := 0
	if len(cr.nameGlobs) > 0 {
		nameScore = -1
		for _, g := range cr.nameGlobs {
			if matchSegment(g, name) {
				if score := literalCountString(g); score > nameScore {
					nameScore = score
				}
			}
		}
		if nameScore < 0 {
			return 0, false
		}
	}
	return pathScore + nameScore, true
}

// Ref converts a rule to the compact reference stored on change entries.
func (r *Rule) Ref() *model.RuleRef {
	if r == nil {
		return nil
	}
	return &model.RuleRef{
		ID: r.ID, Name: r.Name, Category: r.Category,
		CategoryLabel: CategoryLabel(r.Category), Confidence: r.Confidence,
	}
}

func cleanPath(p string) string {
	p = filepath.Clean(p)
	p = strings.ReplaceAll(p, "/", `\`)
	if len(p) == 3 && p[1] == ':' && p[2] == '\\' {
		p = p[:2]
	}
	p = strings.TrimRight(p, `\`)
	return strings.ToLower(p)
}

func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, `\`)
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func splitGlob(g string) []string {
	g = strings.ReplaceAll(g, "/", `\`)
	g = strings.ToLower(g)
	g = strings.TrimSuffix(g, `\`)
	return strings.Split(g, `\`)
}

// matchPath matches whole path segments. '**' consumes zero or more segments.
func matchPath(pattern, path []string) bool {
	n, m := len(pattern), len(path)
	// memo: 0 unknown, 1 false, 2 true
	memo := make([][]int8, n+1)
	for i := range memo {
		memo[i] = make([]int8, m+1)
	}
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		if i == n {
			return j == m
		}
		if memo[i][j] != 0 {
			return memo[i][j] == 2
		}
		ok := false
		if pattern[i] == "**" {
			ok = rec(i+1, j) || (j < m && rec(i, j+1))
		} else if j < m && matchSegment(pattern[i], path[j]) {
			ok = rec(i+1, j+1)
		}
		if ok {
			memo[i][j] = 2
		} else {
			memo[i][j] = 1
		}
		return ok
	}
	return rec(0, 0)
}

// matchSegment matches one path segment with '*' and '?' only; '[', ']' and
// backslashes are literal so ordinary file names never gain glob power.
func matchSegment(pattern, s string) bool {
	p := []rune(pattern)
	t := []rune(s)
	n, m := len(p), len(t)
	memo := make([][]int8, n+1)
	for i := range memo {
		memo[i] = make([]int8, m+1)
	}
	var rec func(i, j int) bool
	rec = func(i, j int) bool {
		if i == n {
			return j == m
		}
		if memo[i][j] != 0 {
			return memo[i][j] == 2
		}
		ok := false
		switch p[i] {
		case '*':
			ok = rec(i+1, j) || (j < m && rec(i, j+1))
		case '?':
			ok = j < m && rec(i+1, j+1)
		default:
			ok = j < m && p[i] == t[j] && rec(i+1, j+1)
		}
		if ok {
			memo[i][j] = 2
		} else {
			memo[i][j] = 1
		}
		return ok
	}
	return rec(0, 0)
}

func literalCount(segs []string) int {
	n := 0
	for _, s := range segs {
		n += literalCountString(s)
	}
	return n
}

func literalCountString(s string) int {
	n := 0
	for _, r := range s {
		if r != '*' && r != '?' {
			n++
		}
	}
	return n
}
