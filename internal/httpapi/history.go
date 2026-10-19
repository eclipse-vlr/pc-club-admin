package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func historyQuery(r *http.Request) (url.Values, error) {
	if len(r.URL.RawQuery) > 4096 {
		return nil, auth.Fail("INVALID_INPUT", 400)
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, auth.Fail("INVALID_INPUT", 400)
	}
	for _, items := range values {
		if len(items) != 1 {
			return nil, auth.Fail("INVALID_INPUT", 400)
		}
	}
	return values, nil
}
func historyLimit(values url.Values) (int, error) {
	raw := values.Get("limit")
	if raw == "" {
		return 50, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 {
		return 0, auth.Fail("INVALID_INPUT", 400)
	}
	return value, nil
}
func historyRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	mux.HandleFunc("GET /api/v1/shifts", func(w http.ResponseWriter, r *http.Request) {
		q, err := historyQuery(r)
		if err != nil {
			a.failure(w, err)
			return
		}
		limit, err := historyLimit(q)
		if err != nil {
			a.failure(w, err)
			return
		}
		result, err := s.History(r.Context(), a.request(w, r), shifts.HistoryFilter{DateFrom: q.Get("date_from"), DateTo: q.Get("date_to"), OwnerID: q.Get("owner_id"), Kind: q.Get("kind"), Status: q.Get("status"), Limit: limit, Cursor: q.Get("cursor")})
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/v1/shifts/{id}/handovers", func(w http.ResponseWriter, r *http.Request) {
		q, err := historyQuery(r)
		if err != nil {
			a.failure(w, err)
			return
		}
		limit, err := historyLimit(q)
		if err != nil {
			a.failure(w, err)
			return
		}
		result, err := s.HandoverHistory(r.Context(), a.request(w, r), r.PathValue("id"), q.Get("cursor"), limit)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
}
