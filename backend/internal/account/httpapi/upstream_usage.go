// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	context "context"
	strconv "strconv"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// UpstreamUsageQueries 是只读管理查询端口；HTTP 不接触账号仓储或供应商客户端。
type UpstreamUsageQueries interface {
	QueryAccount(context.Context, int64) (*account.UpstreamUsageQueryResult, error)
	QueryBatch(context.Context, []int64) (map[int64]*account.UpstreamUsageQueryResult, map[int64]error, error)
}
type UpstreamUsageHandler struct{ queries UpstreamUsageQueries }

func NewUpstreamUsageHandler(queries UpstreamUsageQueries) *UpstreamUsageHandler {
	return &UpstreamUsageHandler{queries: queries}
}

// UpstreamUsageBatchRequest 是 API Key 上游用量批量查询请求。
type UpstreamUsageBatchRequest struct {
	AccountIDs []int64 `json:"account_ids" binding:"required"`
}

// QueryUpstreamUsage 查询 API Key 账号的实时上游用量。
// POST /api/v1/admin/accounts/:id/upstream-usage/query
func (h *UpstreamUsageHandler) QueryUpstreamUsage(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.ErrorFrom(c, account.ErrUpstreamUsageAccountInvalid)
		return
	}
	if h.queries == nil {
		response.ErrorFrom(c, account.ErrUpstreamUsageUnavailable)
		return
	}
	result, err := h.queries.QueryAccount(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// QueryBatchUpstreamUsage 批量查询 API Key 账号的实时上游用量。
// POST /api/v1/admin/accounts/upstream-usage/query/batch
func (h *UpstreamUsageHandler) QueryBatchUpstreamUsage(c *gin.Context) {
	var req UpstreamUsageBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.ErrorFrom(c, account.ErrUpstreamUsageBatchInvalid)
		return
	}
	if len(req.AccountIDs) == 0 {
		response.ErrorFrom(c, account.ErrUpstreamUsageBatchInvalid)
		return
	}
	if len(req.AccountIDs) > 100 {
		response.ErrorFrom(c, account.ErrUpstreamUsageBatchTooLarge)
		return
	}
	for _, accountID := range req.AccountIDs {
		if accountID <= 0 {
			response.ErrorFrom(c, account.ErrUpstreamUsageBatchInvalid)
			return
		}
	}
	if h.queries == nil {
		response.ErrorFrom(c, account.ErrUpstreamUsageUnavailable)
		return
	}
	usageByAccount, errorsByAccount, err := h.queries.QueryBatch(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	serializedErrors := make(map[string]gin.H, len(errorsByAccount))
	for accountID, queryErr := range errorsByAccount {
		status := infraerrors.FromError(queryErr)
		serializedErrors[strconv.FormatInt(accountID, 10)] = gin.H{
			"code":    status.Reason,
			"reason":  status.Reason,
			"message": status.Message,
			"status":  status.Code,
		}
	}
	response.Success(c, gin.H{
		"usage":  usageByAccount,
		"errors": serializedErrors,
	})
}
