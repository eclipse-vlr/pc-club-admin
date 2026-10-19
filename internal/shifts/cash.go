package shifts

import (
	"context"
	"encoding/json"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/cash"
	"pc-club-admin/internal/checklists"
	"strings"
	"time"
	"unicode/utf8"
)

type CashInput struct {
	Langame  *int64 `json:"langame_cash_amount_kopecks"`
	Refunds  *int64 `json:"extra_cash_refunds_kopecks"`
	Expenses *int64 `json:"extra_cash_expenses_kopecks"`
	Counted  *int64 `json:"counted_closing_kopecks"`
	Version  int64  `json:"version"`
}
type CollectionInput struct {
	Amount     *int64    `json:"amount_kopecks"`
	Reason     string    `json:"reason"`
	Recipient  string    `json:"recipient"`
	OccurredAt time.Time `json:"occurred_at"`
	Proof      string    `json:"reauth_proof"`
	Version    int64     `json:"version"`
}
type ReverseInput struct {
	Reason  string `json:"reason"`
	Proof   string `json:"reauth_proof"`
	Version int64  `json:"version"`
}
type ActivateInput struct {
	Counted *int64 `json:"counted_opening_kopecks"`
	Proof   string `json:"reauth_proof"`
	Version int64  `json:"version"`
}

func cashView(session cash.Session) cash.View {
	return cash.View{Session: session, Summary: cash.Evaluate(session)}
}
func cashOwner(shift Shift, id auth.Identity) error {
	if !id.User.Has("admin") || shift.OwnerID != id.User.ID {
		return auth.Fail("FORBIDDEN", 403)
	}
	if !id.PINSession || !id.DeviceTrusted {
		return auth.Fail("DEVICE_REQUIRED", 403)
	}
	return nil
}
func checkedDraft(session cash.Session) error {
	for _, b := range cash.Evaluate(session).Blockers {
		if b.Code == "CASH_OVERFLOW" || b.Code == "CASH_INVALID_AMOUNT" {
			return auth.Fail(b.Code, 400)
		}
	}
	return nil
}
func consume(tx Tx, id auth.Identity, proof, action, target string, revision int64) error {
	if len(proof) != 64 {
		return auth.Fail("REAUTH_REQUIRED", 403)
	}
	ok, err := tx.ConsumeProof(auth.Proof{Hash: auth.Digest(proof), ActorID: id.User.ID, Action: action, TargetID: target, Revision: revision, SessionHash: id.Session.Hash})
	if err != nil {
		return err
	}
	if !ok {
		return auth.Fail("REAUTH_REQUIRED", 403)
	}
	return nil
}

