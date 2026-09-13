// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	sha256 "crypto/sha256"
	hex "encoding/hex"
	json "encoding/json"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
	http "net/http"
	strconv "strconv"
	strings "strings"
)

const accountListGroupUngroupedQueryValue = "ungrouped"

// List handles listing all accounts with pagination
// GET /api/v1/admin/accounts
func (h *ManagementHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	platform := c.Query("platform")
	accountType := c.Query("type")
	status := c.Query("status")
	search := c.Query("search")
	privacyMode := strings.TrimSpace(c.Query("privacy_mode"))
	sortBy := c.DefaultQuery("sort_by", "name")
	sortOrder := c.DefaultQuery("sort_order", "asc")
	// 标准化和验证 search 参数
	search = strings.TrimSpace(search)
	if len(search) > 100 {
		search = search[:100]
	}
	lite := response.ParseBoolQueryWithDefault(c.Query("lite"), false)
	// 调度分需要跨候选池批量打分并读取负载，默认列表不计算；只有前端列可见时才显式开启。
	includeSchedulerScore := response.ParseBoolQueryWithDefault(c.Query("include_scheduler_score"), false)

	var groupID int64
	if groupIDStr := c.Query("group"); groupIDStr != "" {
		if groupIDStr == accountListGroupUngroupedQueryValue {
			groupID = accountcore.AccountListGroupUngrouped
		} else {
			parsedGroupID, parseErr := strconv.ParseInt(groupIDStr, 10, 64)
			if parseErr != nil {
				response.ErrorFrom(c, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter"))
				return
			}
			if parsedGroupID < 0 {
				response.ErrorFrom(c, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter"))
				return
			}
			groupID = parsedGroupID
		}
	}

	listing, err := h.listing.List(c.Request.Context(), accountcore.ManagementListInput{Page: page, PageSize: pageSize, Platform: platform, AccountType: accountType, Status: status, Search: search, GroupID: groupID, PrivacyMode: privacyMode, SortBy: sortBy, SortOrder: sortOrder, IncludeSchedulerScore: includeSchedulerScore})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	total := listing.Total
	result := make([]AccountWithConcurrency, len(listing.Items))
	for i, state := range listing.Items {
		result[i] = h.runtimePresenter.Project(state)
	}
	h.runtimePresenter.EnrichShadowParents(c.Request.Context(), result)

	etag := BuildAccountsListETag(result, total, page, pageSize, platform, accountType, status, search, lite)
	if etag != "" {
		c.Header("ETag", etag)
		c.Header("Vary", "If-None-Match")
		if response.IfNoneMatchMatched(c.GetHeader("If-None-Match"), etag) {
			c.Status(http.StatusNotModified)
			return
		}
	}

	response.Paginated(c, result, total, page, pageSize)
}
func BuildAccountsListETag(
	items []AccountWithConcurrency,
	total int64,
	page, pageSize int,
	platform, accountType, status, search string,
	lite bool,
) string {
	payload := struct {
		Total       int64                    `json:"total"`
		Page        int                      `json:"page"`
		PageSize    int                      `json:"page_size"`
		Platform    string                   `json:"platform"`
		AccountType string                   `json:"type"`
		Status      string                   `json:"status"`
		Search      string                   `json:"search"`
		Lite        bool                     `json:"lite"`
		Items       []AccountWithConcurrency `json:"items"`
	}{
		Total:       total,
		Page:        page,
		PageSize:    pageSize,
		Platform:    platform,
		AccountType: accountType,
		Status:      status,
		Search:      search,
		Lite:        lite,
		Items:       items,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "\"" + hex.EncodeToString(sum[:]) + "\""
}
