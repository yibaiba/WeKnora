package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type memoryWeComRepo struct {
	identities  map[string]*types.WeComIdentity
	departments map[string]*types.WeComDepartment
	memberships map[string][]string
	bindings    map[string]*types.WeComUserBinding
}

func newMemoryWeComRepo() *memoryWeComRepo {
	return &memoryWeComRepo{
		identities:  map[string]*types.WeComIdentity{},
		departments: map[string]*types.WeComDepartment{},
		memberships: map[string][]string{},
		bindings:    map[string]*types.WeComUserBinding{},
	}
}

func (r *memoryWeComRepo) UpsertIdentities(_ context.Context, identities []*types.WeComIdentity) error {
	for _, identity := range identities {
		cp := *identity
		r.identities[identity.UserID] = &cp
	}
	return nil
}

func (r *memoryWeComRepo) UpsertDepartments(_ context.Context, departments []*types.WeComDepartment) error {
	for _, department := range departments {
		cp := *department
		r.departments[department.DepartmentID] = &cp
	}
	return nil
}

func (r *memoryWeComRepo) ReplaceIdentityDepartments(
	_ context.Context, _ uint64, userID string, departmentIDs []string, _ time.Time,
) error {
	r.memberships[userID] = append([]string(nil), departmentIDs...)
	return nil
}

func (r *memoryWeComRepo) FindIdentity(_ context.Context, _ uint64, userID string) (*types.WeComIdentity, error) {
	identity, ok := r.identities[userID]
	if !ok {
		return nil, apprepo.ErrWeComIdentityNotFound
	}
	return identity, nil
}

func (r *memoryWeComRepo) UpsertBinding(
	_ context.Context, binding *types.WeComUserBinding,
) (*types.WeComUserBinding, error) {
	cp := *binding
	r.bindings[binding.WeKnoraUserID] = &cp
	return &cp, nil
}

func (r *memoryWeComRepo) DeleteBinding(_ context.Context, _ uint64, weknoraUserID string) error {
	delete(r.bindings, weknoraUserID)
	return nil
}

func (r *memoryWeComRepo) ListBindings(
	context.Context, uint64, interfaces.WeComBindingListQuery,
) ([]*types.WeComUserBinding, int64, error) {
	out := make([]*types.WeComUserBinding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		out = append(out, binding)
	}
	return out, int64(len(out)), nil
}

func (r *memoryWeComRepo) ResolveSubject(_ context.Context, _ uint64, weknoraUserID string) (*types.WeComACLSubject, error) {
	binding := r.bindings[weknoraUserID]
	if binding == nil {
		return &types.WeComACLSubject{Departments: []string{}, Groups: []string{}}, nil
	}
	return &types.WeComACLSubject{
		Bound:       true,
		WeComUserID: binding.WeComUserID,
		Departments: r.memberships[binding.WeComUserID],
		Groups:      []string{},
	}, nil
}

type memoryUserRepo struct {
	interfaces.UserRepository
	byID    map[string]*types.User
	byEmail map[string]*types.User
}

func (r *memoryUserRepo) GetUserByID(_ context.Context, id string) (*types.User, error) {
	user, ok := r.byID[id]
	if !ok {
		return nil, apprepo.ErrUserNotFound
	}
	return user, nil
}

func (r *memoryUserRepo) GetUserByEmail(_ context.Context, email string) (*types.User, error) {
	user, ok := r.byEmail[email]
	if !ok {
		return nil, apprepo.ErrUserNotFound
	}
	return user, nil
}

