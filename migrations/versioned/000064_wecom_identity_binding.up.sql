-- Migration: 000064_wecom_identity_binding
-- Description: Store Enterprise WeChat identities, departments, memberships, and WeKnora user bindings.
DO $$ BEGIN RAISE NOTICE '[Migration 000064] Creating WeCom identity binding tables...'; END $$;

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

DO $$ BEGIN RAISE NOTICE '[Migration 000064] WeCom identity binding tables ready'; END $$;
