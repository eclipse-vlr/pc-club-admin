CREATE TABLE club_settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    timezone text NOT NULL CHECK (length(timezone) BETWEEN 1 AND 100),
    cash_policy_status text NOT NULL DEFAULT 'unverified' CHECK (cash_policy_status = 'unverified'),
    cash_policy_version text NOT NULL DEFAULT 'draft-v1' CHECK (cash_policy_version = 'draft-v1'),
    created_at timestamptz NOT NULL DEFAULT now()
);
-- The initial cash balance is deliberately absent: it must be explicitly confirmed
-- by manager and first administrator in the later bootstrap scenario.
