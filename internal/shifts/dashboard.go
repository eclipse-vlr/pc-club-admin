package shifts

import (
	"context"
	"time"

	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/cash"
	"pc-club-admin/internal/checklists"
)

// Dashboard is one authorized transaction's read model, never an approval to
// prepare/accept a handover. Lack of evidence is represented explicitly.
type Dashboard struct {
	ObservedAt        time.Time          `json:"observed_at"`
	Timezone          string             `json:"timezone"`
	State             string             `json:"state"`
	CashPolicy        string             `json:"cash_policy_status"`
	ProductionBlocked bool               `json:"production_blocked"`
	Responsible       *DashboardShift    `json:"responsible"`
	InitialOpening    *DashboardShift    `json:"initial_opening"`
	Pending           *DashboardTransfer `json:"pending_handover"`
}
type DashboardShift struct {
	ShiftSummary
	Overdue      bool                `json:"overdue"`
	DelaySeconds int64               `json:"delay_seconds"`
	Acceptance   checklists.Progress `json:"acceptance"`
	Closing      checklists.Progress `json:"closing"`
	Cash         *DashboardCash      `json:"cash"`
}
type DashboardCash struct {
	Opening            int64        `json:"opening_cash_kopecks"`
	CountedOpening     *int64       `json:"counted_opening_kopecks"`
	OpeningConfirmedAt *time.Time   `json:"opening_confirmed_at"`
	CountedClosing     *int64       `json:"counted_closing_kopecks"`
	Summary            cash.Summary `json:"summary"`
	State              string       `json:"state"`
	LastSavedAt        *time.Time   `json:"last_saved_at"`
	ChangedAfterSave   bool         `json:"changed_after_save"`
}
type DashboardTransfer struct {
	ReceiptState string              `json:"receipt_state"`
	ID           string              `json:"id"`
	Revision     int64               `json:"revision"`
	ReceiverID   string              `json:"receiver_id"`
	ReceiverName string              `json:"receiver_name"`
	PreparedAt   time.Time           `json:"prepared_at"`
	Expected     int64               `json:"expected_cash_kopecks"`
	Counted      *int64              `json:"counted_cash_kopecks"`
	Acknowledged int                 `json:"acknowledged_notes"`
	Notes        int                 `json:"total_notes"`
	Acceptance   checklists.Progress `json:"acceptance"`
}

func (s *Service) Dashboard(ctx context.Context, r auth.Request) (Dashboard, error) {
	result := Dashboard{Timezone: s.timezone, CashPolicy: "unverified", ProductionBlocked: s.production}
	err := s.within(ctx, r, false, func(tx Tx, id auth.Identity) error {
		if !id.User.Has("manager") {
			return auth.Fail("FORBIDDEN", 403)
		}
		now, responsible, opening, hasHistory, err := tx.DashboardSource()
		if err != nil {
			return err
		}
		if responsible != "" && opening != "" {
			return auth.Fail("DASHBOARD_INCONSISTENT", 503)
		}
		result.ObservedAt = now
		result.State = "uninitialized"
		if hasHistory {
			result.State = "no_responsible"
		}
		if opening != "" {
			shift, err := tx.LoadShift(opening)
			if err != nil {
				return err
			}
			card, err := dashboardShift(tx, shift, now)
			if err != nil {
				return err
			}
			result.InitialOpening = &card
			result.State = "opening"
		}
		if responsible == "" {
			return nil
		}
		shift, err := tx.LoadShift(responsible)
		if err != nil {
			return err
		}
		card, err := dashboardShift(tx, shift, now)
		if err != nil {
			return err
		}
		result.Responsible = &card
		result.State = shift.Status
		if shift.Status != "handover_pending" {
			return nil
		}
		if shift.HandoverID == "" {
			return auth.Fail("DASHBOARD_INCONSISTENT", 503)
		}
		handover, err := tx.LoadHandover(shift.HandoverID)
		if err != nil {
			return err
		}
		next, err := tx.LoadShift(handover.ToShiftID)
		if err != nil {
			return err
		}
		if handover.Status != "pending" || handover.FromShiftID != shift.ID || handover.SourceVersion+1 != shift.Version || next.Status != "opening" || next.ParentID != shift.ID || next.OwnerID != handover.ToUserID {
			return auth.Fail("DASHBOARD_INCONSISTENT", 503)
		}
		receiver, err := tx.User(handover.ToUserID)
		if err != nil {
			return err
		}
		receiptState := "missing"
		if handover.Counted != nil {
			receiptState = "mismatch"
			if *handover.Counted == handover.Expected {
				receiptState = "matched"
			}
		}
		result.Pending = &DashboardTransfer{ReceiptState: receiptState, ID: handover.ID, Revision: handover.Revision, ReceiverID: receiver.ID, ReceiverName: receiver.Name, PreparedAt: handover.PreparedAt, Expected: handover.Expected, Counted: handover.Counted, Acknowledged: len(handover.Acknowledged), Notes: len(handover.Snapshot.Notes), Acceptance: checklists.Evaluate(next.Entries, next.Notes, "acceptance")}
		return nil
	})
	return result, err
}
func dashboardShift(tx Tx, shift Shift, now time.Time) (DashboardShift, error) {
	result := DashboardShift{ShiftSummary: ShiftSummary{ID: shift.ID, OwnerID: shift.OwnerID, OwnerName: shift.OwnerName, BusinessDate: shift.BusinessDate, Kind: shift.Kind, Status: shift.Status, PlannedStart: shift.PlannedStart, PlannedEnd: shift.PlannedEnd, ActualStart: shift.ActualStart, ActualEnd: shift.ActualEnd}, Acceptance: checklists.Evaluate(shift.Entries, shift.Notes, "acceptance"), Closing: checklists.Evaluate(shift.Entries, shift.Notes, "closing")}
	if (shift.Status == "active" || shift.Status == "handover_pending") && now.After(shift.PlannedEnd) {
		result.Overdue = true
		result.DelaySeconds = now.Unix() - shift.PlannedEnd.Unix()
	}
	if shift.Cash == nil {
		return result, nil
	}
	session := shift.Cash.Session
	saved, savedVersion, err := tx.CashSavedReport(shift.ID)
	if err != nil {
		return result, err
	}
	view := &DashboardCash{Opening: session.Opening, CountedOpening: session.CountedOpening, OpeningConfirmedAt: session.OpeningConfirmedAt, CountedClosing: session.Counted, Summary: cash.Evaluate(session), LastSavedAt: saved}
	view.State = "matched"
	if session.OpeningConfirmedAt == nil {
		view.State = "opening_unconfirmed"
	} else if len(view.Summary.Blockers) > 0 {
		view.State = "invalid"
		for _, blocker := range view.Summary.Blockers {
			if blocker.Code == "CASH_INPUT_MISSING" {
				view.State = "incomplete"
				break
			}
			if blocker.Code == "CASH_MISMATCH" {
				view.State = "mismatch"
			}
		}
	}
	if saved != nil && savedVersion > session.Version {
		return result, auth.Fail("DASHBOARD_INCONSISTENT", 503)
	}
	view.ChangedAfterSave = saved != nil && session.Version > savedVersion || saved == nil && len(session.Movements) > 0
	result.Cash = view
	return result, nil
}
