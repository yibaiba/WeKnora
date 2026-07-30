package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRegisterTenantRoutesIncludesWeComIdentityEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	RegisterTenantRoutes(
		v1,
		&handler.TenantHandler{},
		nil,
		nil,
		&handler.WeComIdentityHandler{},
		nil,
		&rbacGuards{},
	)

	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}

	want := []string{
		http.MethodGet + " /api/v1/tenants/:id/wecom/bindings",
		http.MethodPost + " /api/v1/tenants/:id/wecom/bindings",
		http.MethodPut + " /api/v1/tenants/:id/wecom/bindings/:user_id",
		http.MethodDelete + " /api/v1/tenants/:id/wecom/bindings/:user_id",
		http.MethodPost + " /api/v1/tenants/:id/wecom/bindings/import",
		http.MethodPost + " /api/v1/tenants/:id/wecom/sync",
		http.MethodGet + " /api/v1/tenants/:id/wecom/subjects/:user_id",
	}
	for _, route := range want {
		assert.Contains(t, routes, route)
	}
}
