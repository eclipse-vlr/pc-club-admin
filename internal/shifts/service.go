package shifts

import (
	"context"
	"encoding/json"
	"errors"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/checklists"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	repo       Repository
	auth       *auth.Service
	timezone   string
	production bool
}

func New(repo Repository, access *auth.Service, timezone string, production bool) *Service {
	return &Service{repo, access, timezone, production}
}
func (s *Service) within(ctx context.Context, r auth.Request, mutation bool, fn func(Tx, auth.Identity) error) error {
	var actor auth.Identity
	err := s.repo.Within(ctx, func(tx Tx) error {
		id, err := s.auth.Authorize(tx, r, "", mutation)
		if err != nil {
			return err
		}
		actor = id
		if mutation && s.production {
			return auth.Fail("CASH_POLICY_UNVERIFIED", 503)
		}
		return fn(tx, id)
	})
	var failure *auth.Failure
	if errors.As(err, &failure) || errors.Is(err, auth.ErrMissing) {
		reason := "NOT_FOUND"
		if failure != nil {
			reason = failure.Code
		}
		if e := s.repo.Within(ctx, func(tx Tx) error {
			return event(tx, r, actor, "shift_request_rejected", "", false, reason, func() map[string]any {
				if failure != nil {
					return failure.Details
				}
				return nil
			}())
		}); e != nil {
			return e
		}
	}
	return err
}
func event(tx Tx, r auth.Request, id auth.Identity, action, entity string, success bool, reason string, details map[string]any) error {
	return tx.Audit(auth.Event{ActorID: id.User.ID, DeviceID: id.Session.DeviceID, Action: action, EntityID: entity, RequestID: r.RequestID, Success: success, Reason: reason, Details: details})
}
func readable(shift Shift, id auth.Identity) bool {
	return id.User.Has("manager") || id.User.Has("admin") && id.User.ID == shift.OwnerID && id.DeviceTrusted && id.Session.DeviceID != ""
}
func writable(shift Shift, id auth.Identity) error {
	if !id.User.Has("admin") || id.User.ID != shift.OwnerID {
		return auth.Fail("FORBIDDEN", 403)
	}
	if id.Session.DeviceID == "" || !id.DeviceTrusted {
		return auth.Fail("DEVICE_REQUIRED", 403)
	}
	if shift.ParentID != "" && shift.Status == "opening" && (!id.PINSession || id.Session.DeviceID != shift.HandoverDeviceID) {
		return auth.Fail("DEVICE_REQUIRED", 403)
	}
	if shift.Status != "opening" && shift.Status != "active" {
		return auth.Fail("INVALID_STATE", 409)
	}
	return nil
}

func editablePhase(phase, status string) bool {
	return phase == "acceptance" && status == "opening" || phase == "closing" && status == "active"
}

