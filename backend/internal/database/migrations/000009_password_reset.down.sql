DROP TABLE password_reset_tokens;
DROP TABLE password_reset_states;
ALTER TABLE users DROP COLUMN session_version;
