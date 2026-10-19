CREATE INDEX audit_cash_saved_lookup ON audit_events(entity_id,id DESC) WHERE success AND action='cash_saved';
