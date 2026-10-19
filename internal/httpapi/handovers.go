package httpapi

import (
	"net/http"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/shifts"
)

func handoverRoutes(mux *http.ServeMux, c config.Config, s *shifts.Service) {
	a := authHTTP{config: c}
	respond := func(w http.ResponseWriter, h shifts.Handover, replay bool, err error, code int) {
		if err != nil {
			a.failure(w, err)
			return
		}
		if replay {
			w.Header().Set("Idempotency-Replayed", "true")
		}
		writeJSON(w, code, h)
	}
	mux.HandleFunc("POST /api/v1/shifts/{id}/handovers", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.PrepareInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		h, replay, err := s.Prepare(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		respond(w, h, replay, err, 201)
	}))
	mux.HandleFunc("GET /api/v1/handovers/{id}", func(w http.ResponseWriter, r *http.Request) {
		h, err := s.Handover(r.Context(), a.request(w, r), r.PathValue("id"))
		respond(w, h, false, err, 200)
	})
	mux.HandleFunc("POST /api/v1/handovers/{id}/cancel", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.CancelInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		h, replay, err := s.Cancel(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		respond(w, h, replay, err, 200)
	}))
	mux.HandleFunc("PATCH /api/v1/handovers/{id}/receipt", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.ReceiptInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		h, replay, err := s.Receipt(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		respond(w, h, replay, err, 200)
	}))
	mux.HandleFunc("POST /api/v1/handovers/{id}/acknowledgements", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.AcknowledgeInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		h, replay, err := s.Acknowledge(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		respond(w, h, replay, err, 200)
	}))
	mux.HandleFunc("POST /api/v1/handovers/{id}/accept", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in shifts.AcceptInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		h, replay, err := s.Accept(r.Context(), a.request(w, r), r.PathValue("id"), r.Header.Get("Idempotency-Key"), in)
		respond(w, h, replay, err, 200)
	}))
}
