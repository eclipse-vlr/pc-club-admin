package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"strings"
	"time"
)

type authHTTP struct {
	service *auth.Service
	config  config.Config
}

func (a authHTTP) cookieName(kind string) string {
	if strings.HasPrefix(a.config.Origin, "https://") {
		return "__Host-pcclub_" + kind
	}
	return "pcclub_" + kind
}
func (a authHTTP) cookie(r *http.Request, kind string) string {
	c, err := r.Cookie(a.cookieName(kind))
	if err != nil {
		return ""
	}
	return c.Value
}
func (a authHTTP) setCookie(w http.ResponseWriter, kind, value string, seconds int) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(kind), Value: value, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(a.config.Origin, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: seconds})
}
func (a authHTTP) request(w http.ResponseWriter, r *http.Request) auth.Request {
	source, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		source = r.RemoteAddr
	}
	return auth.Request{SessionToken: a.cookie(r, "session"), DeviceToken: a.cookie(r, "device"), CSRF: r.Header.Get("X-CSRF-Token"), ActorID: r.Header.Get("X-Actor-ID"), RequestID: w.Header().Get("X-Request-ID"), Source: source}
}
func (a authHTTP) failure(w http.ResponseWriter, err error) {
	code, status := "SERVICE_UNAVAILABLE", 503
	var failure *auth.Failure
	if errors.As(err, &failure) {
		code, status = failure.Code, failure.Status
	} else if errors.Is(err, auth.ErrMissing) {
		code, status = "NOT_FOUND", 404
	}
	messages := map[string]string{"EXPORT_TOO_LARGE": "В выгрузке больше 5000 смен. Уменьшите период или выберите сотрудника", "INVALID_CREDENTIALS": "Неверные данные входа", "AUTH_BLOCKED": "Слишком много попыток. Повторите через 5 минут", "DEVICE_REQUIRED": "Нужен зарегистрированный браузер клуба", "AUTH_REQUIRED": "Войдите в приложение", "SESSION_CHANGED": "Вы вошли как другой сотрудник. Обновите страницу", "CSRF_INVALID": "Проверка запроса не пройдена. Обновите страницу", "FORBIDDEN": "Нет права на это действие", "VERSION_CONFLICT": "Данные изменены. Обновите страницу", "LAST_MANAGER": "Нельзя отключить последнего управляющего", "INVALID_INPUT": "Проверьте обязательные поля", "ALREADY_EXISTS": "Такая запись уже существует", "INVALID_ENROLLMENT": "Код недействителен, использован или истёк"}
	messages["INVALID_STATE"] = "Этот этап чек-листа сейчас недоступен для изменения"
	messages["IDEMPOTENCY_CONFLICT"] = "Ключ повтора уже использован с другими данными"
	messages["ACTIVE_SHIFT_EXISTS"] = "Уже существует смена или черновик, препятствующий этой операции"
	messages["CHECKLIST_TEMPLATE_INVALID"] = "Шаблон чек-листа не настроен"
	messages["CASH_POLICY_UNVERIFIED"] = "Работа со сменами в production запрещена до проверки кассовой политики"
	messages["USER_HAS_UNFINISHED_SHIFT"] = "У сотрудника есть незавершённый черновик или ответственная смена"
	messages["CASH_INPUT_MISSING"] = "Введите сумму явно; пустое поле не равно нулю"
	messages["CASH_MISMATCH"] = "Расхождение кассы. Приём или передача запрещены"
	messages["CASH_OVERFLOW"] = "Сумма выходит за границы int64"
	messages["CASH_INVALID_AMOUNT"] = "Недопустимая денежная сумма"
	messages["REAUTH_REQUIRED"] = "Повторите личный PIN: подтверждение истекло, использовано или относится к другим данным"
	messages["COLLECTION_ALREADY_REVERSED"] = "Эта инкассация уже отменена"
	messages["CHECKLIST_INCOMPLETE"] = "Чек-лист заполнен не полностью"
	messages["CHECKLIST_ANSWER_BLOCKS"] = "Один из ответов блокирует завершение чек-листа"
	messages["NOTE_REQUIRED"] = "Не хватает связанной заметки"
	messages["SELF_HANDOVER"] = "Передать смену самому себе нельзя"
	messages["HANDOVER_CHANGED"] = "Передача изменена или отозвана. Проверьте данные заново"
	messages["RECEIPT_MISMATCH"] = "Ваш пересчёт не совпадает с передаваемой суммой. Приём запрещён"
	messages["NOTE_ACK_REQUIRED"] = "Подтвердите ознакомление со всеми замечаниями сдающего"
	messages["DASHBOARD_INCONSISTENT"] = "Не удалось получить согласованное состояние клуба. Обновите данные"
	message := messages[code]
	if message == "" {
		message = "Действие недоступно"
	}
	if status == 429 {
		w.Header().Set("Retry-After", "300")
	}
	payload := map[string]any{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")}
	if failure != nil && failure.Details != nil {
		payload["details"] = failure.Details
	}
	writeJSON(w, status, map[string]any{"error": payload})
}
func (a authHTTP) mutate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie := a.cookie(r, "csrf")
		token := r.Header.Get("X-CSRF-Token")
		if r.Header.Get("Origin") != a.config.Origin || len(token) != 64 || !auth.Equal(cookie, token) {
			a.failure(w, auth.Fail("CSRF_INVALID", 403))
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			a.failure(w, auth.Fail("INVALID_INPUT", 400))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		next(w, r)
	}
}
func decode(r *http.Request, v any) error {
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return auth.Fail("INVALID_INPUT", 400)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return auth.Fail("INVALID_INPUT", 400)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return auth.Fail("INVALID_INPUT", 400)
	}
	object := json.NewDecoder(bytes.NewReader(raw))
	object.DisallowUnknownFields()
	if err := object.Decode(v); err != nil {
		return auth.Fail("INVALID_INPUT", 400)
	}
	return nil
}

