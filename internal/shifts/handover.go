package shifts

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/cash"
	"pc-club-admin/internal/checklists"
)

type Handover struct {
	ID            string     `json:"id"`
	FromShiftID   string     `json:"from_shift_id"`
	ToShiftID     string     `json:"to_shift_id"`
	FromUserID    string     `json:"from_user_id"`
	ToUserID      string     `json:"to_user_id"`
	DeviceID      string     `json:"-"`
	Revision      int64      `json:"revision"`
	Version       int64      `json:"version"`
	Status        string     `json:"status"`
	SourceVersion int64      `json:"source_version"`
	Snapshot      Shift      `json:"snapshot"`
	Expected      int64      `json:"expected_cash_kopecks"`
	Counted       *int64     `json:"counted_cash_kopecks"`
	Acknowledged  []string   `json:"acknowledged_note_ids"`
	PreparedAt    time.Time  `json:"prepared_at"`
	AcceptedAt    *time.Time `json:"accepted_at"`
	CancelledAt   *time.Time `json:"cancelled_at"`
	Boundary      *time.Time `json:"actual_boundary_at"`
	Reason        string     `json:"cancellation_reason"`
}
type PrepareInput struct {
	ReceiverID string `json:"receiver_id"`
	Version    int64  `json:"version"`
	Proof      string `json:"reauth_proof"`
}
type CancelInput struct {
	Version int64  `json:"version"`
	Reason  string `json:"reason"`
}
type ReceiptInput struct {
	Revision int64  `json:"revision"`
	Version  int64  `json:"version"`
	Counted  *int64 `json:"counted_cash_kopecks"`
}
type AcknowledgeInput struct {
	Revision int64    `json:"revision"`
	Version  int64    `json:"version"`
	NoteIDs  []string `json:"note_ids"`
}
type AcceptInput struct {
	Revision     int64  `json:"revision"`
	Version      int64  `json:"version"`
	ShiftVersion int64  `json:"shift_version"`
	Proof        string `json:"reauth_proof"`
}

func completion(shift Shift, phase string) error {
	blockers := checklists.Evaluate(shift.Entries, shift.Notes, phase).Blockers
	if len(blockers) > 0 {
		return &auth.Failure{Code: blockers[0].Code, Status: 422, Details: map[string]any{"blockers": blockers}}
	}
	return nil
}
func closingCash(shift Shift) (int64, error) {
	if shift.Cash == nil || shift.Cash.Session.OpeningConfirmedAt == nil {
		return 0, auth.Fail("OPENING_COUNT_REQUIRED", 422)
	}
	summary := cash.Evaluate(shift.Cash.Session)
	if len(summary.Blockers) > 0 {
		return 0, &auth.Failure{Code: summary.Blockers[0].Code, Status: 422, Details: map[string]any{"blockers": summary.Blockers, "expected_kopecks": summary.Expected, "counted_kopecks": shift.Cash.Session.Counted, "difference_kopecks": summary.Difference}}
	}
	if summary.Expected == nil {
		return 0, auth.Fail("CASH_INPUT_MISSING", 422)
	}
	return *summary.Expected, nil
}

