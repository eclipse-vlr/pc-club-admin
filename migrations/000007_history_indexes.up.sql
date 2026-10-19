CREATE INDEX shifts_history_order ON shifts(business_date DESC,created_at DESC,id DESC);
CREATE INDEX shifts_owner_history_order ON shifts(owner_id,business_date DESC,created_at DESC,id DESC);
