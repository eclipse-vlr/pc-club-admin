CREATE TABLE users (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 display_name text NOT NULL CHECK(length(display_name) BETWEEN 1 AND 100),
 manager_login text UNIQUE CHECK(manager_login IS NULL OR (length(manager_login) BETWEEN 3 AND 100 AND manager_login = lower(manager_login))),
 active boolean NOT NULL DEFAULT true,
 pin_hash text NOT NULL DEFAULT '',
 password_hash text NOT NULL DEFAULT '',
 auth_version bigint NOT NULL DEFAULT 1 CHECK(auth_version > 0),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0)
);
CREATE TABLE user_roles (
 user_id uuid NOT NULL REFERENCES users(id),
 role text NOT NULL CHECK(role IN ('admin','manager')),
 PRIMARY KEY(user_id,role)
);
CREATE TABLE trusted_devices (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 token_hash text NOT NULL UNIQUE,
 label text NOT NULL CHECK(length(label) BETWEEN 1 AND 100),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 created_by uuid NOT NULL REFERENCES users(id)
);
CREATE TABLE device_enrollments (
 token_hash text PRIMARY KEY,
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 created_by uuid NOT NULL REFERENCES users(id)
);
CREATE TABLE sessions (
 token_hash text PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES users(id),
 device_id uuid REFERENCES trusted_devices(id),
 auth_version bigint NOT NULL,
 csrf_hash text NOT NULL,
 idle_expires_at timestamptz NOT NULL,
 absolute_expires_at timestamptz NOT NULL,
 revoked_at timestamptz
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE auth_attempts (
 key text PRIMARY KEY,
 attempts integer NOT NULL CHECK(attempts >= 0),
 window_start timestamptz NOT NULL,
 blocked_until timestamptz NOT NULL
);
CREATE TABLE reauth_proofs (
 token_hash text PRIMARY KEY,
 actor_id uuid NOT NULL REFERENCES users(id),
 action text NOT NULL CHECK(action IN ('prepare_handover','accept_handover','collection','reverse_collection')),
 target_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision > 0),
 expires_at timestamptz NOT NULL,
 used_at timestamptz,
 session_hash text NOT NULL REFERENCES sessions(token_hash)
);
CREATE TABLE audit_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id uuid REFERENCES users(id),
 device_id uuid REFERENCES trusted_devices(id),
 action text NOT NULL,
 entity_id text NOT NULL DEFAULT '',
 request_id text NOT NULL,
 success boolean NOT NULL,
 reason text NOT NULL DEFAULT '',
 details jsonb NOT NULL DEFAULT '{}'::jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
-- Application API has no UPDATE/DELETE endpoint for audit or identities' history.