func (s *Service) Prepare(ctx context.Context, r auth.Request, shiftID, key string, in PrepareInput) (Handover, bool, error) {
	if !auth.ValidID(shiftID) || !auth.ValidID(in.ReceiverID) || !validKey(key) {
		return Handover{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	var result Handover
	replay := false
	encoded, _ := json.Marshal(in)
	hash := auth.Digest(string(encoded))
	route := "POST /api/v1/shifts/" + shiftID + "/handovers"
	err := s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
		shift, err := tx.LoadShift(shiftID)
		if err != nil {
			return err
		}
		if err := cashOwner(shift, id); err != nil {
			return err
		}
		previous, body, found, err := tx.OperationResult(id.User.ID, route, key)
		if err != nil {
			return err
		}
		if found {
			if previous != hash {
				return auth.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			replay = true
			return json.Unmarshal(body, &result)
		}
		if shift.Status != "active" {
			return auth.Fail("INVALID_STATE", 409)
		}
		if shift.Version != in.Version {
			return auth.Fail("VERSION_CONFLICT", 409)
		}
		if in.ReceiverID == id.User.ID {
			return auth.Fail("SELF_HANDOVER", 422)
		}
		receiver, err := tx.User(in.ReceiverID)
		if err != nil {
			return err
		}
		if !receiver.Active || !receiver.Has("admin") || receiver.PINHash == "" {
			return auth.Fail("INVALID_INPUT", 400)
		}
		unfinished, err := tx.UserHasUnfinishedShift(receiver.ID)
		if err != nil {
			return err
		}
		if unfinished {
			return auth.Fail("ACTIVE_SHIFT_EXISTS", 409)
		}
		if err := completion(shift, "acceptance"); err != nil {
			return err
		}
		if err := completion(shift, "closing"); err != nil {
			return err
		}
		amount, err := closingCash(shift)
		if err != nil {
			return err
		}
		if err := consume(tx, id, in.Proof, "prepare_handover", shift.ID, shift.Version); err != nil {
			return err
		}
		// The next planned interval follows the outgoing interval, even if actual handover is late.
		location, err := time.LoadLocation(s.timezone)
		if err != nil {
			return err
		}
		nextDate := shift.PlannedEnd.In(location).Format("2006-01-02")
		kind := "day"
		if shift.Kind == "day" {
			kind = "night"
		}
		start, end, err := PlannedBounds(nextDate, kind, s.timezone)
		if err != nil {
			return err
		}
		next := Shift{OwnerID: receiver.ID, ParentID: shift.ID, BusinessDate: nextDate, Kind: kind, PlannedStart: start, PlannedEnd: end, Status: "opening"}
		result = Handover{FromShiftID: shift.ID, FromUserID: id.User.ID, ToUserID: receiver.ID, DeviceID: id.Session.DeviceID, SourceVersion: shift.Version, Snapshot: shift, Expected: amount}
		if err := tx.CreateHandover(&result, next, id.User.ID); err != nil {
			return err
		}
		if err := event(tx, r, id, "handover_prepared", result.ID, true, "", map[string]any{"from_shift_id": shift.ID, "to_shift_id": result.ToShiftID, "revision": result.Revision, "expected_cash_kopecks": amount}); err != nil {
			return err
		}
		body, err = json.Marshal(result)
		if err != nil {
			return err
		}
		if err := tx.SaveOperationResult(id.User.ID, route, key, hash, 201, body); err != nil {
			return err
		}
		return tx.RevokeSession(id.Session.Hash)
	})
	return result, replay, err
}
func handoverReadable(h Handover, id auth.Identity) bool {
	return id.User.Has("manager") || id.User.Has("admin") && id.DeviceTrusted && id.PINSession && (id.User.ID == h.FromUserID || id.User.ID == h.ToUserID)
}
func (s *Service) Handover(ctx context.Context, r auth.Request, handoverID string) (Handover, error) {
	if !auth.ValidID(handoverID) {
		return Handover{}, auth.Fail("INVALID_INPUT", 400)
	}
	var result Handover
	err := s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		var err error
		result, err = tx.LoadHandover(handoverID)
		if err != nil {
			return err
		}
		if !handoverReadable(result, id) {
			return auth.ErrMissing
		}
		return nil
	})
	return result, err
}
func receiver(h Handover, id auth.Identity) error {
	if !id.User.Has("admin") || id.User.ID != h.ToUserID {
		return auth.Fail("FORBIDDEN", 403)
	}
	if !id.PINSession || !id.DeviceTrusted || id.Session.DeviceID != h.DeviceID {
		return auth.Fail("DEVICE_REQUIRED", 403)
	}
	return nil
}

