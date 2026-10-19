package httpapi

import (
	"net/http"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
	"time"
)

func auditRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	mux.HandleFunc("GET /api/v1/audit-events", func(w http.ResponseWriter, r *http.Request) {
		q, err := historyQuery(r)
		if err != nil {
			a.failure(w, err)
			return
		}
		for key := range q {
			switch key {
			case "actor_id", "entity_id", "action", "cursor", "limit", "from", "to":
			default:
				a.failure(w, auth.Fail("INVALID_INPUT", 400))
				return
			}
		}
		limit, err := historyLimit(q)
		if err != nil {
			a.failure(w, err)
			return
		}
		var from, to *time.Time
		for key, target := range map[string]**time.Time{"from": &from, "to": &to} {
			if raw := q.Get(key); raw != "" {
				parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
				if parseErr != nil {
					a.failure(w, auth.Fail("INVALID_INPUT", 400))
					return
				}
				*target = &parsed
			}
		}
		page, err := s.Audit(r.Context(), a.request(w, r), shifts.AuditFilter{ActorID: q.Get("actor_id"), EntityID: q.Get("entity_id"), Action: q.Get("action"), Cursor: q.Get("cursor"), Limit: limit, From: from, To: to})
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, page)
	})
}
