CREATE TABLE IF NOT EXISTS wecom_identities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    userid VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    email VARCHAR(255) NOT NULL DEFAULT '',
    mobile VARCHAR(64) NOT NULL DEFAULT '',
    avatar VARCHAR(1024) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_identities_tenant_userid
    ON wecom_identities(tenant_id, userid)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wecom_identities_tenant_id ON wecom_identities(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wecom_identities_status ON wecom_identities(status);
CREATE INDEX IF NOT EXISTS idx_wecom_identities_deleted_at ON wecom_identities(deleted_at);

CREATE TABLE IF NOT EXISTS wecom_departments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    department_id VARCHAR(128) NOT NULL,
    parent_id VARCHAR(128) NOT NULL DEFAULT '',
    name VARCHAR(255) NOT NULL DEFAULT '',
    dept_order INTEGER NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wecom_departments_tenant_dept
    ON wecom_departments(tenant_id, department_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_wecom_departments_tenant_id ON wecom_departments(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_parent ON wecom_departments(tenant_id, parent_id);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_status ON wecom_departments(status);
CREATE INDEX IF NOT EXISTS idx_wecom_departments_deleted_at ON wecom_departments(deleted_at);

CREATE TABLE IF NOT EXISTS wecom_identity_departments (
    tenant_id INTEGER NOT NULL,
    userid VARCHAR(128) NOT NULL,
    department_id VARCHAR(128) NOT NULL,
    synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, userid, department_id)
);

CREATE INDEX IF NOT EXISTS idx_wecom_identity_departments_user
    ON wecom_identity_departments(tenant_id, userid);
CREATE INDEX IF NOT EXISTS idx_wecom_identity_departments_dept
    ON wecom_identity_departments(tenant_id, department_id);

CREATE TABLE IF NOT EXISTS wecom_user_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    weknora_user_id VARCHAR(36) NOT NULL,
    wecom_userid VARCHAR(128) NOT NULL,
    provider VARCHAR(32) NOT NULL DEFAULT 'wecom',
    source VARCHAR(32) NOT NULL DEFAULT 'admin',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    last_verified_at DATETIME NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
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
