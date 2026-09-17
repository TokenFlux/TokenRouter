package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	idemhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

// SystemHandler handles system-related operations
// RestartRequester 只请求进程关闭，不授予 handler 直接退出进程的能力。
type RestartRequester = maintenance.RestartRequester
type SystemHandler struct {
	updateSvc  systemUpdateService
	operations *maintenance.Operations
}

type systemUpdateService interface {
	CheckUpdate(ctx context.Context, force bool) (*ops.UpdateInfo, error)
	PerformUpdate(ctx context.Context) error
	Rollback() error
	ListRollbackVersions(ctx context.Context) ([]ops.RollbackVersion, error)
	RollbackToVersion(ctx context.Context, version string) error
}

// NewSystemHandler creates a new SystemHandler
func NewSystemHandler(updateSvc systemUpdateService, lockSvc *maintenance.SystemOperationLockService, restarters ...RestartRequester) *SystemHandler {
	var restarter RestartRequester
	if len(restarters) > 0 {
		restarter = restarters[0]
	}
	return &SystemHandler{
		updateSvc:  updateSvc,
		operations: maintenance.NewOperations(updateSvc, lockSvc, restarter),
	}
}

// GetVersion returns the current version
// GET /api/v1/admin/system/version
func (h *SystemHandler) GetVersion(c *gin.Context) {
	info, _ := h.updateSvc.CheckUpdate(c.Request.Context(), false)
	response.Success(c, gin.H{
		"version": info.CurrentVersion,
	})
}

// CheckUpdates checks for available updates
// GET /api/v1/admin/system/check-updates
func (h *SystemHandler) CheckUpdates(c *gin.Context) {
	force := c.Query("force") == "true"
	info, err := h.updateSvc.CheckUpdate(c.Request.Context(), force)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, info)
}

// PerformUpdate downloads and applies the update
// POST /api/v1/admin/system/update
func (h *SystemHandler) PerformUpdate(c *gin.Context) {
	operationID := buildSystemOperationID(c, "update")
	payload := gin.H{"operation_id": operationID}
	idemhttp.ExecuteAdminIdempotentJSON(c, "admin.system.update", payload, idempotency.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.operations.Update(ctx, operationID)
	})
}

// GetRollbackVersions 返回允许回退的历史版本列表。
// GET /api/v1/admin/system/rollback-versions
func (h *SystemHandler) GetRollbackVersions(c *gin.Context) {
	versions, err := h.updateSvc.ListRollbackVersions(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Success(c, gin.H{
		"versions": versions,
	})
}

// Rollback 恢复历史版本。
// 无请求体或 version 为空时恢复上次原地更新留下的 .backup；传入
// {"version":"x.y.z"} 时下载并安装允许列表中的指定 release。
// POST /api/v1/admin/system/rollback
func (h *SystemHandler) Rollback(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
	}
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			response.Error(c, http.StatusBadRequest, "invalid request body")
			return
		}
	}
	targetVersion := strings.TrimSpace(req.Version)

	operation := "rollback"
	if targetVersion != "" {
		operation = "rollback:" + targetVersion
	}
	operationID := buildSystemOperationID(c, operation)
	payload := gin.H{"operation_id": operationID, "version": targetVersion}
	idemhttp.ExecuteAdminIdempotentJSON(c, "admin.system.rollback", payload, idempotency.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.operations.Rollback(ctx, operationID, targetVersion)
	})
}

// RestartService restarts the systemd service
// POST /api/v1/admin/system/restart
func (h *SystemHandler) RestartService(c *gin.Context) {
	operationID := buildSystemOperationID(c, "restart")
	payload := gin.H{"operation_id": operationID}
	idemhttp.ExecuteAdminIdempotentJSON(c, "admin.system.restart", payload, idempotency.DefaultSystemOperationIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.operations.Restart(ctx, operationID)
	})
}

func buildSystemOperationID(c *gin.Context, operation string) string {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		return "sysop-" + operation + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	actorScope := "admin:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "admin:" + strconv.FormatInt(subject.UserID, 10)
	}
	seed := operation + "|" + actorScope + "|" + c.FullPath() + "|" + key
	hash := idempotency.HashIdempotencyKey(seed)
	if len(hash) > 24 {
		hash = hash[:24]
	}
	return "sysop-" + hash
}

// NewSystemRuntimeHandler 使用 app 已登记生命周期的唯一维护实例。
func NewSystemRuntimeHandler(update systemUpdateService, operations *maintenance.Operations) *SystemHandler {
	return &SystemHandler{updateSvc: update, operations: operations}
}
