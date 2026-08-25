ALTER TABLE knowledge_bases
    ADD COLUMN wiki_config TEXT;

ALTER TABLE knowledge_bases
    ADD COLUMN indexing_strategy TEXT NOT NULL DEFAULT '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}';

UPDATE knowledge_bases
SET indexing_strategy = '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'
WHERE indexing_strategy IS NULL OR indexing_strategy = '';
