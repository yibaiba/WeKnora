-- Bind local users to the enterprise SSO subject (emp_ + UUID).
-- Empty string is treated as unbound so password-only accounts stay unique-index-free.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS sso_subject VARCHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_sso_subject
    ON users (sso_subject)
    WHERE sso_subject IS NOT NULL AND sso_subject <> '';

COMMENT ON COLUMN users.sso_subject IS 'Enterprise SSO subject (emp_ UUID). Local user key for SSO logins; not a login name.';
