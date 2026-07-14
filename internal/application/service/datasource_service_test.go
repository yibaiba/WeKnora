package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncCancelsWhenKnowledgeBaseDeleted(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-deleted",
		Type:            types.ConnectorTypeRSS,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo("kb-deleted", ds)
	syncLog := &types.SyncLog{
		ID:           "log-1",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}

	svc := &DataSourceService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		kbService:   &processSyncKBService{getErr: apprepo.ErrKnowledgeBaseNotFound},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusCanceled, updated.Status)
	assert.Equal(t, "knowledge base has been deleted", updated.ErrorMessage)
	require.NotNil(t, updated.FinishedAt)
}

func TestProcessSyncPersistsPartialWhenItemIngestionFails(t *testing.T) {
	config := &types.DataSourceConfig{Type: "partial_test"}
	configJSON, err := config.ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID:              "ds-partial",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Name:            "WeDrive",
		Type:            "partial_test",
		Config:          configJSON,
		Status:          types.DataSourceStatusActive,
		SyncMode:        types.SyncModeFull,
	}
	dsRepo := newKBDeleteDSRepo("kb-1", ds)
	syncLog := &types.SyncLog{
		ID:           "log-partial",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(&partialTestConnector{}))

	svc := &DataSourceService{
		dsRepo:            dsRepo,
		syncLogRepo:       syncLogRepo,
		kbService:         &processSyncKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}},
		knowledgeService:  &captureKnowledgeService{repo: &captureKnowledgeRepository{}},
		tenantRepo:        &processSyncTenantRepo{},
		tagService:        &processSyncTagService{},
		connectorRegistry: registry,
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
		ForceFull:    true,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusPartial, updated.Status)
	assert.Equal(t, 2, updated.ItemsTotal)
	assert.Equal(t, 1, updated.ItemsCreated)
	assert.Equal(t, 1, updated.ItemsFailed)
	assert.Contains(t, updated.ErrorMessage, "Some items failed")
}

type processSyncKBService struct {
	kb     *types.KnowledgeBase
	getErr error
}

func (s *processSyncKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.kb, nil
}
func (s *processSyncKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.kb, nil
}
func (s *processSyncKBService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return nil
}
func (s *processSyncKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) UpdateKnowledgeBase(
	context.Context, string, string, string, *types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *processSyncKBService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}
func (s *processSyncKBService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, nil
}
func (s *processSyncKBService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}
func (s *processSyncKBService) CopyKnowledgeBase(context.Context, string, string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}
func (s *processSyncKBService) DuplicateKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetRepository() interfaces.KnowledgeBaseRepository { return nil }
func (s *processSyncKBService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

var _ interfaces.KnowledgeBaseService = (*processSyncKBService)(nil)

type processSyncSyncLogRepo struct {
	logs map[string]*types.SyncLog
}

func (r *processSyncSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	log, ok := r.logs[id]
	if !ok {
		return nil, errors.New("sync log not found")
	}
	return log, nil
}
func (r *processSyncSyncLogRepo) FindByDataSource(context.Context, string, int, int) ([]*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	return false, nil
}
func (r *processSyncSyncLogRepo) Update(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) UpdateResult(_ context.Context, log *types.SyncLog) error {
	return r.Update(context.Background(), log)
}
func (r *processSyncSyncLogRepo) CancelPendingByDataSource(context.Context, string) error {
	return nil
}
func (r *processSyncSyncLogRepo) CleanupOldLogs(context.Context, int) error { return nil }

type partialTestConnector struct{}

func (c *partialTestConnector) Type() string { return "partial_test" }
func (c *partialTestConnector) Validate(context.Context, *types.DataSourceConfig) error {
	return nil
}
func (c *partialTestConnector) ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error) {
	return nil, nil
}
func (c *partialTestConnector) ResolveResourceAncestors(
	context.Context, *types.DataSourceConfig, []string,
) ([]string, error) {
	return nil, nil
}
func (c *partialTestConnector) FetchAll(
	context.Context, *types.DataSourceConfig, []string,
) ([]types.FetchedItem, error) {
	return []types.FetchedItem{
		{
			ExternalID:       "ok",
			Title:            "Ok.md",
			FileName:         "Ok.md",
			Content:          []byte("# ok"),
			SourceResourceID: "root",
		},
		{
			ExternalID:       "bad",
			Title:            "Bad.md",
			FileName:         "Bad.md",
			SourceResourceID: "root",
			Metadata:         map[string]string{"error": "download failed"},
		},
	}, nil
}
func (c *partialTestConnector) FetchIncremental(
	context.Context, *types.DataSourceConfig, *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor, error) {
	return nil, nil, errors.New("not used")
}

type processSyncTenantRepo struct {
	interfaces.TenantRepository
}

func (r *processSyncTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: 1, Name: "tenant"}, nil
}

type processSyncTagService struct {
	interfaces.KnowledgeTagService
}

