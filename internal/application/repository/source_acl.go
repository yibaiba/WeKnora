package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var ErrSourceACLNotFound = errors.New("source acl snapshot not found")

type sourceACLRepository struct {
	db *gorm.DB
}

func NewSourceACLRepository(db *gorm.DB) interfaces.SourceACLRepository {
	return &sourceACLRepository{db: db}
}

func (r *sourceACLRepository) UpsertSnapshot(
	ctx context.Context,
	input interfaces.SourceACLUpsertInput,
) (*types.SourceACLRecord, error) {
	snapshot := normalizeSourceACLSnapshot(input.Snapshot)
	if err := validateSourceACLSnapshot(snapshot); err != nil {
		return nil, err
	}

	var out *types.SourceACLRecord
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := upsertSourceACLSnapshot(tx, snapshot)
		if err != nil {
			return err
		}
		entries := normalizeSourceACLEntries(stored, input.Entries)
		if err := tx.Where("snapshot_id = ?", stored.ID).Delete(&types.SourceACLEntry{}).Error; err != nil {
			return err
		}
		if len(entries) > 0 {
			if err := tx.Create(&entries).Error; err != nil {
				return err
			}
		}
		out = &types.SourceACLRecord{Snapshot: stored, Entries: entries}
		return nil
	})
	return out, err
}

func (r *sourceACLRepository) FindByKnowledgeID(
	ctx context.Context,
	tenantID uint64,
	knowledgeID string,
) (*types.SourceACLRecord, error) {
	var snapshot types.SourceACLSnapshot
	err := r.baseLookup(ctx).
		Where("tenant_id = ? AND knowledge_id = ?", tenantID, strings.TrimSpace(knowledgeID)).
		First(&snapshot).Error
	return sourceACLRecordFromLookup(&snapshot, err)
}

func (r *sourceACLRepository) FindBySourceItem(
	ctx context.Context,
	tenantID uint64,
	provider string,
	sourceItemID string,
) (*types.SourceACLRecord, error) {
	var snapshot types.SourceACLSnapshot
	err := r.baseLookup(ctx).
		Where(
			"tenant_id = ? AND provider = ? AND source_item_id = ?",
			tenantID,
			strings.TrimSpace(provider),
			strings.TrimSpace(sourceItemID),
		).
		First(&snapshot).Error
	return sourceACLRecordFromLookup(&snapshot, err)
}

func (r *sourceACLRepository) baseLookup(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Preload("Entries", func(db *gorm.DB) *gorm.DB {
			return db.Order("subject_type ASC, subject_id ASC, id ASC")
		}).
		Where("deleted_at IS NULL")
}

func upsertSourceACLSnapshot(tx *gorm.DB, snapshot *types.SourceACLSnapshot) (*types.SourceACLSnapshot, error) {
	var existing types.SourceACLSnapshot
	err := tx.Where("tenant_id = ? AND knowledge_id = ? AND deleted_at IS NULL", snapshot.TenantID, snapshot.KnowledgeID).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if err := tx.Create(snapshot).Error; err != nil {
			return nil, err
		}
		return snapshot, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]any{
		"provider":                   snapshot.Provider,
		"knowledge_base_id":          snapshot.KnowledgeBaseID,
		"source_item_id":             snapshot.SourceItemID,
		"source_resource_id":         snapshot.SourceResourceID,
		"visibility":                 snapshot.Visibility,
		"status":                     snapshot.Status,
		"synced_at":                  snapshot.SyncedAt,
		"stale_after":                snapshot.StaleAfter,
		"provenance":                 snapshot.Provenance,
		"inherited_from_resource_id": snapshot.InheritedFromResourceID,
		"source_revision":            snapshot.SourceRevision,
		"source_hash":                snapshot.SourceHash,
		"metadata":                   snapshot.Metadata,
		"updated_at":                 time.Now().UTC(),
	}
	if err := tx.Model(&existing).Updates(values).Error; err != nil {
		return nil, err
	}
	if err := tx.First(&existing, "id = ?", existing.ID).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

func sourceACLRecordFromLookup(snapshot *types.SourceACLSnapshot, err error) (*types.SourceACLRecord, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSourceACLNotFound
	}
	if err != nil {
		return nil, err
	}
	return &types.SourceACLRecord{Snapshot: snapshot, Entries: snapshot.Entries}, nil
}

func normalizeSourceACLSnapshot(snapshot *types.SourceACLSnapshot) *types.SourceACLSnapshot {
	if snapshot == nil {
		return nil
	}
	cp := *snapshot
	cp.Provider = strings.TrimSpace(cp.Provider)
	cp.KnowledgeID = strings.TrimSpace(cp.KnowledgeID)
	cp.KnowledgeBaseID = strings.TrimSpace(cp.KnowledgeBaseID)
	cp.SourceItemID = strings.TrimSpace(cp.SourceItemID)
	cp.SourceResourceID = strings.TrimSpace(cp.SourceResourceID)
	cp.Visibility = defaultString(cp.Visibility, types.SourceACLVisibilityRestricted)
	cp.Status = defaultString(cp.Status, types.SourceACLStatusReady)
	cp.Provenance = defaultString(cp.Provenance, types.SourceACLProvenanceDirect)
	cp.InheritedFromResourceID = strings.TrimSpace(cp.InheritedFromResourceID)
	cp.SourceRevision = strings.TrimSpace(cp.SourceRevision)
	cp.SourceHash = strings.TrimSpace(cp.SourceHash)
	if cp.SyncedAt.IsZero() {
		cp.SyncedAt = time.Now().UTC()
	}
	if len(cp.Metadata) == 0 {
		cp.Metadata = types.JSON(`{}`)
	}
	return &cp
}

func validateSourceACLSnapshot(snapshot *types.SourceACLSnapshot) error {
	if snapshot == nil {
		return errors.New("source acl snapshot is nil")
	}
	if snapshot.TenantID == 0 ||
		snapshot.Provider == "" ||
		snapshot.KnowledgeID == "" ||
		snapshot.KnowledgeBaseID == "" ||
		snapshot.SourceItemID == "" {
		return errors.New("source acl snapshot missing required fields")
	}
	return nil
}

func normalizeSourceACLEntries(
	snapshot *types.SourceACLSnapshot,
	entries []*types.SourceACLEntry,
) []*types.SourceACLEntry {
	out := make([]*types.SourceACLEntry, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		cp := *entry
		cp.ID = 0
		cp.TenantID = snapshot.TenantID
		cp.SnapshotID = snapshot.ID
		cp.Provider = defaultString(cp.Provider, snapshot.Provider)
		cp.SubjectType = strings.TrimSpace(cp.SubjectType)
		cp.SubjectID = normalizedACLSubjectID(cp.SubjectType, cp.SubjectID)
		cp.Permission = defaultString(cp.Permission, types.SourceACLPermissionRead)
		cp.Provenance = defaultString(cp.Provenance, types.SourceACLProvenanceDirect)
		cp.InheritedFromResourceID = strings.TrimSpace(cp.InheritedFromResourceID)
		if cp.SubjectType == "" || cp.SubjectID == "" {
			continue
		}
		key := cp.SubjectType + "\x00" + cp.SubjectID + "\x00" + cp.Permission
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, &cp)
	}
	return out
}

func normalizedACLSubjectID(subjectType, subjectID string) string {
	subjectID = strings.TrimSpace(subjectID)
	switch strings.TrimSpace(subjectType) {
	case types.SourceACLSubjectAllCompany, types.SourceACLSubjectPublic:
		if subjectID == "" {
			return "*"
		}
	}
	return subjectID
}
