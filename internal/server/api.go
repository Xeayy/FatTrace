package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"fattrace/internal/alerts"
	"fattrace/internal/diff"
	"fattrace/internal/health"
	"fattrace/internal/model"
	"fattrace/internal/rules"
	"fattrace/internal/store"
)

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r.URL.Query(), "limit", 50, 1, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.st.ListSnapshots(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []store.SnapshotInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list})
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	res, err := s.cachedDiff(r.Context(), r.URL.Query())
	if err != nil {
		writeDiffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// cachedDiff resolves and caches a snapshot-pair diff. It is shared by the diff
// endpoint and the unexplained/rule-preview browsers so they reuse one compute.
func (s *Server) cachedDiff(ctx context.Context, q url.Values) (model.DiffResult, error) {
	fromID, err := queryInt64(q, "from")
	if err != nil {
		return model.DiffResult{}, err
	}
	toID, err := queryInt64(q, "to")
	if err != nil {
		return model.DiffResult{}, err
	}
	topN, err := queryInt(q, "top", s.topN, 1, 500)
	if err != nil {
		return model.DiffResult{}, err
	}
	cross := q.Get("cross") == "1" || q.Get("cross") == "true"

	latest := s.latestCompleteSnapshotID(ctx)
	key := diffKey{from: fromID, to: toID, topN: topN, cross: cross, latest: latest}

	s.diffMu.Lock()
	res, ok := s.diffCache[key]
	if !ok {
		res, err = rules.Diff(ctx, s.st, diff.Options{
			FromID: fromID, ToID: toID, TopN: topN, CrossVolume: cross,
		}, s.rules)
		if err == nil {
			if len(s.diffCache) >= 8 {
				clear(s.diffCache)
			}
			s.diffCache[key] = res
		}
	}
	s.diffMu.Unlock()
	return res, err
}

func writeDiffError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrSnapshotNotFound) {
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}

// handleUnexplained serves a paginated, searchable view of the unexplained
// growth sample retained in memory. Read-only; it never writes rules.
func (s *Server) handleUnexplained(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.cachedDiff(r.Context(), q)
	if err != nil {
		writeDiffError(w, err)
		return
	}
	limit, err := queryInt(q, "limit", 100, 1, 1000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := queryInt(q, "offset", 0, 0, 1_000_000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := strings.ToLower(strings.TrimSpace(q.Get("q")))

	var all []model.UnexplainedItem
	var growth, explained int64
	var pct float64
	var unexplainedTotal, sample int
	if ex := res.Explanation; ex != nil {
		all = ex.AllUnexplained
		growth, explained, pct = ex.GrowthBytes, ex.ExplainedBytes, ex.ExplainedPct
		unexplainedTotal, sample = ex.UnexplainedTotal, len(ex.AllUnexplained)
	}
	items := make([]model.UnexplainedItem, 0, len(all))
	for _, it := range all {
		if filter != "" && !strings.Contains(strings.ToLower(it.Path), filter) {
			continue
		}
		items = append(items, it)
	}
	if q.Get("sort") == "path" {
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	}
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := items[offset:end]

	writeJSON(w, http.StatusOK, map[string]any{
		"items":             page,
		"total":             total,
		"offset":            offset,
		"limit":             limit,
		"unexplained_total": unexplainedTotal,
		"store_sample":      sample,
		"truncated":         unexplainedTotal > sample,
		"growth_bytes":      growth,
		"explained_bytes":   explained,
		"explained_pct":     pct,
		"from_snapshot":     res.FromSnapshot.ID,
		"to_snapshot":       res.ToSnapshot.ID,
	})
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rules)
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"categories": rules.KnownCategories()})
}

// handleRulePreview previews a candidate rule against the current unexplained
// sample. Read-only: it compiles the rule in memory and never writes rules.yaml.
func (s *Server) handleRulePreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.cachedDiff(r.Context(), q)
	if err != nil {
		writeDiffError(w, err)
		return
	}
	candidate := rules.Rule{
		ID:         q.Get("id"),
		Name:       q.Get("name"),
		Category:   q.Get("category"),
		Confidence: q.Get("confidence"),
		Match: rules.Match{
			PathGlobs: q["path_globs"],
			NameGlobs: q["name_globs"],
		},
	}
	var unexplained []model.UnexplainedItem
	var growth, explained int64
	if ex := res.Explanation; ex != nil {
		unexplained = ex.AllUnexplained
		growth, explained = ex.GrowthBytes, ex.ExplainedBytes
	}
	writeJSON(w, http.StatusOK, rules.Preview(s.rules, candidate, unexplained, growth, explained))
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

type dailyRow struct {
	model.DailySummary
	TopDirs []model.DailyTopDir `json:"top_dirs,omitempty"`
}

func (s *Server) handleDaily(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	volID, err := queryInt64(q, "volume")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := queryInt(q, "limit", 90, 1, 365)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	list, err := s.st.ListDailySummaries(r.Context(), volID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]dailyRow, 0, len(list))
	for _, d := range list {
		dirs, err := s.st.DailyTopDirs(r.Context(), d.VolumeID, d.Day, 5)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rows = append(rows, dailyRow{DailySummary: d, TopDirs: dirs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": rows})
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	rep, err := alerts.Evaluate(r.Context(), s.st, s.cfg.Alerts, alerts.Options{Persist: false})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.st.ListSnapshots(r.Context(), 1000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rep := health.Evaluate(snaps, s.cfg.Schedule.Roots,
		time.Duration(s.cfg.Alerts.StaleHours*float64(time.Hour)), time.Now())
	writeJSON(w, http.StatusOK, rep)
}

// latestCompleteSnapshotID participates in auto-pair cache keys so a fresh scan
// does not keep serving the previous default pair. Only complete rows count: the
// running row is inserted when a scan starts, so keying on it would leave the
// cache pinned to the pre-scan pair once that run finishes.
func (s *Server) latestCompleteSnapshotID(ctx context.Context) int64 {
	list, err := s.st.RecentComplete(ctx, "", 1)
	if err != nil || len(list) == 0 {
		return 0
	}
	return list[0].ID
}

func (s *Server) handleDir(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	snapID, err := queryInt64(q, "snapshot")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if snapID == 0 {
		writeError(w, http.StatusBadRequest, "snapshot is required")
		return
	}
	frn, err := queryUint64(q, "frn")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dirLimit, err := queryInt(q, "dir_limit", s.dirLimit, 1, 5000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fileLimit, err := queryInt(q, "file_limit", s.fileLimit, 1, 5000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.st.BrowseDir(r.Context(), snapID, frn, dirLimit, fileLimit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrSnapshotNotFound) || errors.Is(err, store.ErrDirNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleTrend(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	volID, err := queryInt64(q, "volume")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if volID == 0 {
		writeError(w, http.StatusBadRequest, "volume is required")
		return
	}
	limit, err := queryInt(q, "limit", 8, 1, 104)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	points, err := s.st.VolumeRootSnapshots(r.Context(), volID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if points == nil {
		points = []store.SnapshotInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"volume_id": volID, "points": points})
}

func queryInt(q url.Values, name string, def, min, max int) (int, error) {
	raw := q.Get(name)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min {
		return 0, fmt.Errorf("invalid %s: %q", name, raw)
	}
	if v > max {
		v = max
	}
	return v, nil
}

func queryInt64(q url.Values, name string) (int64, error) {
	raw := q.Get(name)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid %s: %q", name, raw)
	}
	return v, nil
}

func queryUint64(q url.Values, name string) (uint64, error) {
	raw := q.Get(name)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", name, raw)
	}
	return v, nil
}
