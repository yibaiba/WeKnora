ALTER TABLE tenant_api_keys
    ADD COLUMN IF NOT EXISTS owner_user_id VARCHAR(36)
        REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_tenant_owner
    ON tenant_api_keys(tenant_id, owner_user_id);
