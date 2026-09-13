// 查询参与函数复用调用者的连接与主查询，保持批量及分页前排序。
package query

import (
	"context"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres" // KeyLatestUsageLogIPsQuery 按数据库方言生成批量查询：PostgreSQL 使用数组，

	// 其它方言使用逐项占位符，便于 SQLite 回归测试覆盖真实 SQL。
	// 每个 Key 只做一次有序索引探测，避免为整段历史记录计算窗口排名。
	"github.com/lib/pq"
)

func KeyLatestUsageLogIPsQuery(apiKeyIDs []int64, dialectName string) (string, []any) {
	if dialectName == dialect.Postgres {
		return `
		SELECT requested.api_key_id, latest.ip_address
		FROM unnest($1::bigint[]) AS requested(api_key_id)
		CROSS JOIN LATERAL (
			SELECT ul.ip_address
			FROM usage_logs AS ul
			WHERE ul.api_key_id = requested.api_key_id
				AND ul.ip_address IS NOT NULL
				AND ul.ip_address <> ''
			ORDER BY ul.created_at DESC, ul.id DESC
			LIMIT 1
		) AS latest`, []any{pq.Array(apiKeyIDs)}
	}

	placeholders := make([]string, len(apiKeyIDs))
	args := make([]any, len(apiKeyIDs))
	for i, id := range apiKeyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	return fmt.Sprintf(`
		SELECT api_key_id, ip_address
		FROM (
			SELECT api_key_id, ip_address,
				ROW_NUMBER() OVER (PARTITION BY api_key_id ORDER BY created_at DESC, id DESC) AS rn
			FROM usage_logs
			WHERE api_key_id IN (%s)
				AND ip_address IS NOT NULL
				AND ip_address <> ''
		) ranked
		WHERE rn = 1`, strings.Join(placeholders, ", ")), args
}

// KeyLatestUsageLogIPs 从 usage_logs 查询每个 API Key 最新的非空 IP。
func KeyLatestUsageLogIPs(ctx context.Context, db infra.Executor, dialectName string, apiKeyIDs []int64) (result map[int64]string, err error) {
	if len(apiKeyIDs) == 0 || db == nil {
		return map[int64]string{}, nil
	}

	query, args := KeyLatestUsageLogIPsQuery(apiKeyIDs, dialectName)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	out := make(map[int64]string, len(apiKeyIDs))
	for rows.Next() {
		var apiKeyID int64
		var ipAddress string
		if err := rows.Scan(&apiKeyID, &ipAddress); err != nil {
			return nil, err
		}
		out[apiKeyID] = ipAddress
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
