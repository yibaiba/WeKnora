package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type sideEffectWeCom struct {
	interfaces.WeComIdentityService
	bound      []interfaces.WeComBindingInput
	tenants    []uint64
	ensuredFor []uint64
	listedIDs  []string
}

func (s *sideEffectWeCom) ResolveSubject(context.Context, uint64, string) (*types.WeComACLSubject, error) {
	return &types.WeComACLSubject{Bound: false, Departments: []string{}}, nil
}

func (s *sideEffectWeCom) CreateOrUpdateBinding(_ context.Context, tenantID uint64, input interfaces.WeComBindingInput) (*types.WeComUserBinding, error) {
	inputCopy := input
	s.bound = append(s.bound, inputCopy)
	return &types.WeComUserBinding{TenantID: tenantID, WeKnoraUserID: input.WeKnoraUserID, WeComUserID: input.WeComUserID, Source: input.Source}, nil
}

func (s *sideEffectWeCom) ListTenantIDsByDepartmentIDs(_ context.Context, departmentIDs []string) ([]uint64, error) {
	s.listedIDs = append([]string(nil), departmentIDs...)
	return s.tenants, nil
}

func (s *sideEffectWeCom) EnsureSSOEmployee(_ context.Context, tenantID uint64, _, _ string, _ []types.SSODepartment) error {
	s.ensuredFor = append(s.ensuredFor, tenantID)
	return nil
}

type sideEffectMembers struct {
	interfaces.TenantMemberService
	added []uint64
}

func (s *sideEffectMembers) AddMember(_ context.Context, _ string, tenantID uint64, role types.TenantRole, _ *string) (*types.TenantMember, error) {
	if role != types.TenantRoleViewer {
		return nil, ErrInvalidTenantRole
	}
	s.added = append(s.added, tenantID)
	return &types.TenantMember{TenantID: tenantID, Role: role}, nil
}

type sideEffectTenants struct {
	interfaces.TenantService
}

func (s *sideEffectTenants) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, Status: "active"}, nil
}

func TestApplySSOAccessSideEffectsBindsAndJoins(t *testing.T) {
	withOIDCSSRFWhitelist(t, "127.0.0.1")
	mux := http.NewServeMux()
	mux.HandleFunc("/QueryEmployeeDirectory", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(types.SSODirectorySnapshot{
			Sub: "emp_1",
			Departments: []types.SSODepartment{
				{WeComDepartmentID: 42, Name: "Sales", IsMain: true},
				{WeComDepartmentID: 7, Name: "Eng", IsMain: false},
			},
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	wecom := &sideEffectWeCom{tenants: []uint64{9}}
	members := &sideEffectMembers{}
	repo := &oidcMemoryUserRepo{}
	user := &types.User{ID: "u1", TenantID: 3, Email: "a@example.com"}
	svc := &userService{
		userRepo:      repo,
		wecomIdentity: wecom,
		memberService: members,
		tenantService: &sideEffectTenants{},
		config:        &config.Config{},
	}
	svc.applySSOAccessSideEffects(context.Background(), &config.OIDCAuthConfig{
		IssuerURL: server.URL,
	}, user, "sso-or-resource-access", "wx-sales")

	if len(wecom.ensuredFor) == 0 || wecom.ensuredFor[0] != 3 {
		t.Fatalf("expected local departments created in home tenant 3, got %v", wecom.ensuredFor)
	}
	if len(wecom.bound) == 0 {
		t.Fatal("expected WeCom bind for home tenant or joined tenant")
	}
	if wecom.bound[0].WeComUserID != "wx-sales" || wecom.bound[0].Source != types.WeComBindingSourceSSO {
		t.Fatalf("binding = %+v", wecom.bound[0])
	}
	if len(members.added) != 1 || members.added[0] != 9 {
		t.Fatalf("auto-joined tenants = %v, want [9]", members.added)
	}
	if len(wecom.listedIDs) != 2 {
		t.Fatalf("expected both departments when joining spaces, got %v", wecom.listedIDs)
	}
}
