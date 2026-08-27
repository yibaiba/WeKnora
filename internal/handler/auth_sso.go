package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler/dto"
	"github.com/Tencent/WeKnora/internal/logger"
)

// ExchangeSSOAccess godoc
// @Summary      用企业 SSO 资源票换 WeKnora JWT
// @Description  Hermes 持 aud=weknora 的 SSO Access Token 调用本接口，换回 WeKnora 自己签发的 JWT。不是网页 OIDC 回调，也不接受 aud=sso 登录票。
// @Tags         认证
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string  true  "Bearer {aud=weknora SSO JWT}"
// @Success      200            {object}  dto.AuthLoginResponse
// @Failure      401            {object}  errors.AppError
// @Failure      403            {object}  errors.AppError
// @Router       /auth/sso/exchange [post]
func (h *AuthHandler) ExchangeSSOAccess(c *gin.Context) {
	ctx := c.Request.Context()
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		appErr := errors.NewUnauthorizedError("Authorization header is required")
		c.Error(appErr)
		return
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		appErr := errors.NewUnauthorizedError("Invalid Authorization header format")
		c.Error(appErr)
		return
	}

	resp, err := h.userService.ExchangeSSOAccess(ctx, strings.TrimSpace(parts[1]), h.resolveDefaultTenantMode(ctx))
	if err != nil {
		logger.Errorf(ctx, "SSO access exchange failed")
		if stderrors.Is(err, service.ErrOIDCDisabled) {
			appErr := errors.NewForbiddenError("OIDC is not enabled")
			c.Error(appErr)
			return
		}
		appErr := errors.NewUnauthorizedError("SSO token exchange failed")
		c.Error(appErr)
		return
	}
	if !resp.Success {
		c.JSON(http.StatusUnauthorized, dto.NewAuthLoginResponse(resp))
		return
	}
	logger.Infof(ctx, "SSO access exchanged for WeKnora session, user_id: %s", resp.User.ID)
	c.JSON(http.StatusOK, dto.NewAuthLoginResponse(resp))
}
