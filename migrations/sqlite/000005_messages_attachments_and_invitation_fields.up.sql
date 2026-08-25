-- attachments / invitation token columns are added by applySQLiteMergeSchemaPatches.

-- Share-link rows use invitee_user_id=''; relax the pending uniqueness index
-- so multiple share links can coexist on one tenant (see versioned 000054).
DROP INDEX IF EXISTS idx_tenant_invitations_unique_pending;
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending'
      AND deleted_at IS NULL
      AND invitee_user_id <> '';
