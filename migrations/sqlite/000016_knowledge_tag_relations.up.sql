-- Table and backfill are created by 000010_knowledge_multi_tags. Keep this
-- file as a no-op CREATE so older Lite tests that applied this version still
-- succeed when the table already exists.
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_ktr_knowledge
    ON knowledge_tag_relations(knowledge_id);

CREATE INDEX IF NOT EXISTS idx_ktr_tag
    ON knowledge_tag_relations(tag_id);
