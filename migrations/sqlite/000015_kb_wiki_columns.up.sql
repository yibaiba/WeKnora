-- wiki_config / indexing_strategy are created in 000000_init (and added
-- idempotently by applySQLiteMergeSchemaPatches on older Lite DBs).
UPDATE knowledge_bases
SET indexing_strategy = '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'
WHERE indexing_strategy IS NULL OR indexing_strategy = '';