func (s *processSyncTagService) FindOrCreateTagByName(
	context.Context, string, string,
) (*types.KnowledgeTag, error) {
	return &types.KnowledgeTag{ID: "tag-1", Name: "WeDrive"}, nil
}

func TestAllFetchedItemsFailedError(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  2,
		Failed: 2,
		Errors: []string{"doc one: export failed"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all fetched items failed during sync (2/2)")
	assert.Contains(t, err.Error(), "doc one: export failed")
}

func TestAllFetchedItemsFailedErrorIgnoresPartialFailure(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Created: 1,
		Failed:  2,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorIgnoresSkippedItems(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Skipped: 3,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorTruncatesLongDetail(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  1,
		Failed: 1,
		Errors: []string{strings.Repeat("x", 600)},
	})
	require.Error(t, err)
	assert.LessOrEqual(t, len(err.Error()), 560)
	assert.Contains(t, err.Error(), "...")
}

func TestProcessConfigFromSettings(t *testing.T) {
	raw := map[string]interface{}{
		"chunking_config": map[string]interface{}{"chunk_size": float64(768)},
		"parser_engine_overrides": map[string]interface{}{
			"pdf_force_scanned": "true",
		},
	}

	overrides, err := processConfigFromSettings(map[string]interface{}{"process_config": raw})
	require.NoError(t, err)
	require.NotNil(t, overrides)
	require.NotNil(t, overrides.ChunkingConfig)
	assert.Equal(t, 768, overrides.ChunkingConfig.ChunkSize)
	assert.Equal(t, "true", overrides.ParserEngineOverrides["pdf_force_scanned"])
}

func TestSyncStatusFromResultMarksItemFailuresPartial(t *testing.T) {
	status, message := syncStatusFromResult(&types.SyncResult{Total: 2, Created: 1, Failed: 1}, nil)

	assert.Equal(t, types.SyncLogStatusPartial, status)
	assert.Contains(t, message, "Some items failed")
}

func TestIngestItemForwardsProcessOverridesToFileIngestion(t *testing.T) {
	chunkSize := 512
	overrides := &types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: chunkSize},
	}
	knowledgeSvc := &captureKnowledgeService{repo: &captureKnowledgeRepository{}}
	svc := &DataSourceService{knowledgeService: knowledgeSvc}
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeWeComWeDrive,
	}
	item := &types.FetchedItem{
		ExternalID:       "wecom_wedrive:space:file",
		Title:            "Doc.md",
		FileName:         "Doc.md",
		Content:          []byte("# doc"),
		SourceResourceID: "folder:space:folder",
		Metadata:         map[string]string{"provider": "wecom_wedrive"},
	}

	_, err := svc.ingestItem(context.Background(), ds, item, []string{"tag-1"}, overrides)
	require.NoError(t, err)
	require.Same(t, overrides, knowledgeSvc.fileOverrides)
	assert.Equal(t, "tag-1", knowledgeSvc.fileTagIDs[0])
	assert.Equal(t, types.ConnectorTypeWeComWeDrive, knowledgeSvc.fileChannel)
}

func TestIngestItemUpsertsSourceACLForRestrictedWeDriveItem(t *testing.T) {
	entries, err := json.Marshal([]types.SourceACLMetadataEntry{
		{
			SubjectType: types.SourceACLSubjectWeComUser,
			SubjectID:   "wx-a",
			Permission:  types.SourceACLPermissionRead,
		},
		{
			SubjectType: types.SourceACLSubjectWeComDepartment,
			SubjectID:   "42",
			Permission:  types.SourceACLPermissionRead,
		},
	})
	require.NoError(t, err)

	knowledgeSvc := &captureKnowledgeService{repo: &captureKnowledgeRepository{}}
	aclRepo := &captureSourceACLRepository{}
	svc := &DataSourceService{knowledgeService: knowledgeSvc, sourceACLRepo: aclRepo}
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeWeComWeDrive,
	}
	item := &types.FetchedItem{
		ExternalID:       "wecom_wedrive:space:file-1",
		Title:            "Doc.md",
		FileName:         "Doc.md",
		Content:          []byte("# doc"),
		SourceResourceID: "folder:space:folder-1",
		Metadata: map[string]string{
			"provider":                           types.ConnectorTypeWeComWeDrive,
			"file_id":                            "file-1",
			"access_mode":                        "restricted",
			"queryable_state":                    "restricted",
			"require_source_acl":                 "true",
			types.SourceACLMetadataKeyVisibility: "restricted",
			types.SourceACLMetadataKeyStatus:     "ready",
			types.SourceACLMetadataKeySourceHash: "acl:abc",
			types.SourceACLMetadataKeyEntries:    string(entries),
		},
	}

	_, err = svc.ingestItem(context.Background(), ds, item, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, aclRepo.input.Snapshot)
	assert.NotContains(t, knowledgeSvc.fileMetadata, types.SourceACLMetadataKeyEntries)
	assert.Equal(t, "restricted", knowledgeSvc.fileMetadata["access_mode"])
	assert.Equal(t, uint64(7), aclRepo.input.Snapshot.TenantID)
	assert.Equal(t, "knowledge-1", aclRepo.input.Snapshot.KnowledgeID)
	assert.Equal(t, "kb-1", aclRepo.input.Snapshot.KnowledgeBaseID)
	assert.Equal(t, "file-1", aclRepo.input.Snapshot.SourceItemID)
	assert.Equal(t, types.SourceACLVisibilityRestricted, aclRepo.input.Snapshot.Visibility)
	assert.Equal(t, "acl:abc", aclRepo.input.Snapshot.SourceHash)
	require.Len(t, aclRepo.input.Entries, 2)
	assert.Equal(t, types.SourceACLSubjectWeComUser, aclRepo.input.Entries[0].SubjectType)
	assert.Equal(t, "wx-a", aclRepo.input.Entries[0].SubjectID)
	assert.Equal(t, types.SourceACLSubjectWeComDepartment, aclRepo.input.Entries[1].SubjectType)
	assert.Equal(t, "42", aclRepo.input.Entries[1].SubjectID)
}

