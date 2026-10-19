-- Destructive. Restore only after compatibility review and backup.
DROP TABLE audit_events, reauth_proofs, auth_attempts, sessions, device_enrollments, trusted_devices, user_roles, users;
