package httpapi

import (
	"net/http"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func shiftRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	cashRoutes(mux, c, s)
	handoverRoutes(mux, c, s)
	historyRoutes(mux, c, s)
	dashboardRoutes(mux, c, s)
	mux.HandleFunc("POST /api/v1/shifts/bootstrap", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.BootstrapInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		shift, replay, err := s.Bootstrap(r.Context(), a.request(w, r), in, r.Header.Get("Idempotency-Key"))
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, 201, shift)
	}))
	mux.HandleFunc("GET /api/v1/shifts/current", func(w http.ResponseWriter, r *http.Request) {
		shift, err := s.Current(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"shift": shift})
	})
	mux.HandleFunc("GET /api/v1/shifts/{id}", func(w http.ResponseWriter, r *http.Request) {
		shift, err := s.Get(r.Context(), a.request(w, r), r.PathValue("id"))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, shift)
	})
	mux.HandleFunc("GET /api/v1/shifts/{id}/readiness", func(w http.ResponseWriter, r *http.Request) {
		result, err := s.Readiness(r.Context(), a.request(w, r), r.PathValue("id"))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, result)
	})
	mux.HandleFunc("GET /api/v1/shifts/{id}/excel-sync", func(w http.ResponseWriter, r *http.Request) {
		shift, err := s.Get(r.Context(), a.request(w, r), r.PathValue("id"))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, shift.ExcelSync)
	})
	mux.HandleFunc("PATCH /api/v1/shifts/{id}/checklist/{code}", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.AnswerInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		shift, err := s.Answer(r.Context(), a.request(w, r), r.PathValue("id"), r.PathValue("code"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, shift)
	}))
	mux.HandleFunc("POST /api/v1/shifts/{id}/notes", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.NoteInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		note, err := s.Note(r.Context(), a.request(w, r), r.PathValue("id"), "", in)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 201, note)
	}))
	mux.HandleFunc("PATCH /api/v1/shifts/{id}/notes/{note_id}", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.NoteInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		note, err := s.Note(r.Context(), a.request(w, r), r.PathValue("id"), r.PathValue("note_id"), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, note)
	}))
}
