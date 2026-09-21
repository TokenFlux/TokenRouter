package openai

import (
	"net/http"
	"strings"
)

// EnsureRemoteCompactionV2Header 确保协商头包含 V2 能力，同时保留
// 客户端已声明的其它能力，避免把多行头压缩时丢失 token。
func EnsureRemoteCompactionV2Header(h http.Header) {
	if h == nil {
		return
	}
	tokens := make([]string, 0, 4)
	for _, value := range h.Values("x-codex-beta-features") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if token == "remote_compaction_v2" {
				return
			}
			tokens = append(tokens, token)
		}
	}
	tokens = append(tokens, "remote_compaction_v2")
	h.Set("x-codex-beta-features", strings.Join(tokens, ","))
}
