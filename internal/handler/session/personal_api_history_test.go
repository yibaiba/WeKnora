package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type personalHistorySessionService struct {
	interfaces.SessionService
	ownerID string
	query   *types.SessionListQuery
}

func (s *personalHistorySessionService) ListSessions(
	ctx context.Context, query *types.SessionListQuery,
) (*types.PageResult, error) {
	s.ownerID = types.SessionOwnerIDFromContext(ctx)
	s.query = query
	pagination := &types.Pagination{Page: query.Page, PageSize: query.PageSize}
	return types.NewPageResult(1, pagination, []types.SessionListItem{{
		Session: types.Session{ID: "api-session", UserID: s.ownerID},
	}}), nil
}

type personalHistoryMessageService struct {
	interfaces.MessageService
	ownerID   string
	sessionID string
	role      types.TenantRole
}

func (s *personalHistoryMessageService) GetMessagesBySession(
	ctx context.Context, sessionID string, _, _ int,
) ([]*types.Message, error) {
	s.ownerID = types.SessionOwnerIDFromContext(ctx)
	s.sessionID = sessionID
	s.role = types.TenantRoleFromContext(ctx)
	return []*types.Message{{ID: "message-1", SessionID: sessionID}}, nil
}

func TestPersonalAPIHistoryUsesAPIMemberScope(t *testing.T) {
	sessions := &personalHistorySessionService{}
	messages := &personalHistoryMessageService{}
	router := personalHistoryTestRouter(&Handler{sessionService: sessions, messageService: messages})

	list := performPersonalHistoryRequest(router, http.MethodGet, "/tenants/42/me/api-sessions?keyword=report")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", list.Code, list.Body.String())
	}
	if sessions.ownerID != "api_member:42:member-1" || sessions.query.Keyword != "report" {
		t.Fatalf("list scope/query = %q %#v", sessions.ownerID, sessions.query)
	}

	load := performPersonalHistoryRequest(router, http.MethodGet, "/tenants/42/me/api-sessions/api-session/messages")
	if load.Code != http.StatusOK {
		t.Fatalf("messages status = %d: %s", load.Code, load.Body.String())
	}
	if messages.ownerID != "api_member:42:member-1" || messages.sessionID != "api-session" || messages.role != types.TenantRoleViewer {
		t.Fatalf("message scope/session/role = %q %q %q", messages.ownerID, messages.sessionID, messages.role)
	}
}

func TestPersonalAPIHistoryRejectsMachinePrincipal(t *testing.T) {
	router := personalHistoryTestRouter(&Handler{
		sessionService: &personalHistorySessionService{}, messageService: &personalHistoryMessageService{},
	})
	request := httptest.NewRequest(http.MethodGet, "/tenants/42/me/api-sessions", nil)
	ctx := context.WithValue(request.Context(), types.TenantIDContextKey, uint64(42))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "member-1")
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	ctx = types.WithPrincipal(ctx, types.APIMemberPrincipal(42, "member-1"))
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func personalHistoryTestRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/tenants/:id/me/api-sessions", handler.ListPersonalAPIHistory)
	router.GET("/tenants/:id/me/api-sessions/:session_id/messages", handler.GetPersonalAPIHistoryMessages)
	return router
}

func performPersonalHistoryRequest(router http.Handler, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(request.Context(), types.TenantIDContextKey, uint64(42))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "member-1")
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "member-1"})
	request = request.WithContext(ctx)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
