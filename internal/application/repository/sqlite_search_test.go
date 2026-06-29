package repository

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupSQLiteSearchTestDB(t *testing.T, ddl string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(ddl).Error)
	return db
}

func requireExecSQLFile(t *testing.T, db *sql.DB, pathParts ...string) {
	t.Helper()
	sqlBytes, err := os.ReadFile(filepath.Join(pathParts...))
	require.NoError(t, err)
	_, err = db.Exec(string(sqlBytes))
	require.NoError(t, err)
}

func TestMessageRepositorySearchMessagesByKeyword_SQLite(t *testing.T) {
	db := setupSQLiteSearchTestDB(t, `
CREATE TABLE sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    title VARCHAR(255),
    deleted_at DATETIME
);
CREATE TABLE messages (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    rendered_content TEXT NOT NULL DEFAULT '',
    knowledge_references TEXT NOT NULL DEFAULT '[]',
    agent_steps TEXT DEFAULT NULL,
    mentioned_items TEXT DEFAULT '[]',
    images TEXT DEFAULT '[]',
    is_completed BOOLEAN NOT NULL DEFAULT 0,
    is_fallback BOOLEAN NOT NULL DEFAULT 0,
    channel VARCHAR(50) NOT NULL DEFAULT '',
    agent_duration_ms INTEGER DEFAULT 0,
    knowledge_id VARCHAR(36),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`)
	require.NoError(t, db.Exec(`
INSERT INTO sessions (id, tenant_id, title) VALUES ('s1', 7, 'Alpha Session'), ('s2', 7, 'Beta Session');
INSERT INTO messages (id, request_id, session_id, role, content)
VALUES
  ('m1', 'r1', 's1', 'user', 'Need QUARTERLY budget'),
  ('m2', 'r2', 's2', 'user', 'unrelated');
`).Error)

	repo := NewMessageRepository(db)
	got, err := repo.SearchMessagesByKeyword(context.Background(), 7, "quarterly", nil, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "m1", got[0].ID)
	assert.Equal(t, "Alpha Session", got[0].SessionTitle)
}

func TestUserRepositorySearchUsers_SQLite(t *testing.T) {
	db := setupSQLiteSearchTestDB(t, `
CREATE TABLE users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL,
    email VARCHAR(255) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    avatar VARCHAR(500),
    tenant_id INTEGER,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    can_access_all_tenants BOOLEAN NOT NULL DEFAULT 0,
    is_system_admin BOOLEAN NOT NULL DEFAULT 0,
    preferences TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`)
	require.NoError(t, db.Exec(`
INSERT INTO users (id, username, email, password_hash, is_active)
VALUES
  ('u1', 'AliceOps', 'alice@example.test', 'x', 1),
  ('u2', 'bob', 'bob@example.test', 'x', 1),
  ('u3', 'alice-disabled', 'disabled@example.test', 'x', 0);
`).Error)

	repo := NewUserRepository(db)
	got, err := repo.SearchUsers(context.Background(), "alice", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "u1", got[0].ID)
}

func TestOrganizationRepositoryListSearchable_SQLite(t *testing.T) {
	db := setupSQLiteSearchTestDB(t, `
CREATE TABLE organizations (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    owner_id VARCHAR(36) NOT NULL,
    owner_tenant_id INTEGER NOT NULL DEFAULT 0,
    invite_code VARCHAR(32),
    require_approval BOOLEAN DEFAULT 0,
    invite_code_expires_at DATETIME,
    invite_code_validity_days INTEGER NOT NULL DEFAULT 7,
    avatar VARCHAR(512) DEFAULT '',
    searchable BOOLEAN NOT NULL DEFAULT 0,
    member_limit INTEGER NOT NULL DEFAULT 50,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
`)
	require.NoError(t, db.Exec(`
INSERT INTO organizations (id, name, description, owner_id, searchable)
VALUES
  ('org-alpha', 'Finance Hub', 'Quarterly planning', 'owner', 1),
  ('org-beta', 'Engineering', 'Build notes', 'owner', 1),
  ('org-hidden', 'Quarterly hidden', 'private', 'owner', 0);
`).Error)

	repo := NewOrganizationRepository(db)
	got, err := repo.ListSearchable(context.Background(), "quarterly", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "org-alpha", got[0].ID)
}

func TestSQLiteKnowledgeTagRelationsMigration(t *testing.T) {
	db := setupSQLiteSearchTestDB(t, `
CREATE TABLE knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tag_id VARCHAR(36),
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);
INSERT INTO knowledges (id, tag_id) VALUES ('k1', 'tag-a'), ('k2', ''), ('k3', 'tag-b');
`)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	requireExecSQLFile(t, sqlDB, "..", "..", "..", "migrations", "sqlite", "000004_knowledge_tag_relations.up.sql")

	var rows []types.KnowledgeTagRelation
	require.NoError(t, db.Order("knowledge_id ASC").Find(&rows).Error)
	require.Len(t, rows, 2)
	assert.Equal(t, "k1", rows[0].KnowledgeID)
	assert.Equal(t, "tag-a", rows[0].TagID)
	assert.Equal(t, "k3", rows[1].KnowledgeID)
	assert.Equal(t, "tag-b", rows[1].TagID)
}