func TestWeComIdentityServiceSyncAndImport(t *testing.T) {
	repo := newMemoryWeComRepo()
	userRepo := &memoryUserRepo{
		byID:    map[string]*types.User{"u1": {ID: "u1", Email: "a@example.com"}},
		byEmail: map[string]*types.User{"a@example.com": {ID: "u1", Email: "a@example.com"}},
	}
	server := newWeComContactTestServer(t)
	defer server.Close()

	svc := newWeComIdentityService(repo, userRepo, nil, func(input interfaces.WeComIdentitySyncInput) *wecomContactClient {
		return newWeComContactClient(input.CorpID, input.Secret, withWeComContactBaseURL(server.URL))
	})

	result, err := svc.Sync(context.Background(), 1, interfaces.WeComIdentitySyncInput{
		CorpID:     "ww123",
		Secret:     "secret",
		FetchChild: true,
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.Departments)
	require.Equal(t, 3, result.Users)
	require.Equal(t, []string{"1", "2"}, repo.memberships["wx-a"])
	require.Equal(t, types.WeComIdentityStatusSuspended, repo.identities["wx-b"].Status)
	require.Equal(t, types.WeComIdentityStatusDeleted, repo.identities["wx-c"].Status)

	importResults := svc.ImportBindings(context.Background(), 1, []interfaces.WeComBindingImportRow{
		{RowNumber: 1, Email: "a@example.com", WeComUserID: "wx-a"},
		{RowNumber: 2, WeKnoraUserID: "u1", WeComUserID: "missing"},
		{RowNumber: 3, Email: "missing@example.com", WeComUserID: "wx-a"},
	})
	require.Len(t, importResults, 3)
	require.True(t, importResults[0].Success)
	require.False(t, importResults[1].Success)
	require.Contains(t, importResults[1].Error, ErrWeComIdentityUnknown.Error())
	require.False(t, importResults[2].Success)
	require.Contains(t, importResults[2].Error, apprepo.ErrUserNotFound.Error())

	subject, err := svc.ResolveSubject(context.Background(), 1, "u1")
	require.NoError(t, err)
	require.True(t, subject.Bound)
	require.Equal(t, "wx-a", subject.WeComUserID)
	require.Equal(t, []string{"1", "2"}, subject.Departments)
}

func TestWeComIdentityServiceRejectsInactiveIdentity(t *testing.T) {
	repo := newMemoryWeComRepo()
	repo.identities["wx-suspended"] = &types.WeComIdentity{
		UserID: "wx-suspended",
		Status: types.WeComIdentityStatusSuspended,
	}
	userRepo := &memoryUserRepo{byID: map[string]*types.User{"u1": {ID: "u1"}}, byEmail: map[string]*types.User{}}
	svc := newWeComIdentityService(repo, userRepo, nil, nil)

	_, err := svc.CreateOrUpdateBinding(context.Background(), 1, interfaces.WeComBindingInput{
		WeKnoraUserID: "u1",
		WeComUserID:   "wx-suspended",
	})
	require.ErrorIs(t, err, ErrWeComIdentityInactive)
}

func TestWeComContactClientRedactsSecretAndToken(t *testing.T) {
	err := errors.New(`Get "https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=ww&corpsecret=top-secret&access_token=token-secret": boom`)
	got := sanitizeWeComHTTPError(err)
	require.NotContains(t, got, "top-secret")
	require.NotContains(t, got, "token-secret")
	require.Contains(t, got, "corpsecret=REDACTED")
	require.Contains(t, got, "access_token=REDACTED")
}

func newWeComContactTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gettoken":
			if r.URL.Query().Get("corpsecret") != "secret" {
				t.Fatalf("unexpected token query: %s", r.URL.RawQuery)
			}
			writeWeComJSON(t, w, map[string]any{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case "/department/list":
			require.Equal(t, "token-1", r.URL.Query().Get("access_token"))
			writeWeComJSON(t, w, map[string]any{
				"errcode": 0,
				"department": []map[string]any{
					{"id": 1, "name": "Root", "parentid": 0, "order": 1},
					{"id": 2, "name": "Engineering", "parentid": 1, "order": 2},
				},
			})
		case "/user/list":
			require.Equal(t, "1", r.URL.Query().Get("department_id"))
			require.Equal(t, "1", r.URL.Query().Get("fetch_child"))
			writeWeComJSON(t, w, map[string]any{
				"errcode": 0,
				"userlist": []map[string]any{
					{"userid": "wx-a", "name": "Alice", "department": []int{1, 2}, "email": "a@example.com", "status": 1},
					{"userid": "wx-b", "name": "Bob", "department": []int{2}, "email": "b@example.com", "status": 2},
					{"userid": "wx-c", "name": "Chris", "department": []int{2}, "email": "c@example.com", "status": 4},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func writeWeComJSON(t *testing.T, w http.ResponseWriter, body map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(body))
}

func TestWeComAPIErrorDoesNotIncludePayload(t *testing.T) {
	err := wecomAPIError{Endpoint: "/user/list", ErrCode: 60011, ErrMsg: "no permission"}
	require.Equal(t, "wecom api error: endpoint=/user/list errcode=60011 errmsg=no permission", err.Error())
	require.False(t, strings.Contains(err.Error(), "corpsecret"))
}
