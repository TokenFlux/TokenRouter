// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	fmt "fmt"
	strconv "strconv"
	strings "strings"

	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

func (h *ProxyHandler) ExportData(c *gin.Context) {
	ids, err := parseProxyIDs(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 100 {
		search = search[:100]
	}
	proxies, exported, err := h.transfer.Export(c.Request.Context(), egress.ProxyExportQuery{IDs: ids, Protocol: c.Query("protocol"), Status: c.Query("status"), Search: search, SortBy: c.DefaultQuery("sort_by", "id"), SortOrder: c.DefaultQuery("sort_order", "desc")})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, transfer.DataPayload{ExportedAt: exported, Proxies: proxies, Accounts: []transfer.DataAccount{}})
}
func (h *ProxyHandler) ImportData(c *gin.Context) {
	var req struct {
		Data transfer.DataPayload `json:"data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := transfer.ValidateHeader(req.Data); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.transfer.Import(c.Request.Context(), req.Data.Proxies)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, transfer.DataImportResult{ProxyCreated: result.ProxyCreated, ProxyReused: result.ProxyReused, ProxyFailed: result.ProxyFailed, Errors: result.Errors})
}
func parseProxyIDs(c *gin.Context) ([]int64, error) {
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
				return nil, fmt.Errorf("invalid proxy id: %s", part)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