func TestHandleACLOnlyFetchedItemRefreshesExistingSnapshot(t *testing.T) {
	repo := &captureKnowledgeRepository{
		byExternalID: map[string]*types.Knowledge{
			"wecom_wedrive:space:file-1": {
				ID:              "knowledge-1",
				TenantID:        7,
				KnowledgeBaseID: "kb-1",
			},
		},
	}
	aclRepo := &captureSourceACLRepository{}
	svc := &DataSourceService{
		knowledgeService: &captureKnowledgeService{repo: repo},
		sourceACLRepo:    aclRepo,
	}
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            types.ConnectorTypeWeComWeDrive,
	}
	item := &types.FetchedItem{
		ExternalID:       "wecom_wedrive:space:file-1",
		Title:            "Doc.md",
		SourceResourceID: "folder:space:folder-1",
		Metadata: map[string]string{
			"provider":                           types.ConnectorTypeWeComWeDrive,
			"file_id":                            "file-1",
			"access_mode":                        "restricted",
			"queryable_state":                    "restricted",
			"require_source_acl":                 "true",
			types.SourceACLMetadataKeyVisibility: "restricted",
			types.SourceACLMetadataKeyStatus:     types.SourceACLStatusUnmapped,
			types.SourceACLMetadataKeySourceHash: "acl:revoked",
			types.SourceACLMetadataKeyEntries:    "[]",
		},
	}
	result := &types.SyncResult{Total: 1}

	handled := svc.handleACLOnlyFetchedItem(context.Background(), ds, item, result)
	require.True(t, handled)
	assert.Equal(t, 1, result.Updated)
	assert.Equal(t, types.SourceACLStatusUnmapped, aclRepo.input.Snapshot.Status)
	assert.Equal(t, "knowledge-1", aclRepo.input.Snapshot.KnowledgeID)
	assert.Empty(t, aclRepo.input.Entries)
}

type captureKnowledgeService struct {
	interfaces.KnowledgeService
	repo          interfaces.KnowledgeRepository
	fileOverrides *types.KnowledgeProcessOverrides
	fileTagIDs    []string
	fileChannel   string
	fileMetadata  map[string]string
}

type captureSourceACLRepository struct {
	interfaces.SourceACLRepository
	input interfaces.SourceACLUpsertInput
	err   error
}

func (r *captureSourceACLRepository) UpsertSnapshot(
	_ context.Context,
	input interfaces.SourceACLUpsertInput,
) (*types.SourceACLRecord, error) {
	r.input = input
	if r.err != nil {
		return nil, r.err
	}
	return &types.SourceACLRecord{Snapshot: input.Snapshot, Entries: input.Entries}, nil
}

func (s *captureKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *captureKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context,
	kbID string,
	_ *multipart.FileHeader,
	metadata map[string]string,
	_ *bool,
	_ string,
	tagIDs []string,
	channel string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.fileOverrides = processOverrides
	s.fileTagIDs = tagIDs
	s.fileChannel = channel
	s.fileMetadata = metadata
	return &types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: kbID}, nil
}

type captureKnowledgeRepository struct {
	interfaces.KnowledgeRepository
	byExternalID map[string]*types.Knowledge
}

func (r *captureKnowledgeRepository) FindByMetadataKey(
	_ context.Context,
	tenantID uint64,
	kbID string,
	key string,
	value string,
) (*types.Knowledge, error) {
	if key != "external_id" || r.byExternalID == nil {
		return nil, nil
	}
	knowledge := r.byExternalID[value]
	if knowledge == nil || knowledge.TenantID != tenantID || knowledge.KnowledgeBaseID != kbID {
		return nil, nil
	}
	return knowledge, nil
}

func (r *captureKnowledgeRepository) CheckKnowledgeExists(
	context.Context,
	uint64,
	string,
	*types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	return false, nil, nil
}
