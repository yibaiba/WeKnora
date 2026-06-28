-- Rollback: 000065_source_acl_policy
DO $$ BEGIN RAISE NOTICE '[Migration 000065 rollback] Dropping source ACL policy tables...'; END $$;

DROP TABLE IF EXISTS source_acl_entries;
DROP TABLE IF EXISTS source_acl_snapshots;

DO $$ BEGIN RAISE NOTICE '[Migration 000065 rollback] Source ACL policy tables dropped'; END $$;
