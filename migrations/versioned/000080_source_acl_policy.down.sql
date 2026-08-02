-- Rollback: 000080_source_acl_policy
DO $$ BEGIN RAISE NOTICE '[Migration 000080 rollback] Dropping source ACL policy tables...'; END $$;

DROP TABLE IF EXISTS source_acl_entries;
DROP TABLE IF EXISTS source_acl_snapshots;

DO $$ BEGIN RAISE NOTICE '[Migration 000080 rollback] Source ACL policy tables dropped'; END $$;
