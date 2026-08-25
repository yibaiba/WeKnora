DROP INDEX IF EXISTS idx_tenant_api_keys_tenant_owner;

ALTER TABLE tenant_api_keys
    DROP COLUMN IF EXISTS owner_user_id;
