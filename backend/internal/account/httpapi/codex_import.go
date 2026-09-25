// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// CodexImportHandler 只拥有管理 HTTP 和原幂等响应。
type CodexImportHandler struct {
	idempotencyhttp.Executor
	core *account.CodexImporter
}

func NewCodexImportHandler(core *account.CodexImporter) *CodexImportHandler {
	return &CodexImportHandler{core: core}
}
func (h *CodexImportHandler) ImportCodexSession(c *gin.Context) {
	var req account.CodexSessionImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	account.DiscardDeprecatedExtra(req.Extra)
	if req.Concurrency != nil && *req.Concurrency < 0 {
		response.BadRequest(c, "concurrency must be >= 0")
		return
	}
	if req.Priority != nil && *req.Priority < 0 {
		response.BadRequest(c, "priority must be >= 0")
		return
	}
	if req.RateMultiplier != nil && *req.RateMultiplier < 0 {
		response.BadRequest(c, "rate_multiplier must be >= 0")
		return
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		response.BadRequest(c, "load_factor must be <= 10000")
		return
	}

	entries, err := account.ParseCodexSessionImportEntries(req)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if len(entries) == 0 {
		response.BadRequest(c, "请输入 accessToken 或 Codex session JSON")
		return
	}

	h.ExecuteAdminIdempotentJSON(c, "admin.accounts.import_codex_session", req, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.core.Import(ctx, req, entries)
	})
}
