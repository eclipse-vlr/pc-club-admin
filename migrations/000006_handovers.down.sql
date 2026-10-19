DROP TABLE handover_note_acknowledgements;
DROP TABLE handover_receipts;
DROP TABLE handovers;
DROP FUNCTION protect_handover_snapshot();
DROP INDEX one_initial_opening;
CREATE UNIQUE INDEX one_initial_opening ON shifts(club_id) WHERE status='opening' AND previous_shift_id IS NULL;
ALTER TABLE shifts DROP CONSTRAINT shifts_owner_pair;
ALTER TABLE shifts DROP COLUMN handover_parent_id;
