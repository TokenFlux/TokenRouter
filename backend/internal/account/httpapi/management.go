// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"
	"errors"
	"strconv"

	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// AccountManagement 只表达 HTTP 使用的账号用例，不暴露具体存储。
type AccountManagement interface {
	BulkUpdateAccounts(context.Context, *accountcore.BulkUpdateAccountsInput) (*accountcore.BulkUpdateAccountsResult, error)
	SetAccountSchedulable(context.Context, int64, bool) (*accountcore.Record, error)
	ResetAccountQuota(context.Context, int64) error
	RevertAccountProxyFallback(context.Context, int64) error
	GetAccountsByIDs(context.Context, []int64) ([]*accountcore.Record, error)
	CreateAccount(context.Context, *accountcore.CreateAccountInput) (*accountcore.Record, error)
	UpdateAccount(context.Context, int64, *accountcore.UpdateAccountInput) (*accountcore.Record, error)
	DuplicateAccount(context.Context, int64, string, string) (*accountcore.Record, error)
	RecoverDuplicateAccount(context.Context, int64, string, string) (*accountcore.Record, error)
	GetAccount(context.Context, int64) (*accountcore.Record, error)
	DeleteAccount(context.Context, int64) error
	CheckMixedChannelRisk(context.Context, int64, string, []int64) error
}
type AccountRuntimePresenter interface {
	Present(context.Context, *accountcore.Record) AccountWithConcurrency
}
type AccountRuntimePresenterFunc func(context.Context, *accountcore.Record) AccountWithConcurrency

func (f AccountRuntimePresenterFunc) Present(ctx context.Context, v *accountcore.Record) AccountWithConcurrency {
	return f(ctx, v)
}

// ManagementHandler 保留原管理员 HTTP 契约，运行投影与 Ollama 用量通过已装配端口取得。
type ManagementHandler struct {
	idempotencyhttp.Executor

	models           *accountcore.ModelSyncService
	reports          AccountReportOptions
	tier             *accountcore.TierManagement
	catalog          *routing.AdminCatalog
	modelDefaults    accountcore.ModelMappingDefaults
	listing          *accountcore.ManagementList
	runtimePresenter *RuntimePresenter
	recovery         *accountcore.RecoveryService
	batch            *accountcore.ManagementBatch
	managed          *accountcore.ManagedRefreshService
	adminService     AccountManagement
	presenter        AccountRuntimePresenter
	ollamaCloudUsage *accountcore.OllamaCloudUsageService
	privacy          AccountCreationPrivacy
	afterCreate      func(*accountcore.Record)
}

type AccountCreationPrivacy interface {
	ForceAntigravityPrivacy(context.Context, *accountcore.Record) string
	ForceOpenAIPrivacy(context.Context, *accountcore.Record) string
}
type ManagementOptions struct {
	Models           *accountcore.ModelSyncService
	Reports          AccountReportOptions
	Tier             *accountcore.TierManagement
	Catalog          *routing.AdminCatalog
	ModelDefaults    accountcore.ModelMappingDefaults
	List             *accountcore.ManagementList
	RuntimePresenter *RuntimePresenter
	Recovery         *accountcore.RecoveryService
	Batch            *accountcore.ManagementBatch
	Managed          *accountcore.ManagedRefreshService
	Presenter        AccountRuntimePresenter
	Ollama           *accountcore.OllamaCloudUsageService
	Privacy          AccountCreationPrivacy
	AfterCreate      func(*accountcore.Record)
}

func NewManagementHandler(admin AccountManagement, options ManagementOptions) *ManagementHandler {
	return &ManagementHandler{models: options.Models, reports: options.Reports, tier: options.Tier, catalog: options.Catalog, modelDefaults: options.ModelDefaults, listing: options.List, runtimePresenter: options.RuntimePresenter, recovery: options.Recovery, batch: options.Batch, managed: options.Managed, adminService: admin, presenter: options.Presenter, ollamaCloudUsage: options.Ollama, privacy: options.Privacy, afterCreate: options.AfterCreate}
}

// GetByID 按原状态码与展示流程查询账号。
// GET /api/v1/admin/accounts/:id
func (h *ManagementHandler) GetByID(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	account, err := h.adminService.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.ollamaCloudUsage != nil {
		if err := h.ollamaCloudUsage.ResolveAccounts(c.Request.Context(), []*accountcore.Record{account}); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	response.Success(c, h.presenter.Present(c.Request.Context(), account))
}

// Delete 删除账号并保持原确认响应。
// DELETE /api/v1/admin/accounts/:id
func (h *ManagementHandler) Delete(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	err = h.adminService.DeleteAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Account deleted successfully"})
}

// CheckMixedChannel 返回账号绑组的混合渠道风险。
// POST /api/v1/admin/accounts/check-mixed-channel
func (h *ManagementHandler) CheckMixedChannel(c *gin.Context) {
	var req CheckMixedChannelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if len(req.GroupIDs) == 0 {
		response.Success(c, gin.H{"has_risk": false})
		return
	}

	accountID := int64(0)
	if req.AccountID != nil {
		accountID = *req.AccountID
	}

	err := h.adminService.CheckMixedChannelRisk(c.Request.Context(), accountID, req.Platform, req.GroupIDs)
	if err != nil {
		var mixedErr *accountcore.MixedChannelError
		if errors.As(err, &mixedErr) {
			response.Success(c, gin.H{
				"has_risk": true,
				"error":    "mixed_channel_warning",
				"message":  mixedErr.Error(),
				"details": gin.H{
					"group_id":         mixedErr.GroupID,
					"group_name":       mixedErr.GroupName,
					"current_platform": mixedErr.CurrentPlatform,
					"other_platform":   mixedErr.OtherPlatform,
				},
			})
			return
		}

		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"has_risk": false})
}
