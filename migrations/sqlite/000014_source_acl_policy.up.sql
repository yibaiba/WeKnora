CREATE TABLE IF NOT EXISTS source_acl_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    provider VARCHAR(64) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    source_item_id VARCHAR(255) NOT NULL,
    source_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    visibility VARCHAR(32) NOT NULL DEFAULT 'restricted',
    status VARCHAR(32) NOT NULL DEFAULT 'ready',
    synced_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stale_after DATETIME NULL,
    provenance VARCHAR(32) NOT NULL DEFAULT 'direct',
    inherited_from_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    source_revision VARCHAR(255) NOT NULL DEFAULT '',
    source_hash VARCHAR(255) NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_source_acl_snapshot_knowledge
    ON source_acl_snapshots(tenant_id, knowledge_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_source_acl_snapshot_source
    ON source_acl_snapshots(tenant_id, provider, source_item_id)
    WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_source_acl_snapshots_kb ON source_acl_snapshots(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_source_acl_snapshots_visibility ON source_acl_snapshots(visibility);
CREATE INDEX IF NOT EXISTS idx_source_acl_snapshots_status ON source_acl_snapshots(status);
CREATE INDEX IF NOT EXISTS idx_source_acl_snapshots_deleted_at ON source_acl_snapshots(deleted_at);

CREATE TABLE IF NOT EXISTS source_acl_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    snapshot_id INTEGER NOT NULL,
    provider VARCHAR(64) NOT NULL,
    subject_type VARCHAR(64) NOT NULL,
    subject_id VARCHAR(255) NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'read',
    provenance VARCHAR(32) NOT NULL DEFAULT 'direct',
    inherited_from_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_source_acl_entries_snapshot ON source_acl_entries(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_source_acl_entries_subject
    ON source_acl_entries(tenant_id, subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_source_acl_entries_provider ON source_acl_entries(provider);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_acl_entries_unique_subject
    ON source_acl_entries(snapshot_id, subject_type, subject_id, permission);
