-- Destructive; review data and compatibility before use.
DROP TABLE cash_movements,cash_sessions;
DROP FUNCTION immutable_cash_movement();
ALTER TABLE reauth_proofs DROP CONSTRAINT reauth_proofs_action_check;
DELETE FROM reauth_proofs WHERE action='activate_first_shift';
ALTER TABLE reauth_proofs ADD CONSTRAINT reauth_proofs_action_check CHECK(action IN ('prepare_handover','accept_handover','collection','reverse_collection'));
