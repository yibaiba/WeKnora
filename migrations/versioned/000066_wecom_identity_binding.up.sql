-- Migration: 000066_wecom_identity_binding
-- Description: Store Enterprise WeChat identities, departments, memberships, and WeKnora user bindings.

-- Merge repair: local enterprise branches previously used versions 000064
-- and 000065 before upstream added principal_model and tenant_api_keys at
-- those numbers. A database that already recorded the old versions would skip
-- the upstream files, so keep the upstream schema changes idempotently here.
DO $$ BEGIN RAISE NOTICE '[Migration 000066] Ensuring upstream 000064 principal model schema...'; END $$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema()
          AND table_name = 'mcp_oauth_tokens'
    ) THEN
        ALTER TABLE mcp_oauth_tokens
            ADD COLUMN IF NOT EXISTS principal_type VARCHAR(32),
            ADD COLUMN IF NOT EXISTS principal_id VARCHAR(512);

        ALTER TABLE mcp_oauth_tokens
            ALTER COLUMN user_id TYPE VARCHAR(512);

        UPDATE mcp_oauth_tokens
        SET principal_type = 'web_user',
            principal_id = user_id
        WHERE (principal_type IS NULL OR principal_type = '')
          AND user_id IS NOT NULL
          AND user_id <> '';

        ALTER TABLE mcp_oauth_tokens
            ALTER COLUMN principal_type SET NOT NULL,
            ALTER COLUMN principal_id SET NOT NULL;

        DROP INDEX IF EXISTS idx_mcp_oauth_tokens_tenant_user_svc;

        CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_tenant_principal_svc
            ON mcp_oauth_tokens(tenant_id, principal_type, principal_id, service_id);

        CREATE INDEX IF NOT EXISTS idx_mcp_oauth_tokens_principal
            ON mcp_oauth_tokens(principal_type, principal_id);
    END IF;
END $$;

ALTER TABLE tenants
    ADD COLUMN IF NOT EXISTS api_principal_config JSONB;

ALTER TABLE sessions
    ALTER COLUMN user_id TYPE VARCHAR(512);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Ensuring upstream 000065 tenant_api_keys schema...'; END $$;

CREATE TABLE IF NOT EXISTS tenant_api_keys (
    id BIGSERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    api_key TEXT NOT NULL DEFAULT '',
    full_access BOOLEAN NOT NULL DEFAULT FALSE,
    knowledge_base_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    last_used_at TIMESTAMP,
    expires_at TIMESTAMP,
    revoked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_tenant
    ON tenant_api_keys(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_revoked_at
    ON tenant_api_keys(revoked_at);

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'tenants'
          AND column_name = 'api_key'
    ) THEN
        EXECUTE $migrate_api_keys$
            INSERT INTO tenant_api_keys (
                tenant_id,
                name,
                key_hash,
                api_key,
                full_access,
                knowledge_base_ids,
                created_at,
                updated_at
            )
            SELECT
                id,
                'Tenant API key',
                'migrated-tenant-' || id::text,
                api_key,
                TRUE,
                '[]'::jsonb,
                CURRENT_TIMESTAMP,
                CURRENT_TIMESTAMP
            FROM tenants
            WHERE COALESCE(api_key, '') <> ''
            ON CONFLICT (key_hash) DO NOTHING
        $migrate_api_keys$;

        DROP INDEX IF EXISTS idx_tenants_api_key;
        ALTER TABLE tenants DROP COLUMN IF EXISTS api_key;
    END IF;
END $$;

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating WeCom identity binding tables...'; END $$;

CREATE TABLE IF NOT EXISTS wecom_identities (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    userid VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    email VARCHAR(255) NOT NULL DEFAULT '',
    mobile VARCHAR(64) NOT NULL DEFAULT '',
    avatar VARCHAR(1024) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    synced_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_identities_tenant_userid
    ON wecom_identities(tenant_id, userid)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wecom_identities_tenant_id ON wecom_identities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wecom_identities_status ON wecom_identities(status);
CREATE INDEX IF NOT EXISTS idx_wecom_identities_deleted_at ON wecom_identities(deleted_at);

CREATE TABLE IF NOT EXISTS wecom_departments (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    department_id VARCHAR(128) NOT NULL,
    parent_id VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL DEFAULT '',
    dept_order BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    synced_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_departments_tenant_dept
    ON wecom_departments(tenant_id, department_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wecom_departments_tenant_id ON wecom_departments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_parent ON wecom_departments(tenant_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_status ON wecom_departments(status);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_deleted_at ON wecom_departments(deleted_at);

CREATE TABLE IF NOT EXISTS wecom_identity_departments (
    tenant_id BIGINT NOT NULL,
    userid VARCHAR(128) NOT NULL,
    department_id VARCHAR(128) NOT NULL,
    synced_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, userid, department_id)
);

CREATE INDEX IF NOT EXISTS idx_wecom_identity_departments_user
    ON wecom_identity_departments(tenant_id, userid);
CREATE INDEX IF NOT EXISTS idx_wecom_identity_departments_dept
    ON wecom_identity_departments(tenant_id, department_id);

CREATE TABLE IF NOT EXISTS wecom_user_bindings (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    weknora_user_id VARCHAR(36) NOT NULL,
    wecom_userid VARCHAR(128) NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    source VARCHAR(32) NOT NULL DEFAULT 'admin',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    last_verified_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_bindings_active_weknora_user
    ON wecom_user_bindings(tenant_id, weknora_user_id)
    WHERE deleted_at IS NULL AND status = 'active';
CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_bindings_active_wecom_user
    ON wecom_user_bindings(tenant_id, wecom_userid)
    WHERE deleted_at IS NULL AND status = 'active';
CREATE INDEX IF NOT EXISTS idx_wecom_user_bindings_tenant_id ON wecom_user_bindings(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wecom_user_bindings_wecom_userid ON wecom_user_bindings(wecom_userid);
CREATE INDEX IF NOT EXISTS idx_wecom_user_bindings_status ON wecom_user_bindings(status);
CREATE INDEX IF NOT EXISTS idx_wecom_user_bindings_deleted_at ON wecom_user_bindings(deleted_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] WeCom identity binding tables ready'; END $$;
