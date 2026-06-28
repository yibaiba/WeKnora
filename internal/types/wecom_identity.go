package types

import (
	"time"

	"gorm.io/gorm"
)

const (
	WeComProvider = "wecom"

	WeComIdentityStatusActive      = "active"
	WeComIdentityStatusSuspended   = "suspended"
	WeComIdentityStatusDeleted     = "deleted"
	WeComIdentityStatusUnavailable = "unavailable"

	WeComBindingStatusActive  = "active"
	WeComBindingStatusDeleted = "deleted"

	WeComBindingSourceAdmin = "admin"
)

type WeComIdentity struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID  uint64         `json:"tenant_id" gorm:"not null;index:idx_wecom_identities_tenant_userid,priority:1;index"`
	Provider  string         `json:"provider" gorm:"type:varchar(32);not null;default:'wecom'"`
	UserID    string         `json:"userid" gorm:"column:userid;type:varchar(128);not null;index:idx_wecom_identities_tenant_userid,priority:2"`
	Name      string         `json:"name" gorm:"type:varchar(255);default:''"`
	Email     string         `json:"email" gorm:"type:varchar(255);default:''"`
	Mobile    string         `json:"mobile" gorm:"type:varchar(64);default:''"`
	Avatar    string         `json:"avatar" gorm:"type:varchar(1024);default:''"`
	Status    string         `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	SyncedAt  time.Time      `json:"synced_at"`
	Metadata  JSON           `json:"metadata" gorm:"type:jsonb;default:'{}'"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (WeComIdentity) TableName() string { return "wecom_identities" }

type WeComDepartment struct {
	ID           uint64         `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID     uint64         `json:"tenant_id" gorm:"not null;index:idx_wecom_departments_tenant_dept,priority:1;index"`
	Provider     string         `json:"provider" gorm:"type:varchar(32);not null;default:'wecom'"`
	DepartmentID string         `json:"department_id" gorm:"type:varchar(128);not null;index:idx_wecom_departments_tenant_dept,priority:2"`
	ParentID     string         `json:"parent_id" gorm:"type:varchar(128);default:'';index"`
	Name         string         `json:"name" gorm:"type:varchar(255);default:''"`
	Order        int64          `json:"order" gorm:"column:dept_order;default:0"`
	Status       string         `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	SyncedAt     time.Time      `json:"synced_at"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (WeComDepartment) TableName() string { return "wecom_departments" }

type WeComIdentityDepartment struct {
	TenantID     uint64    `json:"tenant_id" gorm:"primaryKey;autoIncrement:false"`
	UserID       string    `json:"userid" gorm:"column:userid;type:varchar(128);primaryKey"`
	DepartmentID string    `json:"department_id" gorm:"type:varchar(128);primaryKey"`
	SyncedAt     time.Time `json:"synced_at"`
	CreatedAt    time.Time `json:"created_at"`
}

func (WeComIdentityDepartment) TableName() string { return "wecom_identity_departments" }

type WeComUserBinding struct {
	ID             uint64         `json:"id" gorm:"primaryKey;autoIncrement"`
	TenantID       uint64         `json:"tenant_id" gorm:"not null;index"`
	WeKnoraUserID  string         `json:"weknora_user_id" gorm:"column:weknora_user_id;type:varchar(36);not null;index"`
	WeComUserID    string         `json:"wecom_userid" gorm:"column:wecom_userid;type:varchar(128);not null;index"`
	Provider       string         `json:"provider" gorm:"type:varchar(32);not null;default:'wecom'"`
	Source         string         `json:"source" gorm:"type:varchar(32);not null;default:'admin'"`
	Status         string         `json:"status" gorm:"type:varchar(32);not null;default:'active';index"`
	LastVerifiedAt *time.Time     `json:"last_verified_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

func (WeComUserBinding) TableName() string { return "wecom_user_bindings" }

type WeComACLSubject struct {
	Bound         bool      `json:"bound"`
	WeKnoraUserID string    `json:"weknora_user_id,omitempty"`
	WeComUserID   string    `json:"wecom_userid,omitempty"`
	Status        string    `json:"status,omitempty"`
	Departments   []string  `json:"departments"`
	Groups        []string  `json:"groups"`
	ResolvedAt    time.Time `json:"resolved_at"`
}

func IsWeComIdentityUsable(status string) bool {
	return status == "" || status == WeComIdentityStatusActive
}
