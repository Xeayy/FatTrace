package rules

import (
	"container/heap"
	"context"
	"math"
	"sort"

	"fattrace/internal/diff"
	"fattrace/internal/model"
	"fattrace/internal/store"
)

const (
	// unexplainedLimit is the public top-N shown in CLI/report/diff JSON.
	unexplainedLimit = 50
	// unexplainedStoreLimit bounds the larger sample kept in memory for the
	// read-only unexplained browser; it never reaches reports or /api/diff.
	unexplainedStoreLimit = 5000
)

// Diff runs the diff engine with rule explanation enabled. The observer sees
// every file change, so the explanation rate covers all observed growth rather
// than just the top-N lists.
func Diff(ctx context.Context, st *store.Store, opts diff.Options, set *Set) (model.DiffResult, error) {
	if set == nil {
		set = MustBuiltin()
	}
	agg := newAggregator(set)
	opts.OnFileChange = agg.observe
	res, err := diff.Run(ctx, st, opts)
	if err != nil {
		return model.DiffResult{}, err
	}
	agg.finalize(&res)
	annotate(&res, set)
	return res, nil
}

type aggregator struct {
	set         *Set
	growth      int64
	explained   int64
	categories  map[string]*model.ExplainedCategory
	unexplained unexplainedHeap
	total       int
}

func newAggregator(set *Set) *aggregator {
	return &aggregator{set: set, categories: map[string]*model.ExplainedCategory{}}
}

func (a *aggregator) observe(c model.FileChange) {
	if c.Kind != model.ChangeAdded && c.Kind != model.ChangeGrown {
		return
	}
	if c.DeltaAlloc <= 0 {
		return
	}
	a.growth += c.DeltaAlloc
	r := a.set.Match(c.Path, c.Name)
	if r == nil {
		path := c.Path
		if path == "" {
			path = c.Name
		}
		a.total++
		item := model.UnexplainedItem{Path: path, Kind: c.Kind, DeltaAlloc: c.DeltaAlloc}
		if a.unexplained.Len() < unexplainedStoreLimit {
			heap.Push(&a.unexplained, item)
		} else if unexplainedBetter(item, a.unexplained[0]) {
			a.unexplained[0] = item
			heap.Fix(&a.unexplained, 0)
		}
		return
	}
	a.explained += c.DeltaAlloc
	cat := a.categories[r.Category]
	if cat == nil {
		cat = &model.ExplainedCategory{Category: r.Category, Label: CategoryLabel(r.Category)}
		a.categories[r.Category] = cat
	}
	cat.Bytes += c.DeltaAlloc
	cat.Files++
}

func (a *aggregator) finalize(res *model.DiffResult) {
	cats := make([]model.ExplainedCategory, 0, len(a.categories))
	for _, c := range a.categories {
		cats = append(cats, *c)
	}
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].Bytes != cats[j].Bytes {
			return cats[i].Bytes > cats[j].Bytes
		}
		return cats[i].Label < cats[j].Label
	})

	all := make([]model.UnexplainedItem, len(a.unexplained))
	copy(all, a.unexplained)
	sort.Slice(all, func(i, j int) bool {
		if all[i].DeltaAlloc != all[j].DeltaAlloc {
			return all[i].DeltaAlloc > all[j].DeltaAlloc
		}
		return all[i].Path < all[j].Path
	})
	unexplained := all
	if len(unexplained) > unexplainedLimit {
		unexplained = unexplained[:unexplainedLimit]
	}

	pct := 100.0
	if a.growth > 0 {
		pct = math.Round(float64(a.explained)/float64(a.growth)*10000) / 100
	}
	res.Explanation = &model.Explanation{
		RulesLoaded:      len(a.set.Rules),
		GrowthBytes:      a.growth,
		ExplainedBytes:   a.explained,
		UnexplainedBytes: a.growth - a.explained,
		ExplainedPct:     pct,
		NetGrowthBytes:   res.Totals.DeltaAlloc,
		Categories:       cats,
		Unexplained:      unexplained,
		UnexplainedTotal: a.total,
		AllUnexplained:   all,
	}
}

// unexplainedHeap keeps the worst retained item at the root: lower bytes, or a
// larger path when bytes tie. That makes the top-N cut deterministic.
type unexplainedHeap []model.UnexplainedItem

func (h unexplainedHeap) Len() int { return len(h) }
func (h unexplainedHeap) Less(i, j int) bool {
	if h[i].DeltaAlloc != h[j].DeltaAlloc {
		return h[i].DeltaAlloc < h[j].DeltaAlloc
	}
	return h[i].Path > h[j].Path
}
func (h unexplainedHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *unexplainedHeap) Push(x any)   { *h = append(*h, x.(model.UnexplainedItem)) }
func (h *unexplainedHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func unexplainedBetter(a, b model.UnexplainedItem) bool {
	if a.DeltaAlloc != b.DeltaAlloc {
		return a.DeltaAlloc > b.DeltaAlloc
	}
	return a.Path < b.Path
}

// annotate stamps rule references onto the ranked lists shown by the CLI, the
// report and the UI. Explanation totals were already collected from all
// changes; this only labels displayed rows.
func annotate(res *model.DiffResult, set *Set) {
	annotateFiles := func(list []model.FileChange) {
		for i := range list {
			list[i].Rule = set.Match(list[i].Path, list[i].Name).Ref()
		}
	}
	annotateDirs := func(list []model.DirChange) {
		for i := range list {
			list[i].Rule = set.Match(list[i].Path, list[i].Name).Ref()
		}
	}
	annotateFiles(res.TopGrowth)
	annotateFiles(res.TopDeleted)
	annotateFiles(res.Moves)
	annotateDirs(res.TopDirs)
	annotateDirs(res.ShrunkDirs)
	annotateDirs(res.DirMoves)
}
