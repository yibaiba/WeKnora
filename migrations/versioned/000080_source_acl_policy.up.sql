-- Migration: 000080_source_acl_policy
-- Description: Store normalized source ACL snapshots and entries for source-backed knowledge.
DO $$ BEGIN RAISE NOTICE '[Migration 000080] Creating source ACL policy tables...'; END $$;

CREATE TABLE IF NOT EXISTS source_acl_snapshots (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    provider VARCHAR(64) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    source_item_id VARCHAR(255) NOT NULL,
    source_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    visibility VARCHAR(32) NOT NULL DEFAULT 'restricted',
    status VARCHAR(32) NOT NULL DEFAULT 'ready',
    synced_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    stale_after TIMESTAMP WITH TIME ZONE,
    provenance VARCHAR(32) NOT NULL DEFAULT 'direct',
    inherited_from_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    source_revision VARCHAR(255) NOT NULL DEFAULT '',
    source_hash VARCHAR(255) NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMP WITH TIME ZONE
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
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    snapshot_id BIGINT NOT NULL,
    provider VARCHAR(64) NOT NULL,
    subject_type VARCHAR(64) NOT NULL,
    subject_id VARCHAR(255) NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'read',
    provenance VARCHAR(32) NOT NULL DEFAULT 'direct',
    inherited_from_resource_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_source_acl_entries_snapshot ON source_acl_entries(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_source_acl_entries_subject
    ON source_acl_entries(tenant_id, subject_type, subject_id);
CREATE INDEX IF NOT EXISTS idx_source_acl_entries_provider ON source_acl_entries(provider);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_acl_entries_unique_subject
    ON source_acl_entries(snapshot_id, subject_type, subject_id, permission);

DO $$ BEGIN RAISE NOTICE '[Migration 000080] Source ACL policy tables ready'; END $$;
