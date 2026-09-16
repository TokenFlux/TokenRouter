// Beta header 的纯 token 解析复用于原生变体，不引入平台互相引用。
package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseAnthropicBetaHeader 解析 anthropic-beta 头的逗号分隔字符串为 token 列表
func ParseAnthropicBetaHeader(header string) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	if strings.HasPrefix(header, "[") && strings.HasSuffix(header, "]") {
		var parsed []any
		if err := json.Unmarshal([]byte(header), &parsed); err == nil {
			tokens := make([]string, 0, len(parsed))
			for _, item := range parsed {
				token := strings.TrimSpace(fmt.Sprint(item))
				if token != "" {
					tokens = append(tokens, token)
				}
			}
			return tokens
		}
	}
	var tokens []string
	for _, part := range strings.Split(header, ",") {
		t := strings.TrimSpace(part)
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	return tokens
}
