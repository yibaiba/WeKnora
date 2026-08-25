-- folder_path is added idempotently by applySQLiteMergeSchemaPatches.
-- SQLite cannot ADD COLUMN IF NOT EXISTS, and this version may run after a
-- partial upgrade that already patched the column.
SELECT 1;
