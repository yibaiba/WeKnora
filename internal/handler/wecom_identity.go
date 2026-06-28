package handler

import (
	"encoding/csv"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type WeComIdentityHandler struct {
	service interfaces.WeComIdentityService
}

func NewWeComIdentityHandler(service interfaces.WeComIdentityService) *WeComIdentityHandler {
	return &WeComIdentityHandler{service: service}
}

type wecomSyncRequest struct {
	CorpID        string   `json:"corp_id" binding:"required"`
	Secret        string   `json:"secret" binding:"required"`
	DepartmentIDs []string `json:"department_ids,omitempty"`
	FetchChild    bool     `json:"fetch_child"`
}

type wecomBindingRequest struct {
	WeKnoraUserID string `json:"weknora_user_id"`
	WeComUserID   string `json:"wecom_userid" binding:"required"`
}

type wecomBindingImportRequest struct {
	Rows []interfaces.WeComBindingImportRow `json:"rows" binding:"required"`
}

func (h *WeComIdentityHandler) Sync(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	var req wecomSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	result, err := h.service.Sync(ctx, tenantID, interfaces.WeComIdentitySyncInput{
		CorpID:        req.CorpID,
		Secret:        req.Secret,
		DepartmentIDs: req.DepartmentIDs,
		FetchChild:    req.FetchChild,
	})
	if err != nil {
		c.Error(apperrors.NewBadRequestError("failed to sync wecom identities").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *WeComIdentityHandler) ListBindings(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}
	bindings, total, err := h.service.ListBindings(ctx, tenantID, interfaces.WeComBindingListQuery{
		Search: strings.TrimSpace(c.Query("q")),
		Offset: (page - 1) * pageSize,
		Limit:  pageSize,
	})
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to list wecom bindings").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"bindings":  bindings,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

func (h *WeComIdentityHandler) CreateBinding(c *gin.Context) {
	h.upsertBinding(c, "")
}

func (h *WeComIdentityHandler) UpdateBinding(c *gin.Context) {
	h.upsertBinding(c, strings.TrimSpace(c.Param("user_id")))
}

func (h *WeComIdentityHandler) upsertBinding(c *gin.Context, pathUserID string) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	var req wecomBindingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	if pathUserID != "" {
		req.WeKnoraUserID = pathUserID
	}
	binding, err := h.service.CreateOrUpdateBinding(ctx, tenantID, interfaces.WeComBindingInput{
		WeKnoraUserID: req.WeKnoraUserID,
		WeComUserID:   req.WeComUserID,
	})
	if err != nil {
		h.writeBindingError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": binding})
}

func (h *WeComIdentityHandler) DeleteBinding(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	userID := strings.TrimSpace(c.Param("user_id"))
	if userID == "" {
		c.Error(apperrors.NewValidationError("user_id is required"))
		return
	}
	err := h.service.DeleteBinding(ctx, tenantID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.Error(apperrors.NewNotFoundError("wecom binding not found"))
			return
		}
		c.Error(apperrors.NewInternalServerError("failed to delete wecom binding").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *WeComIdentityHandler) ImportBindings(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	rows, err := readWeComBindingImportRows(c)
	if err != nil {
		c.Error(apperrors.NewValidationError("invalid request body").WithDetails(err.Error()))
		return
	}
	results := h.service.ImportBindings(ctx, tenantID, rows)
	successes := 0
	for _, result := range results {
		if result.Success {
			successes++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": successes == len(results),
		"data": gin.H{
			"results":   results,
			"successes": successes,
			"failures":  len(results) - successes,
		},
	})
}

func (h *WeComIdentityHandler) ResolveSubject(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID, ok := parseTenantIDFromPath(c)
	if !ok {
		return
	}
	userID := strings.TrimSpace(c.Param("user_id"))
	if userID == "" {
		c.Error(apperrors.NewValidationError("user_id is required"))
		return
	}
	subject, err := h.service.ResolveSubject(ctx, tenantID, userID)
	if err != nil {
		c.Error(apperrors.NewInternalServerError("failed to resolve wecom subject").WithDetails(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": subject})
}

func readWeComBindingImportRows(c *gin.Context) ([]interfaces.WeComBindingImportRow, error) {
	contentType := c.GetHeader("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "text/csv" || mediaType == "application/csv" {
		return parseWeComBindingCSV(c.Request.Body)
	}
	var req wecomBindingImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, err
	}
	return req.Rows, nil
}

func parseWeComBindingCSV(r io.Reader) ([]interfaces.WeComBindingImportRow, error) {
	reader := csv.NewReader(r)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return []interfaces.WeComBindingImportRow{}, nil
	}
	headers := indexedWeComBindingCSVHeaders(records[0])
	rows := make([]interfaces.WeComBindingImportRow, 0, len(records)-1)
	for i, record := range records[1:] {
		rows = append(rows, interfaces.WeComBindingImportRow{
			RowNumber:     i + 2,
			WeKnoraUserID: csvValue(record, headers, "weknora_user_id", "user_id"),
			Email:         csvValue(record, headers, "email"),
			WeComUserID:   csvValue(record, headers, "wecom_userid", "userid"),
		})
	}
	return rows, nil
}

func indexedWeComBindingCSVHeaders(headers []string) map[string]int {
	out := make(map[string]int, len(headers))
	for i, header := range headers {
		out[strings.ToLower(strings.TrimSpace(header))] = i
	}
	return out
}

func csvValue(record []string, headers map[string]int, names ...string) string {
	for _, name := range names {
		index, ok := headers[name]
		if ok && index >= 0 && index < len(record) {
			return strings.TrimSpace(record[index])
		}
	}
	return ""
}

func (h *WeComIdentityHandler) writeBindingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrWeComBindingInvalid):
		c.Error(apperrors.NewValidationError(err.Error()))
	case errors.Is(err, service.ErrWeComIdentityUnknown), errors.Is(err, apprepo.ErrUserNotFound):
		c.Error(apperrors.NewNotFoundError(err.Error()))
	case errors.Is(err, service.ErrWeComIdentityInactive):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	case errors.Is(err, service.ErrWeComBindingAlreadyExists):
		c.Error(apperrors.NewConflictError(err.Error()))
	default:
		c.Error(apperrors.NewInternalServerError("failed to save wecom binding").WithDetails(err.Error()))
	}
}
