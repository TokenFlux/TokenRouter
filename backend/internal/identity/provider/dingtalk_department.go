// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// resolveDingTalkDeptPath 从叶部门递归向上拼 "公司/部门/子部门" 路径字符串。
// 遇 dept_id=1（根）或 parent_id=0 停止。加 visited set 防循环，最多 50 层。
func ResolveDingTalkDeptPath(ctx context.Context, client *DingTalkClient, deptID int64) (string, error) {
	slog.Info("dingtalk sync: resolve dept path start", "dept_id", deptID)
	const maxDepth = 50
	visited := make(map[int64]bool, maxDepth)
	var parts []string

	current := deptID
	for i := 0; i < maxDepth; i++ {
		if current < 1 || visited[current] {
			break
		}
		visited[current] = true

		info, err := client.GetDeptInfo(ctx, current)
		if err != nil {
			return "", fmt.Errorf("get dept info %d: %w", current, err)
		}
		if strings.TrimSpace(info.Name) != "" {
			parts = append([]string{strings.TrimSpace(info.Name)}, parts...)
		}
		// 钉钉根部门 dept_id=1，ParentID 通常为 0；遇到 0 / self 终止避免循环。
		if info.ParentID < 1 || info.ParentID == current {
			break
		}
		current = info.ParentID
	}

	// 去除根组织名（parts[0] 始终是企业全称），仅保留部门层级。
	// 例：["公司","A","B"] → "A/B"；["公司"] → ""（公司直属）。
	if len(parts) > 0 {
		parts = parts[1:]
	}

	return strings.Join(parts, "/"), nil
}
