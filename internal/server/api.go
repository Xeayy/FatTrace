package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"fattrace/internal/diff"
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
	q := r.URL.Query()
	fromID, err := queryInt64(q, "from")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	toID, err := queryInt64(q, "to")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	topN, err := queryInt(q, "top", s.topN, 1, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cross := q.Get("cross") == "1" || q.Get("cross") == "true"

	latest := s.latestSnapshotID(r.Context())
	key := diffKey{from: fromID, to: toID, topN: topN, cross: cross, latest: latest}

	s.diffMu.Lock()
	res, ok := s.diffCache[key]
	if !ok {
		res, err = rules.Diff(r.Context(), s.st, diff.Options{
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

	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, store.ErrSnapshotNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rules)
}

// latestSnapshotID participates in auto-pair cache keys so a fresh scan does
// not keep serving the previous default pair.
func (s *Server) latestSnapshotID(ctx context.Context) int64 {
	list, err := s.st.ListSnapshots(ctx, 1)
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
