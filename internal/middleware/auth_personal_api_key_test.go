package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type personalKeyAuthUserService struct {
	interfaces.UserService
	user *types.User
}

func (s *personalKeyAuthUserService) GetUserByID(context.Context, string) (*types.User, error) {
	return s.user, nil
}

type personalKeyAuthMemberService struct {
	interfaces.TenantMemberService
	membership *types.TenantMember
}

func (s *personalKeyAuthMemberService) GetMembership(
	context.Context, string, uint64,
) (*types.TenantMember, error) {
	return s.membership, nil
}

func TestAttachPersonalAPIKeyUsesStableMemberPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ownerID := "member-1"
	userService := &personalKeyAuthUserService{user: &types.User{ID: ownerID, IsActive: true}}
	memberService := &personalKeyAuthMemberService{membership: &types.TenantMember{
		UserID: ownerID, TenantID: 42, Role: types.TenantRoleViewer, Status: types.TenantMemberStatusActive,
	}}
	for _, keyID := range []uint64{11, 12} {
		ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
		ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		attachAPIKeyAuthContext(ginContext, &fakeTenantService{tenant: &types.Tenant{ID: 42}}, userService, memberService, 42, &types.TenantAPIKey{
			ID: keyID, TenantID: uint64Pointer(42), OwnerUserID: &ownerID,
			FullAccess:       true,
			KnowledgeBaseIDs: types.StringArray{"kb-1"},
			Capabilities:     types.StringArray{string(types.APIKeyCapabilityManageModels)},
		})
		if ginContext.IsAborted() {
			t.Fatalf("key %d was unexpectedly rejected", keyID)
		}
		principal, ok := types.PrincipalFromContext(ginContext.Request.Context())
		if !ok || principal.Type != types.PrincipalAPIMember || principal.ID != "42:member-1" {
			t.Fatalf("key %d principal = %#v, ok=%v", keyID, principal, ok)
		}
		if owner := types.SessionOwnerIDFromContext(ginContext.Request.Context()); owner != "api_member:42:member-1" {
			t.Fatalf("key %d session owner = %q", keyID, owner)
		}
		scope, ok := types.TenantAPIKeyScopeFromContext(ginContext.Request.Context())
		if !ok || scope.OwnerUserID != ownerID || scope.FullAccess {
			t.Fatalf("key %d scope = %#v, ok=%v", keyID, scope, ok)
		}
		if !scope.HasCapability(types.APIKeyCapabilityRetrieve) ||
			!scope.HasCapability(types.APIKeyCapabilityChat) ||
			scope.HasCapability(types.APIKeyCapabilityManageModels) {
			t.Fatalf("key %d capabilities were not constrained: %#v", keyID, scope.Capabilities)
		}
	}
}

func TestAttachPersonalAPIKeyRejectsInactiveOwnerOrMembership(t *testing.T) {
	tests := []struct {
		name       string
		userActive bool
		membership *types.TenantMember
	}{
		{name: "inactive user", membership: activePersonalKeyMembership()},
		{name: "missing membership", userActive: true},
		{name: "suspended membership", userActive: true, membership: &types.TenantMember{Status: types.TenantMemberStatusSuspended}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ginContext, recorder := gin.CreateTestContext(httptest.NewRecorder())
			ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			ownerID := "member-1"
			attachAPIKeyAuthContext(
				ginContext,
				&fakeTenantService{tenant: &types.Tenant{ID: 42}},
				&personalKeyAuthUserService{user: &types.User{ID: ownerID, IsActive: test.userActive}},
				&personalKeyAuthMemberService{membership: test.membership},
				42,
				&types.TenantAPIKey{
					ID: 11, TenantID: uint64Pointer(42), OwnerUserID: &ownerID,
					KnowledgeBaseIDs: types.StringArray{"kb-1"},
				},
			)
			if !ginContext.IsAborted() || recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 rejection, aborted=%v status=%d", ginContext.IsAborted(), recorder.Code)
			}
		})
	}
}

func activePersonalKeyMembership() *types.TenantMember {
	return &types.TenantMember{Status: types.TenantMemberStatusActive}
}

func uint64Pointer(value uint64) *uint64 {
	return &value
}