func authRoutes(mux *http.ServeMux, c config.Config, s *auth.Service) {
	a := authHTTP{s, c}
	mux.HandleFunc("GET /api/v1/auth/csrf", func(w http.ResponseWriter, r *http.Request) {
		token := a.cookie(r, "csrf")
		if len(token) != 64 {
			token = auth.Token()
			a.setCookie(w, "csrf", token, 14*3600)
		}
		writeJSON(w, 200, map[string]string{"csrf_token": token})
	})
	mux.HandleFunc("GET /api/v1/auth/admin-options", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.AdminOptions(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	for _, mode := range []string{"pin", "manager"} {
		mux.HandleFunc("POST /api/v1/auth/"+mode+"-login", a.mutate(func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				UserID   string `json:"user_id"`
				PIN      string `json:"pin"`
				Login    string `json:"login"`
				Password string `json:"password"`
			}
			if err := decode(r, &in); err != nil {
				a.failure(w, err)
				return
			}
			subject, secret := in.Login, in.Password
			if mode == "pin" {
				subject, secret = in.UserID, in.PIN
			}
			if len(subject) > 100 || len(secret) > 256 {
				a.failure(w, auth.Fail("INVALID_INPUT", 400))
				return
			}
			result, err := s.Login(r.Context(), a.request(w, r), subject, secret, mode == "pin")
			if err != nil {
				a.setCookie(w, "session", "", -1)
				a.failure(w, err)
				return
			}
			a.setCookie(w, "session", result.Token, 14*3600)
			a.setCookie(w, "csrf", result.CSRF, 14*3600)
			writeJSON(w, 200, map[string]any{"identity": result.Identity, "csrf_token": result.CSRF})
		}))
	}
	mux.HandleFunc("GET /api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		id, err := s.Me(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, id)
	})
	mux.HandleFunc("POST /api/v1/auth/logout", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		if err := decode(r, &struct{}{}); err != nil {
			a.failure(w, err)
			return
		}
		if err := s.Logout(r.Context(), a.request(w, r)); err != nil {
			a.failure(w, err)
			return
		}
		a.setCookie(w, "session", "", -1)
		a.setCookie(w, "csrf", "", -1)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/v1/auth/activity", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		if err := decode(r, &struct{}{}); err != nil {
			a.failure(w, err)
			return
		}
		if err := s.Activity(r.Context(), a.request(w, r)); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/v1/auth/reauth", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PIN      string `json:"pin"`
			Action   string `json:"action"`
			Target   string `json:"target_id"`
			Revision int64  `json:"revision"`
		}
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		token, err := s.Reauth(r.Context(), a.request(w, r), in.PIN, in.Action, in.Target, in.Revision)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"proof": token, "expires_in": 120})
	}))
	mux.HandleFunc("GET /api/v1/users", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Users(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("POST /api/v1/users", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in auth.UserInput
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		u, err := s.CreateUser(r.Context(), a.request(w, r), in)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 201, u)
	}))
	mux.HandleFunc("PATCH /api/v1/users/{id}", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name    string `json:"display_name"`
			Active  *bool  `json:"active"`
			Version int64  `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		if in.Active == nil {
			a.failure(w, auth.Fail("INVALID_INPUT", 400))
			return
		}
		u, err := s.UpdateUser(r.Context(), a.request(w, r), r.PathValue("id"), in.Name, *in.Active, in.Version)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, u)
	}))
	mux.HandleFunc("POST /api/v1/users/{id}/reset-pin", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PIN     string `json:"pin"`
			Version int64  `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		if err := s.ResetPIN(r.Context(), a.request(w, r), r.PathValue("id"), in.PIN, in.Version); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/v1/devices/enrollment", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Password string `json:"password"`
		}
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		code, err := s.Enroll(r.Context(), a.request(w, r), in.Password)
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 201, map[string]any{"code": code, "expires_in": 600})
	}))
	mux.HandleFunc("POST /api/v1/devices/register", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code  string `json:"code"`
			Label string `json:"label"`
		}
		if err := decode(r, &in); err != nil {
			a.failure(w, err)
			return
		}
		token, err := s.RegisterDevice(r.Context(), a.request(w, r), in.Code, in.Label)
		if err != nil {
			a.failure(w, err)
			return
		}
		a.setCookie(w, "device", token, int((30 * 24 * time.Hour).Seconds()))
		a.setCookie(w, "session", "", -1)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/v1/devices", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Devices(r.Context(), a.request(w, r))
		if err != nil {
			a.failure(w, err)
			return
		}
		writeJSON(w, 200, list)
	})
	mux.HandleFunc("POST /api/v1/devices/{id}/revoke", a.mutate(func(w http.ResponseWriter, r *http.Request) {
		if err := decode(r, &struct{}{}); err != nil {
			a.failure(w, err)
			return
		}
		if err := s.RevokeDevice(r.Context(), a.request(w, r), r.PathValue("id")); err != nil {
			a.failure(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
