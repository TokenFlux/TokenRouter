// 历史入口拒绝清理拥有固定分类口径，不参与资金写入。
package ops

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const HistoricalIngressClassifierVersion = "ingress-reject-v1"

type HistoricalIngressCandidate struct {
	ID            int64
	StatusCode    int
	Message, Body string
}
type HistoricalIngressStore interface {
	ListCandidates(context.Context, int64, time.Time, int) ([]HistoricalIngressCandidate, error)
	DeleteCandidates(context.Context, []int64, time.Time) (int64, error)
}

func CleanupHistoricalIngress(ctx context.Context, store HistoricalIngressStore, before time.Time, batchSize int, execute bool) (map[string]int64, int64, int64, int64, error) {
	counts := make(map[string]int64)
	var cursor, scanned, matched, deleted int64
	for {
		batch, err := store.ListCandidates(ctx, cursor, before, batchSize)
		if err != nil {
			return nil, scanned, matched, deleted, err
		}
		if len(batch) == 0 {
			break
		}
		cursor = batch[len(batch)-1].ID
		ids := make([]int64, 0, len(batch))
		for _, item := range batch {
			scanned++
			if reason, ok := historicalIngressRejectReason(item); ok {
				matched++
				counts[reason]++
				ids = append(ids, item.ID)
			}
		}
		if execute && len(ids) > 0 {
			n, err := store.DeleteCandidates(ctx, ids, before)
			if err != nil {
				return nil, scanned, matched, deleted, err
			}
			deleted += n
		}
	}
	return counts, scanned, matched, deleted, nil
}
func parseErrorIdentity(body, fallbackMessage string) (string, string) {
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", fallbackMessage
	}
	message := payload.Message
	if message == "" {
		message = payload.Error.Message
	}
	if message == "" {
		message = fallbackMessage
	}
	return strings.TrimSpace(payload.Code), message
}
func historicalIngressRejectReason(item HistoricalIngressCandidate) (string, bool) {
	code, message := parseErrorIdentity(item.Body, item.Message)
	switch code {
	case "API_KEY_REQUIRED":
		return "missing_key", true
	case "INVALID_API_KEY":
		return "invalid_key", true
	case "API_KEY_DISABLED":
		return "key_disabled", true
	case "USER_INACTIVE":
		return "user_inactive", true
	case "GROUP_DELETED":
		return "group_deleted", true
	case "GROUP_DISABLED":
		return "group_disabled", true
	case "GROUP_NOT_ALLOWED":
		return "group_forbidden", true
	case "ACCESS_DENIED":
		return "ip_acl_denied", true
	case "api_key_in_query_deprecated":
		return "query_key_deprecated", true
	}

	normalized := strings.TrimSpace(message)
	switch {
	case normalized == "API key is required":
		return "missing_key", true
	case normalized == "Invalid API key":
		return "invalid_key", true
	case normalized == "API key is disabled":
		return "key_disabled", true
	case normalized == "User account is not active":
		return "user_inactive", true
	case normalized == "API Key 所属分组已删除":
		return "group_deleted", true
	case normalized == "API Key 所属分组已停用":
		return "group_disabled", true
	case normalized == "API Key 所属专属分组不再允许当前用户使用":
		return "group_forbidden", true
	case normalized == "API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group.":
		return "group_unassigned", true
	case strings.HasPrefix(normalized, "Access denied. Your IP is "):
		return "ip_acl_denied", true
	case normalized == "Query parameter api_key is deprecated. Use Authorization header or key instead.":
		return "query_key_deprecated", true
	default:
		return "", false
	}
}
