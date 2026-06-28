package types

import (
	"time"

	"gorm.io/gorm"
)

const (
	SourceACLVisibilityRestricted = "restricted"
	SourceACLVisibilityAllCompany = "all_company"
	SourceACLVisibilityPublic     = "public"

	SourceACLStatusReady    = "ready"
	SourceACLStatusInvalid  = "invalid"
	SourceACLStatusStale    = "stale"
	SourceACLStatusUnmapped = "unmapped"

	SourceACLProvenanceDirect    = "direct"
	SourceACLProvenanceInherited = "inherited"

	SourceACLSubjectWeComUser       = "wecom_user"
	SourceACLSubjectWeComDepartment = "wecom_department"
	SourceACLSubjectWeComGroup      = "wecom_group"
	SourceACLSubjectAllCompany      = "all_company"
	SourceACLSubjectPublic          = "public"

	SourceACLPermissionRead = "read"
)

const (
	SourceACLReasonAllowedDirectUser      = "allowed_direct_user"
	SourceACLReasonAllowedDepartment      = "allowed_department"
	SourceACLReasonAllowedGroup           = "allowed_group"
	SourceACLReasonAllowedAllCompany      = "allowed_all_company"
	SourceACLReasonAllowedPublic          = "allowed_public"
	SourceACLReasonInvalidRequest         = "invalid_request"
	SourceACLReasonServiceKeyActorMissing = "service_key_actor_required"
	SourceACLReasonSnapshotMissing        = "acl_snapshot_missing"
	SourceACLReasonSnapshotInvalid        = "acl_snapshot_invalid"
	SourceACLReasonSnapshotStale          = "acl_snapshot_stale"
	SourceACLReasonSnapshotUnmapped       = "acl_snapshot_unmapped"
	SourceACLReasonActorUnbound           = "actor_unbound"
	SourceACLReasonNoMatchingSubject      = "acl_no_matching_subject"
)

type SourceACLSnapshot struct {
	ID                      uint64            `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID                uint64            `json:"tenant_id" gorm:"not null;index:idx_source_acl_snapshot_knowledge,priority:1;index:idx_source_acl_snapshot_source,priority:1;index"`
	Provider                string            `json:"provider" gorm:"type:varchar(64);not null;index:idx_source_acl_snapshot_source,priority:2"`
	KnowledgeID             string            `json:"knowledge_id" gorm:"type:varchar(36);not null;index:idx_source_acl_snapshot_knowledge,priority:2"`
	KnowledgeBaseID         string            `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index"`
	SourceItemID            string            `json:"source_item_id" gorm:"type:varchar(255);not null;index:idx_source_acl_snapshot_source,priority:3"`
	SourceResourceID        string            `json:"source_resource_id" gorm:"type:varchar(512);not null;default:''"`
	Visibility              string            `json:"visibility" gorm:"type:varchar(32);not null;default:'restricted';index"`
	Status                  string            `json:"status" gorm:"type:varchar(32);not null;default:'ready';index"`
	SyncedAt                time.Time         `json:"synced_at"`
	StaleAfter              *time.Time        `json:"stale_after,omitempty"`
	Provenance              string            `json:"provenance" gorm:"type:varchar(32);not null;default:'direct'"`
	InheritedFromResourceID string            `json:"inherited_from_resource_id" gorm:"type:varchar(512);not null;default:''"`
	SourceRevision          string            `json:"source_revision" gorm:"type:varchar(255);not null;default:''"`
	SourceHash              string            `json:"source_hash" gorm:"type:varchar(255);not null;default:''"`
	Metadata                JSON              `json:"metadata" gorm:"type:jsonb;default:'{}'"`
	Entries                 []*SourceACLEntry `json:"entries,omitempty" gorm:"foreignKey:SnapshotID;references:ID"`
	CreatedAt               time.Time         `json:"created_at"`
	UpdatedAt               time.Time         `json:"updated_at"`
	DeletedAt               gorm.DeletedAt    `json:"deleted_at" gorm:"index"`
}

func (SourceACLSnapshot) TableName() string { return "source_acl_snapshots" }

type SourceACLEntry struct {
	ID                      uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID                uint64    `json:"tenant_id" gorm:"not null;index:idx_source_acl_entries_subject,priority:1;index"`
	SnapshotID              uint64    `json:"snapshot_id" gorm:"not null;index:idx_source_acl_entries_snapshot"`
	Provider                string    `json:"provider" gorm:"type:varchar(64);not null;index"`
	SubjectType             string    `json:"subject_type" gorm:"type:varchar(64);not null;index:idx_source_acl_entries_subject,priority:2"`
	SubjectID               string    `json:"subject_id" gorm:"type:varchar(255);not null;index:idx_source_acl_entries_subject,priority:3"`
	Permission              string    `json:"permission" gorm:"type:varchar(32);not null;default:'read'"`
	Provenance              string    `json:"provenance" gorm:"type:varchar(32);not null;default:'direct'"`
	InheritedFromResourceID string    `json:"inherited_from_resource_id" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

func (SourceACLEntry) TableName() string { return "source_acl_entries" }

type SourceACLRecord struct {
	Snapshot *SourceACLSnapshot
	Entries  []*SourceACLEntry
}

type SourceACLDecision struct {
	Allowed        bool     `json:"allowed"`
	Reason         string   `json:"reason"`
	Provider       string   `json:"provider,omitempty"`
	KnowledgeID    string   `json:"knowledge_id,omitempty"`
	ActorUserID    string   `json:"actor_user_id,omitempty"`
	WeComUserID    string   `json:"wecom_userid,omitempty"`
	Purpose        string   `json:"purpose,omitempty"`
	SubjectIDs     []string `json:"subject_ids,omitempty"`
	ServiceKeyCall bool     `json:"service_key_call,omitempty"`
}
