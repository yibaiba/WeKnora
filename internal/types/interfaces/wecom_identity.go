package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type WeComIdentitySyncInput struct {
	CorpID        string
	Secret        string
	DepartmentIDs []string
	FetchChild    bool
}

type WeComBindingInput struct {
	WeKnoraUserID string
	WeComUserID   string
	Source        string
}

type WeComBindingImportRow struct {
	RowNumber     int    `json:"row_number"`
	WeKnoraUserID string `json:"weknora_user_id,omitempty"`
	Email         string `json:"email,omitempty"`
	WeComUserID   string `json:"wecom_userid"`
}

type WeComBindingImportResult struct {
	RowNumber     int    `json:"row_number"`
	WeKnoraUserID string `json:"weknora_user_id,omitempty"`
	Email         string `json:"email,omitempty"`
	WeComUserID   string `json:"wecom_userid,omitempty"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

type WeComBindingListQuery struct {
	Search string
	Offset int
	Limit  int
}

type WeComIdentitySyncResult struct {
	Departments int       `json:"departments"`
	Users       int       `json:"users"`
	SyncedAt    time.Time `json:"synced_at"`
}

type WeComIdentityRepository interface {
	UpsertIdentities(ctx context.Context, identities []*types.WeComIdentity) error
	UpsertDepartments(ctx context.Context, departments []*types.WeComDepartment) error
	ReplaceIdentityDepartments(
		ctx context.Context,
		tenantID uint64,
		userID string,
		departmentIDs []string,
		syncedAt time.Time,
	) error
	FindIdentity(ctx context.Context, tenantID uint64, userID string) (*types.WeComIdentity, error)
	UpsertBinding(ctx context.Context, binding *types.WeComUserBinding) (*types.WeComUserBinding, error)
	DeleteBinding(ctx context.Context, tenantID uint64, weknoraUserID string) error
	ListBindings(
		ctx context.Context,
		tenantID uint64,
		q WeComBindingListQuery,
	) ([]*types.WeComUserBinding, int64, error)
	ResolveSubject(ctx context.Context, tenantID uint64, weknoraUserID string) (*types.WeComACLSubject, error)
}

type WeComIdentityService interface {
	Sync(ctx context.Context, tenantID uint64, input WeComIdentitySyncInput) (*WeComIdentitySyncResult, error)
	CreateOrUpdateBinding(
		ctx context.Context,
		tenantID uint64,
		input WeComBindingInput,
	) (*types.WeComUserBinding, error)
	DeleteBinding(ctx context.Context, tenantID uint64, weknoraUserID string) error
	ListBindings(
		ctx context.Context,
		tenantID uint64,
		q WeComBindingListQuery,
	) ([]*types.WeComUserBinding, int64, error)
	ImportBindings(
		ctx context.Context,
		tenantID uint64,
		rows []WeComBindingImportRow,
	) []WeComBindingImportResult
	ResolveSubject(ctx context.Context, tenantID uint64, weknoraUserID string) (*types.WeComACLSubject, error)
}
