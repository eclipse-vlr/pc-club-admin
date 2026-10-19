ALTER TABLE shifts ADD COLUMN handover_parent_id uuid REFERENCES shifts(id);
ALTER TABLE shifts ADD CONSTRAINT shifts_owner_pair UNIQUE(id,owner_id);
DROP INDEX one_initial_opening;
CREATE UNIQUE INDEX one_initial_opening ON shifts(club_id) WHERE status='opening' AND handover_parent_id IS NULL;
CREATE TABLE handovers (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 from_shift_id uuid NOT NULL, to_shift_id uuid NOT NULL UNIQUE,
 from_user_id uuid NOT NULL REFERENCES users(id), to_user_id uuid NOT NULL REFERENCES users(id),
 device_id uuid NOT NULL REFERENCES trusted_devices(id),
 revision bigint NOT NULL CHECK(revision>0), version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 status text NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','accepted','cancelled')),
 source_version bigint NOT NULL CHECK(source_version>0), snapshot jsonb NOT NULL,
 expected_cash_kopecks bigint NOT NULL CHECK(expected_cash_kopecks>=0),
 prepared_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 accepted_at timestamptz, cancelled_at timestamptz, actual_boundary_at timestamptz,
 cancellation_reason text NOT NULL DEFAULT '' CHECK(length(cancellation_reason)<=4000),
 UNIQUE(from_shift_id,revision), UNIQUE(id,to_user_id),
 FOREIGN KEY(from_shift_id,from_user_id) REFERENCES shifts(id,owner_id),
 FOREIGN KEY(to_shift_id,to_user_id) REFERENCES shifts(id,owner_id),
 CHECK(from_user_id<>to_user_id AND from_shift_id<>to_shift_id),
 CHECK((status='pending' AND accepted_at IS NULL AND cancelled_at IS NULL AND actual_boundary_at IS NULL) OR
       (status='accepted' AND accepted_at IS NOT NULL AND actual_boundary_at=accepted_at AND cancelled_at IS NULL) OR
       (status='cancelled' AND cancelled_at IS NOT NULL AND accepted_at IS NULL AND actual_boundary_at IS NULL AND length(trim(cancellation_reason))>0))
);
CREATE UNIQUE INDEX one_pending_handover ON handovers(from_shift_id) WHERE status='pending';
CREATE UNIQUE INDEX one_accepted_handover ON handovers(from_shift_id) WHERE status='accepted';
CREATE TABLE handover_receipts (
 handover_id uuid PRIMARY KEY REFERENCES handovers(id), receiver_id uuid NOT NULL REFERENCES users(id),
 counted_cash_kopecks bigint CHECK(counted_cash_kopecks>=0),
 updated_at timestamptz, accepted_at timestamptz,
 FOREIGN KEY(handover_id,receiver_id) REFERENCES handovers(id,to_user_id)
);
CREATE TABLE handover_note_acknowledgements (
 handover_id uuid NOT NULL REFERENCES handovers(id), note_id uuid NOT NULL REFERENCES shift_notes(id),
 actor_id uuid NOT NULL REFERENCES users(id), acknowledged_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(handover_id,note_id),
 FOREIGN KEY(handover_id,actor_id) REFERENCES handovers(id,to_user_id)
);
CREATE FUNCTION protect_handover_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'handover history cannot be deleted'; END IF;
 IF OLD.status<>'pending' OR
 ROW(NEW.from_shift_id,NEW.to_shift_id,NEW.from_user_id,NEW.to_user_id,NEW.device_id,NEW.revision,NEW.source_version,NEW.snapshot,NEW.expected_cash_kopecks,NEW.prepared_at)
 IS DISTINCT FROM
 ROW(OLD.from_shift_id,OLD.to_shift_id,OLD.from_user_id,OLD.to_user_id,OLD.device_id,OLD.revision,OLD.source_version,OLD.snapshot,OLD.expected_cash_kopecks,OLD.prepared_at)
 THEN RAISE EXCEPTION 'handover snapshot is immutable'; END IF;
 IF NEW.status='accepted' AND NOT EXISTS(
  SELECT 1 FROM shifts a JOIN shifts b ON b.id=NEW.to_shift_id
  JOIN cash_sessions c ON c.shift_id=b.id JOIN handover_receipts r ON r.handover_id=NEW.id
  WHERE a.id=NEW.from_shift_id AND a.status='closed' AND b.status='active'
   AND a.actual_end_at=NEW.actual_boundary_at AND b.actual_start_at=NEW.actual_boundary_at
   AND NEW.actual_boundary_at>=a.actual_start_at AND b.previous_shift_id=a.id AND b.handover_parent_id=a.id
   AND r.counted_cash_kopecks=NEW.expected_cash_kopecks AND r.accepted_at=NEW.actual_boundary_at
   AND c.opening_cash_kopecks=NEW.expected_cash_kopecks AND c.counted_opening_kopecks=NEW.expected_cash_kopecks
   AND c.opening_confirmed_by=NEW.to_user_id AND c.opening_confirmed_at=NEW.actual_boundary_at
 ) THEN RAISE EXCEPTION 'handover boundary is inconsistent'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER immutable_handover_snapshot BEFORE UPDATE OR DELETE ON handovers FOR EACH ROW EXECUTE FUNCTION protect_handover_snapshot();
