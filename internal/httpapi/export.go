package httpapi

import (
	"net/http"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func exportRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	for _, kind := range []string{"shifts", "cash"} {
		mux.HandleFunc("GET /api/v1/exports/"+kind+".csv", func(w http.ResponseWriter, r *http.Request) {
			q, err := historyQuery(r)
			if err != nil {
				a.failure(w, err)
				return
			}
			for key := range q {
				if key != "date_from" && key != "date_to" && key != "owner_id" {
					a.failure(w, auth.Fail("INVALID_INPUT", 400))
					return
				}
			}
			body, err := s.ExportCSV(r.Context(), a.request(w, r), shifts.ExportFilter{DateFrom: q.Get("date_from"), DateTo: q.Get("date_to"), OwnerID: q.Get("owner_id")}, kind == "cash")
			if err != nil {
				a.failure(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+kind+`.csv"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		})
	}
}
