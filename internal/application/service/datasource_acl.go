package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func (s *DataSourceService) handleACLOnlyFetchedItem(
	ctx context.Context,
	ds *types.DataSource,
	item *types.FetchedItem,
	result *types.SyncResult,
) bool {
	if s == nil || s.knowledgeService == nil || ds == nil || item == nil || result == nil || item.ExternalID == "" {
		return false
	}
	repo := s.knowledgeService.GetRepository()
	if repo == nil {
		return false
	}
	existing, err := repo.FindByMetadataKey(ctx, ds.TenantID, ds.KnowledgeBaseID, "external_id", item.ExternalID)
	if err != nil {
		result.Failed++
		result.Errors = append(result.Errors, fmt.Sprintf("%s: source ACL existing knowledge lookup failed: %v", item.Title, err))
		return true
	}
	if existing == nil {
		return false
	}
	if err := s.upsertItemSourceACL(ctx, ds, item, existing); err != nil {
		result.Failed++
		result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", item.Title, err))
		return true
	}
	result.Updated++
	return true
}

func (s *DataSourceService) upsertItemSourceACL(
	ctx context.Context,
	ds *types.DataSource,
	item *types.FetchedItem,
	knowledge *types.Knowledge,
) error {
	if !dataSourceItemRequiresSourceACL(item) {
		return nil
	}
	if s.sourceACLRepo == nil {
		return fmt.Errorf("source ACL repository is not configured")
	}
	input, err := sourceACLUpsertInputFromFetchedItem(ds, item, knowledge)
	if err != nil {
		return err
	}
	_, err = s.sourceACLRepo.UpsertSnapshot(ctx, input)
	return err
}

func dataSourceItemRequiresSourceACL(item *types.FetchedItem) bool {
	if item == nil || len(item.Metadata) == 0 {
		return false
	}
	return metadataBool(item.Metadata[sourceACLMetadataRequireACL]) ||
		strings.EqualFold(item.Metadata[sourceACLMetadataAccessMode], sourceACLMetadataRestricted) ||
		strings.EqualFold(item.Metadata[sourceACLMetadataQueryableState], sourceACLMetadataRestricted)
}

func dataSourceMetadataKeyIsTransient(key string) bool {
	return strings.TrimSpace(key) == types.SourceACLMetadataKeyEntries
}

func sourceACLUpsertInputFromFetchedItem(
	ds *types.DataSource,
	item *types.FetchedItem,
	knowledge *types.Knowledge,
) (interfaces.SourceACLUpsertInput, error) {
	if ds == nil || item == nil || knowledge == nil {
		return interfaces.SourceACLUpsertInput{}, fmt.Errorf("source ACL input missing data source, item, or knowledge")
	}
	metadata := item.Metadata
	entries, err := sourceACLEntriesFromMetadata(metadata)
	if err != nil {
		return interfaces.SourceACLUpsertInput{}, err
	}
	status := metadataDefault(metadata[types.SourceACLMetadataKeyStatus], types.SourceACLStatusReady)
	if status == types.SourceACLStatusReady && len(entries) == 0 &&
		!sourceACLVisibilityAllowsWithoutSubject(metadata[types.SourceACLMetadataKeyVisibility]) {
		return interfaces.SourceACLUpsertInput{}, fmt.Errorf("source ACL ready snapshot has no entries")
	}
	syncedAt, err := sourceACLSyncedAt(metadata[types.SourceACLMetadataKeySyncedAt])
	if err != nil {
		return interfaces.SourceACLUpsertInput{}, err
	}
	return interfaces.SourceACLUpsertInput{
		Snapshot: &types.SourceACLSnapshot{
			TenantID:                ds.TenantID,
			Provider:                metadataDefault(metadata["provider"], ds.Type),
			KnowledgeID:             knowledge.ID,
			KnowledgeBaseID:         knowledge.KnowledgeBaseID,
			SourceItemID:            metadataDefault(metadata["file_id"], item.ExternalID),
			SourceResourceID:        metadataDefault(item.SourceResourceID, metadata["source_resource_id"]),
			Visibility:              metadataDefault(metadata[types.SourceACLMetadataKeyVisibility], types.SourceACLVisibilityRestricted),
			Status:                  status,
			SyncedAt:                syncedAt,
			Provenance:              metadataDefault(metadata[types.SourceACLMetadataKeyProvenance], types.SourceACLProvenanceDirect),
			InheritedFromResourceID: metadata[types.SourceACLMetadataKeyInheritedFromResourceID],
			SourceHash:              metadata[types.SourceACLMetadataKeySourceHash],
			Metadata:                sourceACLSnapshotMetadata(metadata),
		},
		Entries: entries,
	}, nil
}

func sourceACLEntriesFromMetadata(metadata map[string]string) ([]*types.SourceACLEntry, error) {
	raw := strings.TrimSpace(metadata[types.SourceACLMetadataKeyEntries])
	if raw == "" {
		return nil, nil
	}
	var decoded []types.SourceACLMetadataEntry
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("parse source ACL entries metadata: %w", err)
	}
	entries := make([]*types.SourceACLEntry, 0, len(decoded))
	for _, entry := range decoded {
		subjectType := strings.TrimSpace(entry.SubjectType)
		subjectID := strings.TrimSpace(entry.SubjectID)
		if subjectType == "" || subjectID == "" {
			continue
		}
		entries = append(entries, &types.SourceACLEntry{
			Provider:                metadata["provider"],
			SubjectType:             subjectType,
			SubjectID:               subjectID,
			Permission:              metadataDefault(entry.Permission, types.SourceACLPermissionRead),
			Provenance:              metadataDefault(entry.Provenance, types.SourceACLProvenanceDirect),
			InheritedFromResourceID: entry.InheritedFromResourceID,
		})
	}
	return entries, nil
}

func sourceACLSnapshotMetadata(metadata map[string]string) types.JSON {
	payload := map[string]string{}
	for _, key := range []string{
		"external_id",
		"space_id",
		"file_id",
		"father_id",
		"source_path",
		"source_resource_id",
		"access_mode",
		"queryable_state",
		types.SourceACLMetadataKeyEntryCount,
	} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			payload[key] = value
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return types.JSON(`{}`)
	}
	return types.JSON(data)
}

func sourceACLSyncedAt(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse source ACL synced_at: %w", err)
	}
	return parsed.UTC(), nil
}

func sourceACLVisibilityAllowsWithoutSubject(visibility string) bool {
	switch strings.TrimSpace(visibility) {
	case types.SourceACLVisibilityAllCompany, types.SourceACLVisibilityPublic:
		return true
	default:
		return false
	}
}

func metadataDefault(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return fallback
}

func metadataBool(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "yes":
		return true
	default:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			return parsed
		}
		return false
	}
}
