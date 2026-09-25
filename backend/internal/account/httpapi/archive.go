// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// ArchiveHandler 只负责备份 HTTP 输入、幂等和管理员响应；资源查询与导入由账号用例拥有。
type ArchiveHandler struct {
	idempotencyhttp.Executor
	archive *account.Archive
}

func NewArchiveHandler(archive *account.Archive) *ArchiveHandler {
	return &ArchiveHandler{archive: archive}
}
func (h *ArchiveHandler) ExportData(c *gin.Context) {
	ids, err := parseAccountIDs(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	query := account.ArchiveExportQuery{IDs: ids, Platform: c.Query("platform"), Type: c.Query("type"), Status: c.Query("status"), PrivacyMode: strings.TrimSpace(c.Query("privacy_mode")), Search: strings.TrimSpace(c.Query("search")), SortBy: c.DefaultQuery("sort_by", "name"), SortOrder: c.DefaultQuery("sort_order", "asc"), IncludeProxies: func() (bool, error) { return parseIncludeProxies(c) }}
	if len(query.Search) > 100 {
		query.Search = query.Search[:100]
	}
	if len(ids) == 0 {
		if group := c.Query("group"); group != "" {
			if group == "ungrouped" {
				query.GroupID = account.AccountListGroupUngrouped
			} else {
				value, parseErr := strconv.ParseInt(group, 10, 64)
				if parseErr != nil || value <= 0 {
					response.ErrorFrom(c, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter"))
					return
				}
				query.GroupID = value
			}
		}
	}
	payload, err := h.archive.Export(c.Request.Context(), query)
	if err != nil {
		var inputErr *account.ArchiveInputError
		if errors.As(err, &inputErr) {
			response.BadRequest(c, inputErr.Error())
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, payload)
}

func (h *ArchiveHandler) ImportData(c *gin.Context) {
	var req transfer.DataImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := transfer.ValidateHeader(req.Data); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	// 废弃账号字段不参与导入幂等指纹，旧客户端的值或非法类型都与缺省输入等价。
	for index := range req.Data.Accounts {
		account.DiscardDeprecatedExtra(req.Data.Accounts[index].Extra)
	}

	h.ExecuteAdminIdempotentJSON(c, "admin.accounts.import_data", req, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.archive.Import(ctx, req)
	})
}
func parseAccountIDs(c *gin.Context) ([]int64, error) {
	values := c.QueryArray("ids")
	if len(values) == 0 {
		raw := strings.TrimSpace(c.Query("ids"))
		if raw != "" {
			values = []string{raw}
		}
	}
	if len(values) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(values))
	for _, item := range values {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("invalid account id: %s", part)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
func parseIncludeProxies(c *gin.Context) (bool, error) {
	raw := strings.TrimSpace(strings.ToLower(c.Query("include_proxies")))
	if raw == "" {
		return true, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return true, fmt.Errorf("invalid include_proxies value: %s", raw)
	}
}