// PlannedBounds uses the club's calendar, including an overnight business date.
func PlannedBounds(date, kind, timezone string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	day, err := time.ParseInLocation("2006-01-02", date, location)
	if err != nil || day.Format("2006-01-02") != date {
		return time.Time{}, time.Time{}, auth.Fail("INVALID_INPUT", 400)
	}
	hour := 9
	if kind == "night" {
		hour = 21
	} else if kind != "day" {
		return time.Time{}, time.Time{}, auth.Fail("INVALID_INPUT", 400)
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, location)
	end := time.Date(day.Year(), day.Month(), day.Day(), 21, 0, 0, 0, location)
	if kind == "night" {
		end = time.Date(day.Year(), day.Month(), day.Day()+1, 9, 0, 0, 0, location)
	}
	return start, end, nil
}
func validKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func (s *Service) Bootstrap(ctx context.Context, r auth.Request, in BootstrapInput, key string) (Shift, bool, error) {
	if !validKey(key) || !auth.ValidID(in.OwnerID) || in.InitialCash == nil || *in.InitialCash < 0 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 1 || utf8.RuneCountInString(in.Reason) > 4000 {
		return Shift{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	start, end, err := PlannedBounds(in.BusinessDate, in.Kind, s.timezone)
	if err != nil {
		return Shift{}, false, err
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return Shift{}, false, err
	}
	hash := auth.Digest(string(payload))
	var shift Shift
	replay := false
	err = s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
		if !id.User.Has("manager") {
			return auth.Fail("FORBIDDEN", 403)
		}
		previous, body, found, err := tx.Idempotency(id.User.ID, key)
		if err != nil {
			return err
		}
		if found {
			if previous != hash {
				return auth.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			replay = true
			return json.Unmarshal(body, &shift)
		}
		exists, err := tx.AnyShift()
		if err != nil {
			return err
		}
		if exists {
			return auth.Fail("ACTIVE_SHIFT_EXISTS", 409)
		}
		owner, err := tx.User(in.OwnerID)
		if err != nil {
			return err
		}
		if !owner.Active || !owner.Has("admin") {
			return auth.Fail("INVALID_INPUT", 400)
		}
		shift = Shift{OwnerID: in.OwnerID, BusinessDate: in.BusinessDate, Kind: in.Kind, Status: "opening", PlannedStart: start, PlannedEnd: end, Version: 1, InitialCash: in.InitialCash, BootstrapReason: strings.TrimSpace(in.Reason)}
		if err := tx.CreateShift(&shift, id.User.ID); err != nil {
			return err
		}
		body, err = json.Marshal(shift)
		if err != nil {
			return err
		}
		if err := tx.SaveIdempotency(id.User.ID, key, hash, body); err != nil {
			return err
		}
		return event(tx, r, id, "shift_bootstrapped", shift.ID, true, "", map[string]any{"owner_id": shift.OwnerID, "opening_cash_kopecks": *shift.InitialCash, "reason": shift.BootstrapReason, "template_version": shift.TemplateVersion})
	})
	return shift, replay, err
}
func (s *Service) Get(ctx context.Context, r auth.Request, shiftID string) (Shift, error) {
	if !auth.ValidID(shiftID) {
		return Shift{}, auth.Fail("INVALID_INPUT", 400)
	}
	var shift Shift
	err := s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		var err error
		shift, err = tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		allowed, err := s.shiftReadable(tx, shift, id)
		if err != nil {
			return err
		}
		if !allowed {
			return auth.ErrMissing
		}
		return nil
	})
	return shift, err
}
func (s *Service) Current(ctx context.Context, r auth.Request) (*Shift, error) {
	var result *Shift
	err := s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if !id.User.Has("manager") && (!id.User.Has("admin") || id.Session.DeviceID == "" || !id.DeviceTrusted) {
			return auth.Fail("DEVICE_REQUIRED", 403)
		}
		shiftID, err := tx.CurrentShift(id.User.ID, id.User.Has("manager"))
		if errors.Is(err, auth.ErrMissing) {
			return nil
		}
		if err != nil {
			return err
		}
		shift, err := tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		result = &shift
		return nil
	})
	return result, err
}
func (s *Service) Answer(ctx context.Context, r auth.Request, shiftID, code string, in AnswerInput) (Shift, error) {
	if !auth.ValidID(shiftID) || !checklists.ValidAnswer(in.Answer) || utf8.RuneCountInString(in.Comment) > 4000 {
		return Shift{}, auth.Fail("INVALID_INPUT", 400)
	}
	var shift Shift
	err := s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
		var err error
		shift, err = tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		if err := writable(shift, id); err != nil {
			return err
		}
		for _, entry := range shift.Entries {
			if entry.Code != code {
				continue
			}
			if entry.Version != in.Version {
				return auth.Fail("VERSION_CONFLICT", 409)
			}
			if !editablePhase(entry.Phase, shift.Status) {
				return auth.Fail("INVALID_STATE", 409)
			}
			if in.EquipmentIssue && !entry.EquipmentCheck {
				return auth.Fail("INVALID_INPUT", 400)
			}
			before := map[string]any{"answer": entry.Answer, "comment": entry.Comment, "equipment_issue": entry.EquipmentIssue, "version": entry.Version}
			entry.Answer = in.Answer
			entry.Comment = in.Comment
			entry.EquipmentIssue = in.EquipmentIssue
			entry.Version++
			if err := tx.SaveAnswer(shift.ID, entry, id.User.ID); err != nil {
				return err
			}
			if err := tx.BumpShift(shift.ID); err != nil {
				return err
			}
			if err := event(tx, r, id, "checklist_answered", shift.ID, true, "", map[string]any{"code": code, "before": before, "after": map[string]any{"answer": entry.Answer, "comment": entry.Comment, "equipment_issue": entry.EquipmentIssue, "version": entry.Version}}); err != nil {
				return err
			}
			shift, err = tx.LoadShift(shift.ID)
			return err
		}
		return auth.ErrMissing
	})
	return shift, err
}
func (s *Service) Note(ctx context.Context, r auth.Request, shiftID, noteID string, in NoteInput) (checklists.Note, error) {
	if !auth.ValidID(shiftID) || (noteID != "" && !auth.ValidID(noteID)) || utf8.RuneCountInString(strings.TrimSpace(in.Text)) < 1 || utf8.RuneCountInString(in.Text) > 4000 || utf8.RuneCountInString(in.Object) > 200 || (in.Category != "equipment" && in.Category != "conflict" && in.Category != "general") {
		return checklists.Note{}, auth.Fail("INVALID_INPUT", 400)
	}
	var note checklists.Note
	err := s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
		shift, err := tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		if err := writable(shift, id); err != nil {
			return err
		}
		var item *checklists.Entry
		for _, entry := range shift.Entries {
			if entry.Code == in.Code {
				e := entry
				item = &e
				break
			}
		}
		if in.Category == "equipment" && (strings.TrimSpace(in.Object) == "" || item == nil || !item.EquipmentCheck) || in.Category == "conflict" && (item == nil || item.Code != "conflicts_recorded") || in.Code != "" && item == nil {
			return auth.Fail("INVALID_INPUT", 400)
		}
		if item != nil && !editablePhase(item.Phase, shift.Status) {
			return auth.Fail("INVALID_STATE", 409)
		}
		note = checklists.Note{ID: noteID, Code: in.Code, Category: in.Category, Object: strings.TrimSpace(in.Object), Text: strings.TrimSpace(in.Text), CreatedBy: id.User.ID, Version: 1}
		var before any
		if noteID != "" {
			found := false
			for _, old := range shift.Notes {
				if old.ID == noteID {
					found = true
					if old.Version != in.Version {
						return auth.Fail("VERSION_CONFLICT", 409)
					}
					// Authorize the persisted link too: changing the new link must
					// not turn an accepted note into an editable closing/general note.
					for _, entry := range shift.Entries {
						if entry.Code == old.Code && !editablePhase(entry.Phase, shift.Status) {
							return auth.Fail("INVALID_STATE", 409)
						}
					}
					note.CreatedBy = old.CreatedBy
					note.Version = old.Version + 1
					before = old
					break
				}
			}
			if !found {
				return auth.ErrMissing
			}
		}
		if err := tx.SaveNote(shiftID, &note, id.User.ID); err != nil {
			return err
		}
		if err := tx.BumpShift(shiftID); err != nil {
			return err
		}
		return event(tx, r, id, "shift_note_saved", shiftID, true, "", map[string]any{"before": before, "after": note})
	})
	return note, err
}
func (s *Service) Readiness(ctx context.Context, r auth.Request, shiftID string) (Readiness, error) {
	shift, err := s.Get(ctx, r, shiftID)
	if err != nil {
		return Readiness{}, err
	}
	acceptance := checklists.Evaluate(shift.Entries, shift.Notes, "acceptance")
	closing := checklists.Evaluate(shift.Entries, shift.Notes, "closing")
	blockers := append([]checklists.Blocker{}, acceptance.Blockers...)
	if shift.Status != "opening" {
		blockers = append(blockers, closing.Blockers...)
		if shift.Cash != nil {
			for _, b := range shift.Cash.Summary.Blockers {
				blockers = append(blockers, checklists.Blocker{Code: b.Code, Message: b.Message})
			}
		}
	}
	canActivate := shift.Status == "opening" && shift.ParentID == "" && len(acceptance.Blockers) == 0 && shift.InitialCash != nil && !s.production
	if shift.Status == "opening" {
		blockers = append(blockers, checklists.Blocker{Code: "OPENING_COUNT_REQUIRED", Message: "Для окончательного приёма введите собственный пересчёт и свежий PIN"})
	} else if shift.Status != "active" {
		blockers = append(blockers, checklists.Blocker{Code: "HANDOVER_PENDING", Message: "Передача ожидает личного приёма или отзыва сдающим"})
	}
	if s.production {
		blockers = append(blockers, checklists.Blocker{Code: "CASH_POLICY_UNVERIFIED", Message: "Кассовая политика не подтверждена для production"})
	}
	result := Readiness{Version: shift.Version, Acceptance: acceptance, Closing: closing, CanActivate: canActivate, CanPrepare: shift.Status == "active" && len(blockers) == 0 && !s.production, Blockers: blockers}
	if shift.Cash != nil {
		result.Cash = &shift.Cash.Summary
	}
	return result, nil
}
