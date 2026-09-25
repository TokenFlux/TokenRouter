// 本文件维护 httpx 的所属能力；兼容入口复用唯一实现。
package httpx

import (
	"strings"
)

// IfNoneMatchMatched 保留旧快照端点的通配、弱标签与逗号分隔匹配。
func IfNoneMatchMatched(ifNoneMatch, etag string) bool {
	if etag == "" || ifNoneMatch == "" {
		return false
	}
	for _, token := range strings.Split(ifNoneMatch, ",") {
		candidate := strings.TrimSpace(token)
		if candidate == "*" {
			return true
		}
		if candidate == etag {
			return true
		}
		if strings.HasPrefix(candidate, "W/") && strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