// moneyMutation owns the transaction: replay, version check, proof, money and audit
// commit together. A rejected mutation does not spend its idempotency key/proof.
func (s *Service) moneyMutation(ctx context.Context, r auth.Request, shiftID, route, key string, payload any, fn func(Tx, auth.Identity, Shift, cash.Session) (cash.View, error)) (cash.View, bool, error) {
	if !auth.ValidID(shiftID) || !validKey(key) {
		return cash.View{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return cash.View{}, false, err
	}
	hash := auth.Digest(string(encoded))
	var result cash.View
	replay := false
	err = s.within(ctx, r, true, func(tx Tx, id auth.Identity) error {
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
		session, err := tx.LoadCash(shiftID)
		if err != nil {
			return err
		}
		if session.OpeningConfirmedAt == nil {
			return auth.Fail("OPENING_COUNT_REQUIRED", 422)
		}
		result, err = fn(tx, id, shift, session)
		if err != nil {
			return err
		}
		body, err = json.Marshal(result)
		if err != nil {
			return err
		}
		code := 200
		if strings.HasPrefix(route, "POST ") {
			code = 201
		}
		return tx.SaveOperationResult(id.User.ID, route, key, hash, code, body)
	})
	return result, replay, err
}
func (s *Service) PatchCash(ctx context.Context, r auth.Request, shiftID, key string, in CashInput) (cash.View, bool, error) {
	if in.Refunds != nil && *in.Refunds < 0 || in.Expenses != nil && *in.Expenses < 0 || in.Counted != nil && *in.Counted < 0 {
		return cash.View{}, false, auth.Fail("CASH_INVALID_AMOUNT", 400)
	}
	return s.moneyMutation(ctx, r, shiftID, "PATCH /api/v1/shifts/"+shiftID+"/cash", key, in, func(tx Tx, id auth.Identity, shift Shift, session cash.Session) (cash.View, error) {
		if session.Version != in.Version {
			return cash.View{}, auth.Fail("VERSION_CONFLICT", 409)
		}
		before := session
		session.Langame = in.Langame
		session.Refunds = in.Refunds
		session.Expenses = in.Expenses
		session.Counted = in.Counted
		session.Version++
		if err := checkedDraft(session); err != nil {
			return cash.View{}, err
		}
		if err := tx.SaveCash(session); err != nil {
			return cash.View{}, err
		}
		if err := tx.BumpShift(shift.ID); err != nil {
			return cash.View{}, err
		}
		if err := event(tx, r, id, "cash_saved", shift.ID, true, "", map[string]any{"before": before, "after": session}); err != nil {
			return cash.View{}, err
		}
		return cashView(session), nil
	})
}
func (s *Service) Collect(ctx context.Context, r auth.Request, shiftID, key string, in CollectionInput) (cash.View, bool, error) {
	if in.Amount == nil || *in.Amount < 0 || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 1 || utf8.RuneCountInString(in.Reason) > 4000 || utf8.RuneCountInString(in.Recipient) > 200 || in.OccurredAt.IsZero() {
		return cash.View{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	return s.moneyMutation(ctx, r, shiftID, "POST /api/v1/shifts/"+shiftID+"/cash/collections", key, in, func(tx Tx, id auth.Identity, shift Shift, session cash.Session) (cash.View, error) {
		if session.Version != in.Version {
			return cash.View{}, auth.Fail("VERSION_CONFLICT", 409)
		}
		if err := consume(tx, id, in.Proof, "collection", shift.ID, session.Version); err != nil {
			return cash.View{}, err
		}
		movement := cash.Movement{Kind: "collection", Amount: *in.Amount, Reason: strings.TrimSpace(in.Reason), Recipient: strings.TrimSpace(in.Recipient), OccurredAt: in.OccurredAt, ActorID: id.User.ID}
		// Compute before inserting, preventing invalid aggregate totals from persisting.
		preview := session
		preview.Movements = append(append([]cash.Movement{}, session.Movements...), movement)
		if err := checkedDraft(preview); err != nil {
			return cash.View{}, err
		}
		if err := tx.AppendMovement(session.ID, &movement); err != nil {
			return cash.View{}, err
		}
		session.Movements = append(session.Movements, movement)
		session.Version++
		if err := tx.SaveCash(session); err != nil {
			return cash.View{}, err
		}
		if err := tx.BumpShift(shift.ID); err != nil {
			return cash.View{}, err
		}
		if err := event(tx, r, id, "cash_collected", shift.ID, true, "", map[string]any{"movement": movement, "policy_version": session.Policy}); err != nil {
			return cash.View{}, err
		}
		return cashView(session), nil
	})
}
func (s *Service) ReverseCollection(ctx context.Context, r auth.Request, shiftID, movementID, key string, in ReverseInput) (cash.View, bool, error) {
	if !auth.ValidID(movementID) || utf8.RuneCountInString(strings.TrimSpace(in.Reason)) < 1 || utf8.RuneCountInString(in.Reason) > 4000 {
		return cash.View{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	return s.moneyMutation(ctx, r, shiftID, "POST /api/v1/shifts/"+shiftID+"/cash/collections/"+movementID+"/reverse", key, in, func(tx Tx, id auth.Identity, shift Shift, session cash.Session) (cash.View, error) {
		if session.Version != in.Version {
			return cash.View{}, auth.Fail("VERSION_CONFLICT", 409)
		}
		var original *cash.Movement
		for _, m := range session.Movements {
			if m.Kind == "reversal" && m.ReversalOf != nil && *m.ReversalOf == movementID {
				return cash.View{}, auth.Fail("COLLECTION_ALREADY_REVERSED", 409)
			}
			if m.ID == movementID && m.Kind == "collection" {
				copy := m
				original = &copy
			}
		}
		if original == nil {
			return cash.View{}, auth.ErrMissing
		}
		if err := consume(tx, id, in.Proof, "reverse_collection", movementID, session.Version); err != nil {
			return cash.View{}, err
		}
		movement := cash.Movement{Kind: "reversal", Amount: original.Amount, ReversalOf: &movementID, Reason: strings.TrimSpace(in.Reason), OccurredAt: time.Now(), ActorID: id.User.ID}
		preview := session
		preview.Movements = append(append([]cash.Movement{}, session.Movements...), movement)
		if err := checkedDraft(preview); err != nil {
			return cash.View{}, err
		}
		if err := tx.AppendMovement(session.ID, &movement); err != nil {
			return cash.View{}, err
		}
		session.Movements = append(session.Movements, movement)
		session.Version++
		if err := tx.SaveCash(session); err != nil {
			return cash.View{}, err
		}
		if err := tx.BumpShift(shift.ID); err != nil {
			return cash.View{}, err
		}
		if err := event(tx, r, id, "cash_collection_reversed", shift.ID, true, "", map[string]any{"movement": movement}); err != nil {
			return cash.View{}, err
		}
		return cashView(session), nil
	})
}
func (s *Service) Activate(ctx context.Context, r auth.Request, shiftID, key string, in ActivateInput) (Shift, bool, error) {
	if !auth.ValidID(shiftID) || !validKey(key) {
		return Shift{}, false, auth.Fail("INVALID_INPUT", 400)
	}
	if in.Counted == nil {
		return Shift{}, false, auth.Fail("CASH_INPUT_MISSING", 422)
	}
	if *in.Counted < 0 {
		return Shift{}, false, auth.Fail("CASH_INVALID_AMOUNT", 400)
	}
	encoded, _ := json.Marshal(in)
	hash := auth.Digest(string(encoded))
	route := "POST /api/v1/shifts/" + shiftID + "/activate"
	var result Shift
	replay := false
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
		if shift.Status != "opening" || shift.ParentID != "" || shift.InitialCash == nil {
			return auth.Fail("INVALID_STATE", 409)
		}
		if shift.Version != in.Version {
			return auth.Fail("VERSION_CONFLICT", 409)
		}
		acceptance := checklists.Evaluate(shift.Entries, shift.Notes, "acceptance")
		if len(acceptance.Blockers) > 0 {
			return &auth.Failure{Code: acceptance.Blockers[0].Code, Status: 422, Details: map[string]any{"blockers": acceptance.Blockers}}
		}
		session, err := tx.LoadCash(shift.ID)
		if err != nil {
			return err
		}
		if session.Opening != *in.Counted {
			difference, _ := cash.Sub(*in.Counted, session.Opening)
			return &auth.Failure{Code: "CASH_MISMATCH", Status: 422, Details: map[string]any{"expected_kopecks": session.Opening, "counted_kopecks": *in.Counted, "difference_kopecks": difference}}
		}
		if err := consume(tx, id, in.Proof, "activate_first_shift", shift.ID, shift.Version); err != nil {
			return err
		}
		if err := tx.ActivateFirst(shift.ID, id.User.ID, *in.Counted); err != nil {
			return err
		}
		result, err = tx.LoadShift(shift.ID)
		if err != nil {
			return err
		}
		if err := event(tx, r, id, "first_shift_activated", shift.ID, true, "", map[string]any{"accepted_opening_kopecks": *in.Counted, "policy_version": session.Policy}); err != nil {
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
