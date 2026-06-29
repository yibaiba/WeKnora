package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

const defaultKBIndexingStrategy = `{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}`

func TestSQLiteKnowledgeBasesWikiColumnsMigration(t *testing.T) {
	db := openSQLiteMigrationTestDB(t)
	defer db.Close()

	createLegacyKnowledgeBasesTable(t, db)
	requireExecFile(t, db, "..", "..", "migrations", "sqlite", "000003_kb_wiki_columns.up.sql")

	columns := sqliteColumnNames(t, db, "knowledge_bases")
	requireSQLiteColumn(t, columns, "wiki_config")
	requireSQLiteColumn(t, columns, "indexing_strategy")

	var got string
	err := db.QueryRow("SELECT indexing_strategy FROM knowledge_bases WHERE id = ?", "kb-legacy").Scan(&got)
	if err != nil {
		t.Fatalf("query backfilled indexing_strategy: %v", err)
	}
	if got != defaultKBIndexingStrategy {
		t.Fatalf("indexing_strategy = %q, want %q", got, defaultKBIndexingStrategy)
	}
}

func openSQLiteMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "weknora.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return db
}

func createLegacyKnowledgeBasesTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
CREATE TABLE knowledge_bases (
    id VARCHAR(36) PRIMARY KEY,
    type VARCHAR(32) NOT NULL DEFAULT 'document'
);
INSERT INTO knowledge_bases (id, type) VALUES ('kb-legacy', 'document');
`)
	if err != nil {
		t.Fatalf("create legacy knowledge_bases table: %v", err)
	}
}

func requireExecFile(t *testing.T, db *sql.DB, pathParts ...string) {
	t.Helper()
	sqlBytes, err := os.ReadFile(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if _, err := db.Exec(string(sqlBytes)); err != nil {
		t.Fatalf("execute migration file: %v", err)
	}
}

func sqliteColumnNames(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("read table info for %s: %v", table, err)
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table info for %s: %v", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table info for %s: %v", table, err)
	}
	return columns
}

func requireSQLiteColumn(t *testing.T, columns map[string]struct{}, name string) {
	t.Helper()
	if _, ok := columns[name]; !ok {
		t.Fatalf("missing sqlite column %q", name)
	}
}
