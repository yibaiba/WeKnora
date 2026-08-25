-- Idempotent repair for origin 000079-000081. This fork previously occupied
-- those version numbers (wecom / source ACL / personal API keys), so databases
-- that already recorded 000079-000081 skip the upstream files. ADD COLUMN
-- IF NOT EXISTS is safe on databases that did apply the upstream versions.

ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS folder_path VARCHAR(1024) NOT NULL DEFAULT '';

UPDATE knowledges
SET folder_path = LEFT(
        REGEXP_REPLACE(file_name, '/[^/]*$', ''),
        1024
    ),
    file_name = REGEXP_REPLACE(file_name, '^.*/', '')
WHERE file_name LIKE '%/%'
  AND folder_path = '';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path);

ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS auto_tag_config JSONB;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS artifacts JSONB DEFAULT '[]'::jsonb;
