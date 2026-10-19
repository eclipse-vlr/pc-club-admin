package httpapi

import (
	"net/http"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func cashRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	mux.HandleFunc("POST /api/v1/shifts/{id}/activate", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.ActivateInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		result, replay, err := s.Activate(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("PATCH /api/v1/shifts/{id}/cash", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.CashInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		result, replay, err := s.PatchCash(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/v1/shifts/{id}/cash/collections", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.CollectionInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		result, replay, err := s.Collect(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, 201, result)
	}))
	mux.HandleFunc("POST /api/v1/shifts/{id}/cash/collections/{movement_id}/reverse", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.ReverseInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		result, replay, err := s.ReverseCollection(r.Context(), a.request(w, r), r.PathValue("id"), r.PathValue("movement_id"), r.Header.Get("Idempotency-Key"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, 201, result)
	}))
}
