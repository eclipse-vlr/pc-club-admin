package httpapi

import (
	"net/http"

	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func dashboardRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	mux.HandleFunc("GET /api/v1/manager/dashboard", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.Dashboard(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
}
