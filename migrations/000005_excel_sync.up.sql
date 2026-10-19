CREATE TABLE excel_sync_queue (
 shift_id uuid PRIMARY KEY REFERENCES shifts(id),
 requested_version bigint NOT NULL CHECK(requested_version>0),
 synced_version bigint NOT NULL DEFAULT 0 CHECK(synced_version>=0),
 synced_target_hash text NOT NULL DEFAULT '',
 requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_attempt_at timestamptz, last_success_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 1000),
 last_error_code text NOT NULL DEFAULT '',
 CHECK(synced_version<=requested_version)
);
CREATE FUNCTION enqueue_excel_sync() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO excel_sync_queue(shift_id,requested_version) VALUES(NEW.id,NEW.version)
 ON CONFLICT(shift_id) DO UPDATE SET requested_version=EXCLUDED.requested_version,
 requested_at=clock_timestamp(),next_attempt_at=clock_timestamp();
 RETURN NEW;
END $$;
CREATE TRIGGER enqueue_excel_shift AFTER INSERT OR UPDATE OF version ON shifts
 FOR EACH ROW EXECUTE FUNCTION enqueue_excel_sync();
INSERT INTO excel_sync_queue(shift_id,requested_version) SELECT id,version FROM shifts;
