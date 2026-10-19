CREATE TABLE cash_sessions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),shift_id uuid NOT NULL UNIQUE REFERENCES shifts(id),
 opening_cash_kopecks bigint NOT NULL CHECK(opening_cash_kopecks>=0),
 counted_opening_kopecks bigint CHECK(counted_opening_kopecks>=0),
 opening_confirmed_by uuid REFERENCES users(id),opening_confirmed_at timestamptz,
 langame_cash_amount_kopecks bigint,
 extra_cash_refunds_kopecks bigint CHECK(extra_cash_refunds_kopecks>=0),
 extra_cash_expenses_kopecks bigint CHECK(extra_cash_expenses_kopecks>=0),
 counted_closing_kopecks bigint CHECK(counted_closing_kopecks>=0),
 policy_version text NOT NULL DEFAULT 'draft-v1' CHECK(policy_version='draft-v1'),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 CHECK((opening_confirmed_by IS NULL AND opening_confirmed_at IS NULL AND counted_opening_kopecks IS NULL) OR
       (opening_confirmed_by IS NOT NULL AND opening_confirmed_at IS NOT NULL AND counted_opening_kopecks IS NOT NULL AND counted_opening_kopecks=opening_cash_kopecks))
);
-- Initial manager amount is not automatically accepted by the administrator.
INSERT INTO cash_sessions(shift_id,opening_cash_kopecks) SELECT id,initial_cash_kopecks FROM shifts WHERE initial_cash_kopecks IS NOT NULL;
CREATE TABLE cash_movements (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),cash_session_id uuid NOT NULL REFERENCES cash_sessions(id),
 kind text NOT NULL CHECK(kind IN ('collection','reversal')),
 amount_kopecks bigint NOT NULL CHECK(amount_kopecks>=0),
 reversal_of uuid UNIQUE,
 target_kind text NOT NULL DEFAULT 'collection' CHECK(target_kind='collection'),
 reason text NOT NULL CHECK(length(trim(reason)) BETWEEN 1 AND 4000),
 recipient text NOT NULL DEFAULT '' CHECK(length(recipient)<=200),
 occurred_at timestamptz NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),actor_id uuid NOT NULL REFERENCES users(id),
 UNIQUE(id,cash_session_id,amount_kopecks,kind),
 FOREIGN KEY(reversal_of,cash_session_id,amount_kopecks,target_kind) REFERENCES cash_movements(id,cash_session_id,amount_kopecks,kind),
 CHECK((kind='collection' AND reversal_of IS NULL) OR (kind='reversal' AND reversal_of IS NOT NULL))
);
CREATE FUNCTION immutable_cash_movement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'cash movements are append-only'; END $$;
CREATE TRIGGER immutable_cash_movements BEFORE UPDATE OR DELETE ON cash_movements FOR EACH ROW EXECUTE FUNCTION immutable_cash_movement();
ALTER TABLE reauth_proofs DROP CONSTRAINT reauth_proofs_action_check;
ALTER TABLE reauth_proofs ADD CONSTRAINT reauth_proofs_action_check CHECK(action IN ('prepare_handover','accept_handover','collection','reverse_collection','activate_first_shift'));
