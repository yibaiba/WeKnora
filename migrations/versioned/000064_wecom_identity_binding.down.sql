-- Rollback: 000064_wecom_identity_binding
DO $$ BEGIN RAISE NOTICE '[Migration 000064 rollback] Dropping WeCom identity binding tables...'; END $$;

DROP TABLE IF EXISTS wecom_user_bindings;
DROP TABLE IF EXISTS wecom_identity_departments;
DROP TABLE IF EXISTS wecom_departments;
DROP TABLE IF EXISTS wecom_identities;

DO $$ BEGIN RAISE NOTICE '[Migration 000064 rollback] WeCom identity binding tables dropped'; END $$;
