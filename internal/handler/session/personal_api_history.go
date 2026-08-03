package session

import (
	"context"
	stderrors "errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ListPersonalAPIHistory returns only sessions created by the current member's
// personal API keys. Web sessions use a different principal and are excluded.
func (h *Handler) ListPersonalAPIHistory(c *gin.Context) {
	ctx, ok := personalAPIHistoryContext(c)
	if !ok {
		return
	}
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	result, err := h.sessionService.ListSessions(ctx, &types.SessionListQuery{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Page:     pagination.GetPage(),
		PageSize: pagination.GetPageSize(),
	})
	if err != nil {
		c.Error(errors.NewInternalServerError("Failed to list personal API sessions").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "data": result.Data, "total": result.Total,
		"page": result.Page, "page_size": result.PageSize,
	})
}

// GetPersonalAPIHistoryMessages is intentionally read-only: no matching
// update, delete, clear, or message-search route is exposed under /me.
func (h *Handler) GetPersonalAPIHistoryMessages(c *gin.Context) {
	ctx, ok := personalAPIHistoryContext(c)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		c.Error(errors.NewBadRequestError("Invalid session ID"))
		return
	}
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	messages, err := h.messageService.GetMessagesBySession(
		ctx, sessionID, pagination.GetPage(), pagination.GetPageSize(),
	)
	if err != nil {
		if stderrors.Is(err, errors.ErrSessionNotFound) {
			c.Error(errors.NewNotFoundError("API session not found"))
			return
		}
		c.Error(errors.NewInternalServerError("Failed to load personal API messages").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true, "data": messages,
		"page": pagination.GetPage(), "page_size": pagination.GetPageSize(),
	})
}

func personalAPIHistoryContext(c *gin.Context) (context.Context, bool) {
	ctx := c.Request.Context()
	tenantID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || tenantID == 0 {
		c.Error(errors.NewBadRequestError("Invalid workspace ID"))
		return nil, false
	}
	activeTenantID, tenantOK := types.TenantIDFromContext(ctx)
	userID, userOK := types.UserIDFromContext(ctx)
	principal, principalOK := types.PrincipalFromContext(ctx)
	if !tenantOK || activeTenantID != tenantID || !userOK || strings.TrimSpace(userID) == "" {
		c.Error(errors.NewForbiddenError("Personal API history is unavailable outside the active workspace"))
		return nil, false
	}
	if !principalOK || principal.Type != types.PrincipalWebUser {
		c.Error(errors.NewForbiddenError("Personal API history requires a signed-in user"))
		return nil, false
	}
	return types.WithPrincipal(ctx, types.APIMemberPrincipal(tenantID, userID)), true
}