// handoverMutation persists the operation result with state/audit. Replay does
// not consume another proof; authorization still runs for the current session.
func (s *Service) handoverMutation(ctx context.Context, r auth.Request, handoverID, key, action string, payload any, fn func(Tx, auth.Identity, Handover) error) (Handover, bool, error) {
	if !auth.ValidID(handoverID) || !validKey(key) {
		return Handover{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	encoded, _ := json.Marshal(payload)
	hash := auth.Digest(string(encoded))
	parts := strings.SplitN(action, " ", 2)
	route := parts[0] + " /api/v1/handovers/" + handoverID + "/" + parts[1]
	var result Handover
	replay := false
	err := s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
		h, err := tx.LoadHandover(handoverID)
		if err != nil {
			return err
		}
		if !handoverReadable(h, id) {
			return auth.ErrMissing
		}
		if action == "POST cancel" {
			if id.User.ID != h.FromUserID || !id.PINSession || !id.DeviceTrusted {
				return auth.Fail("FORBIDDEN", 403)
			}
		} else if err := receiver(h, id); err != nil {
			return err
		}
		previous, body, found, err := tx.OperationResult(id.User.ID, route, key)
		if err != nil {
			return err
		}
		if found {
			if previous != hash {
				return auth.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			replay = true
			return json.Unmarshal(body, &result)
		}
		if h.Status != "pending" {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		if err := fn(tx, id, h); err != nil {
			return err
		}
		result, err = tx.LoadHandover(h.ID)
		if err != nil {
			return err
		}
		if err := event(tx, r, id, map[string]string{"POST cancel": "handover_cancelled", "POST accept": "handover_accepted", "PATCH receipt": "handover_receipt_saved", "POST acknowledgements": "handover_notes_acknowledged"}[action], h.ID, true, "", map[string]any{"revision": h.Revision, "version": result.Version, "from_shift_id": h.FromShiftID, "to_shift_id": h.ToShiftID, "before_counted_cash_kopecks": h.Counted, "counted_cash_kopecks": result.Counted, "acknowledged_note_ids": result.Acknowledged, "status": result.Status, "cancellation_reason": result.Reason, "actual_boundary_at": result.Boundary}); err != nil {
			return err
		}
		body, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.SaveOperationResult(id.User.ID, route, key, hash, 200, body)
	})
	return result, replay, err
}
func (s *Service) Cancel(ctx context.Context, r auth.Request, hID, key string, in CancelInput) (Handover, bool, error) {
	if strings.TrimSpace(in.Reason) == "" || utf8.RuneCountInString(in.Reason) > 4000 {
		return Handover{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	return s.handoverMutation(ctx, r, hID, key, "POST cancel", in, func(tx Tx, _ auth.Identity, h Handover) error {
		if h.Version != in.Version {
			return auth.Fail("VERSION_CONFLICT", 409)
		}
		return tx.CancelHandover(h, strings.TrimSpace(in.Reason))
	})
}
func (s *Service) Receipt(ctx context.Context, r auth.Request, hID, key string, in ReceiptInput) (Handover, bool, error) {
	if in.Counted == nil || *in.Counted < 0 {
		return Handover{}, false, auth.Fail("CASH_INVALID_AMOUNT", 400)
	}
	return s.handoverMutation(ctx, r, hID, key, "PATCH receipt", in, func(tx Tx, _ auth.Identity, h Handover) error {
		if h.Revision != in.Revision || h.Version != in.Version {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		return tx.SaveReceipt(h, in.Counted)
	})
}
func (s *Service) Acknowledge(ctx context.Context, r auth.Request, hID, key string, in AcknowledgeInput) (Handover, bool, error) {
	if len(in.NoteIDs) > 1000 {
		return Handover{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	seen := map[string]bool{}
	for _, noteID := range in.NoteIDs {
		if !auth.ValidID(noteID) || seen[noteID] {
			return Handover{}, false, auth.Fail("INVALID_INPUT", 400)
		}
		seen[noteID] = true
	}
	return s.handoverMutation(ctx, r, hID, key, "POST acknowledgements", in, func(tx Tx, id auth.Identity, h Handover) error {
		if h.Revision != in.Revision || h.Version != in.Version {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		for noteID := range seen {
			found := false
			for _, n := range h.Snapshot.Notes {
				if n.ID == noteID {
					found = true
				}
			}
			if !found {
				return auth.Fail("INVALID_INPUT", 400)
			}
		}
		return tx.Acknowledge(h, in.NoteIDs, id.User.ID)
	})
}
func (s *Service) Accept(ctx context.Context, r auth.Request, hID, key string, in AcceptInput) (Handover, bool, error) {
	return s.handoverMutation(ctx, r, hID, key, "POST accept", in, func(tx Tx, id auth.Identity, h Handover) error {
		if h.Revision != in.Revision || h.Version != in.Version {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		from, err := tx.LoadShift(h.FromShiftID)
		if err != nil {
			return err
		}
		next, err := tx.LoadShift(h.ToShiftID)
		if err != nil {
			return err
		}
		if from.Status != "handover_pending" || from.Version != h.SourceVersion+1 || next.Status != "opening" || next.ParentID != from.ID {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		if next.Version != in.ShiftVersion {
			return auth.Fail("VERSION_CONFLICT", 409)
		}
		if err := completion(from, "acceptance"); err != nil {
			return err
		}
		if err := completion(from, "closing"); err != nil {
			return err
		}
		if err := completion(next, "acceptance"); err != nil {
			return err
		}
		amount, err := closingCash(from)
		if err != nil {
			return err
		}
		if amount != h.Expected {
			return auth.Fail("HANDOVER_CHANGED", 409)
		}
		if h.Counted == nil {
			return auth.Fail("CASH_INPUT_MISSING", 422)
		}
		if *h.Counted != amount {
			return &auth.Failure{Code: "RECEIPT_MISMATCH", Status: 422, Details: map[string]any{"expected_kopecks": amount, "counted_kopecks": *h.Counted, "difference_kopecks": *h.Counted - amount}}
		}
		for _, note := range h.Snapshot.Notes {
			found := false
			for _, ack := range h.Acknowledged {
				if ack == note.ID {
					found = true
				}
			}
			if !found {
				return auth.Fail("NOTE_ACK_REQUIRED", 422)
			}
		}
		if err := consume(tx, id, in.Proof, "accept_handover", h.ID, next.Version); err != nil {
			return err
		}
		return tx.AcceptHandover(h, id.User.ID)
	})
}
