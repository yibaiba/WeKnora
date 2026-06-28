package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubWeComIdentityService struct {
	interfaces.WeComIdentityService
	upsertBinding func(
		ctx context.Context,
		tenantID uint64,
		input interfaces.WeComBindingInput,
	) (*types.WeComUserBinding, error)
	importBindings func(
		ctx context.Context,
		tenantID uint64,
		rows []interfaces.WeComBindingImportRow,
	) []interfaces.WeComBindingImportResult
}

func (s *stubWeComIdentityService) CreateOrUpdateBinding(
	ctx context.Context,
	tenantID uint64,
	input interfaces.WeComBindingInput,
) (*types.WeComUserBinding, error) {
	return s.upsertBinding(ctx, tenantID, input)
}

func (s *stubWeComIdentityService) ImportBindings(
	ctx context.Context,
	tenantID uint64,
	rows []interfaces.WeComBindingImportRow,
) []interfaces.WeComBindingImportResult {
	return s.importBindings(ctx, tenantID, rows)
}

func TestWeComIdentityHandlerImportsCSVRows(t *testing.T) {
	var capturedRows []interfaces.WeComBindingImportRow
	svc := &stubWeComIdentityService{
		importBindings: func(
			_ context.Context,
			tenantID uint64,
			rows []interfaces.WeComBindingImportRow,
		) []interfaces.WeComBindingImportResult {
			require.Equal(t, uint64(1), tenantID)
			capturedRows = rows
			return []interfaces.WeComBindingImportResult{
				{RowNumber: rows[0].RowNumber, WeComUserID: rows[0].WeComUserID, Success: true},
				{RowNumber: rows[1].RowNumber, WeComUserID: rows[1].WeComUserID, Error: "missing user"},
			}
		},
	}
	router := newWeComIdentityTestRouter(NewWeComIdentityHandler(svc))
	body := "email,wecom_userid\nalice@example.com,wx-a\nmissing@example.com,wx-missing\n"
	req := httptest.NewRequest(http.MethodPost, "/tenants/1/wecom/bindings/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/csv")
	req = withMemberCtx(req, memberCtxOpts{callerID: "admin", tenantID: 1})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, capturedRows, 2)
	require.Equal(t, 2, capturedRows[0].RowNumber)
	require.Equal(t, "alice@example.com", capturedRows[0].Email)
	require.Equal(t, "wx-a", capturedRows[0].WeComUserID)
	require.Contains(t, w.Body.String(), `"failures":1`)
}

func TestWeComIdentityHandlerUpdateUsesPathUserID(t *testing.T) {
	var captured interfaces.WeComBindingInput
	svc := &stubWeComIdentityService{
		upsertBinding: func(
			_ context.Context,
			tenantID uint64,
			input interfaces.WeComBindingInput,
		) (*types.WeComUserBinding, error) {
			require.Equal(t, uint64(1), tenantID)
			captured = input
			return &types.WeComUserBinding{
				TenantID:      tenantID,
				WeKnoraUserID: input.WeKnoraUserID,
				WeComUserID:   input.WeComUserID,
			}, nil
		},
	}
	router := newWeComIdentityTestRouter(NewWeComIdentityHandler(svc))
	w := doWeComIdentityJSON(t, router, http.MethodPut, "/tenants/1/wecom/bindings/u1", map[string]string{
		"wecom_userid": "wx-a",
	})

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, "u1", captured.WeKnoraUserID)
	require.Equal(t, "wx-a", captured.WeComUserID)
}

func newWeComIdentityTestRouter(h *WeComIdentityHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(errorCapture())
	r.POST("/tenants/:id/wecom/bindings", h.CreateBinding)
	r.PUT("/tenants/:id/wecom/bindings/:user_id", h.UpdateBinding)
	r.POST("/tenants/:id/wecom/bindings/import", h.ImportBindings)
	return r
}

func doWeComIdentityJSON(
	t *testing.T,
	r *gin.Engine,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req = withMemberCtx(req, memberCtxOpts{callerID: "admin", tenantID: 1})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
