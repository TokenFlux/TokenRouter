package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/lib/pq"
)

// ListRequestTimings 批量读取 http.access 的阶段字段，按 client_request_id 返回最新一条记录。
// 使用独立查询避免管理员使用记录列表产生逐行查询；没有匹配日志的请求不会出现在结果中。
func (r *Store) ListRequestTimings(ctx context.Context, clientRequestIDs []string) (map[string]*ops.OpsRequestTiming, error) {
	result := make(map[string]*ops.OpsRequestTiming)
	if r == nil || r.db == nil || len(clientRequestIDs) == 0 {
		return result, nil
	}

	ids := make([]string, 0, len(clientRequestIDs))
	seen := make(map[string]struct{}, len(clientRequestIDs))
	for _, raw := range clientRequestIDs {
		id := strings.TrimSpace(raw)
		if id == "" || len(id) > 128 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return result, nil
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT DISTINCT ON (l.client_request_id)
  l.client_request_id,
  COALESCE(l.extra::text, '{}')
FROM ops_system_logs l
WHERE l.component = 'http.access'
  AND l.message = 'http request completed'
  AND l.client_request_id = ANY($1)
ORDER BY l.client_request_id, l.created_at DESC, l.id DESC
`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var clientRequestID string
		var extraRaw sql.NullString
		if err := rows.Scan(&clientRequestID, &extraRaw); err != nil {
			return nil, err
		}
		var extra map[string]any
		raw := strings.TrimSpace(extraRaw.String)
		if extraRaw.Valid && raw != "" && raw != "null" && raw != "{}" {
			if err := json.Unmarshal([]byte(raw), &extra); err != nil {
				return nil, fmt.Errorf("decode request timing extra: %w", err)
			}
		}
		if timing := ops.OpsRequestTimingFromExtra(extra); timing != nil {
			result[strings.TrimSpace(clientRequestID)] = timing
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
