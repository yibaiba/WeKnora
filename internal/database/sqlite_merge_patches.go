package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	_ "github.com/mattn/go-sqlite3"
)

type sqliteColumnPatch struct {
	table  string
	column string
	decl   string
}

var sqliteMergeColumnPatches = []sqliteColumnPatch{
	{table: "wiki_pages", column: "last_edit_source", decl: "VARCHAR(16) NOT NULL DEFAULT ''"},
	{table: "wiki_pages", column: "last_editor_id", decl: "VARCHAR(64) NOT NULL DEFAULT ''"},
	{table: "tenant_api_keys", column: "owner_user_id", decl: "VARCHAR(36)"},
	{table: "knowledges", column: "folder_path", decl: "VARCHAR(1024) NOT NULL DEFAULT ''"},
	{table: "knowledge_bases", column: "auto_tag_config", decl: "TEXT"},
	{table: "knowledge_bases", column: "wiki_config", decl: "TEXT"},
	{table: "knowledge_bases", column: "indexing_strategy", decl: "TEXT DEFAULT '{\"vector_enabled\":true,\"keyword_enabled\":true,\"wiki_enabled\":false,\"graph_enabled\":false}'"},
	{table: "messages", column: "attachments", decl: "TEXT NOT NULL DEFAULT '[]'"},
	{table: "messages", column: "used_memories", decl: "TEXT"},
	{table: "tenants", column: "memory_config", decl: "TEXT"},
	{table: "tenant_invitations", column: "token", decl: "VARCHAR(64) NOT NULL DEFAULT ''"},
	{table: "tenant_invitations", column: "accepted_count", decl: "INTEGER NOT NULL DEFAULT 0"},
	{table: "memory_subjects", column: "consolidated_at", decl: "DATETIME"},
	{table: "memory_subjects", column: "forced_consolidated_at", decl: "DATETIME"},
	{table: "memory_topic_stats", column: "aliases", decl: "TEXT NOT NULL DEFAULT '[]'"},
}

func applySQLiteMergeSchemaPatchesIfNeeded(dsn string, opts MigrationOptions) error {
	if opts.SQLiteDBPath == "" && !strings.HasPrefix(dsn, "sqlite3://") {
		return nil
	}
	path := opts.SQLiteDBPath
	if path == "" {
		path = strings.TrimPrefix(dsn, "sqlite3://")
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return fmt.Errorf("open sqlite for merge schema patches: %w", err)
	}
	defer db.Close()
	return applySQLiteMergeSchemaPatches(db)
}

func applySQLiteMergeSchemaPatches(db *sql.DB) error {
	ctx := context.Background()
	for _, patch := range sqliteMergeColumnPatches {
		if err := sqliteEnsureColumn(db, patch.table, patch.column, patch.decl); err != nil {
			return err
		}
	}
	if err := sqliteExecIfTableExists(db, "tenant_api_keys",
		`CREATE INDEX IF NOT EXISTS idx_tenant_api_keys_tenant_owner ON tenant_api_keys(tenant_id, owner_user_id)`); err != nil {
		return fmt.Errorf("ensure idx_tenant_api_keys_tenant_owner: %w", err)
	}
	if err := sqliteExecIfTableExists(db, "knowledges",
		`CREATE INDEX IF NOT EXISTS idx_knowledges_folder_path ON knowledges(tenant_id, knowledge_base_id, folder_path)`); err != nil {
		return fmt.Errorf("ensure idx_knowledges_folder_path: %w", err)
	}
	if err := sqliteExecIfTableExists(db, "tenant_invitations",
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_token ON tenant_invitations(token) WHERE token <> '' AND deleted_at IS NULL`); err != nil {
		return fmt.Errorf("ensure idx_tenant_invitations_token: %w", err)
	}
	if err := sqliteExecIfTableExists(db, "knowledges", `
UPDATE knowledges
SET folder_path = substr(
        rtrim(file_name, replace(file_name, '/', '')),
        1,
        length(rtrim(file_name, replace(file_name, '/', ''))) - 1
    ),
    file_name = substr(
        file_name,
        length(rtrim(file_name, replace(file_name, '/', ''))) + 1
    )
WHERE file_name LIKE '%/%' AND (folder_path IS NULL OR folder_path = '')
`); err != nil {
		return fmt.Errorf("backfill knowledges.folder_path: %w", err)
	}
	logger.Infof(ctx, "Applied SQLite merge schema patches")
	return nil
}

func sqliteEnsureColumn(db *sql.DB, table, column, decl string) error {
	exists, err := sqliteMergeTableExists(db, table)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	has, err := sqliteHasColumn(db, table, column)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	if err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}

func sqliteExecIfTableExists(db *sql.DB, table, stmt string) error {
	exists, err := sqliteMergeTableExists(db, table)
	if err != nil || !exists {
		return err
	}
	_, err = db.Exec(stmt)
	return err
}

func sqliteMergeTableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lookup table %s: %w", table, err)
	}
	return true, nil
}

func sqliteHasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, fmt.Errorf("pragma table_info(%s): %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
