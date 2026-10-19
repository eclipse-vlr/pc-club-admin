-- Destructive delivery metadata rollback; does not delete the remote workbook.
DROP TRIGGER enqueue_excel_shift ON shifts;
DROP FUNCTION enqueue_excel_sync();
DROP TABLE excel_sync_queue;
