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

INSERT OR IGNORE INTO knowledge_tag_relations (knowledge_id, tag_id, created_at)
SELECT id, tag_id, COALESCE(updated_at, CURRENT_TIMESTAMP)
FROM knowledges
WHERE tag_id IS NOT NULL AND tag_id != ''
  AND deleted_at IS NULL;
