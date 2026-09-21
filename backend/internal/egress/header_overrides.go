// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	strings "strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	httpguts "golang.org/x/net/http/httpguts"
)

// 请求头覆写（header override）：对 Anthropic / OpenAI / 国产供应商平台的
// api_key 账号，以及 Grok 平台的 api_key / oauth 账号生效。
// 管理员在账号上配置一组 header name -> value，转发到上游前用配置值覆盖同名请求头
// （匹配不区分大小写）；value 为空的条目视为"未填写"，不参与覆盖。
const (
	credKeyHeaderOverrideEnabled = "header_override_enabled"
	credKeyHeaderOverrides       = "header_overrides"

	maxHeaderOverrideEntries     = 64
	maxHeaderOverrideNameLength  = 200
	maxHeaderOverrideValueLength = 8192
)

// headerOverrideBlockedNames 禁止覆写的请求头（小写）。
//   - 连接控制/逐跳头：由 HTTP 栈管理，覆写会破坏请求传输；
//   - host/content-length：由 Go 的 Request.Host / ContentLength 字段管理，header 覆写不生效或产生冲突；
//   - content-type：承载报文框架信息（multipart boundary 为每请求随机值），静态覆写必然与 body 不匹配；
//   - authorization/x-api-key/cookie 等：上游认证头由账号凭据统一注入，禁止通过覆写篡改或重新引入；
//   - accept-encoding：强制压缩会破坏网关对上游流式响应（SSE/usage）的解析；
//   - sec-websocket-*：WebSocket 握手头由拨号器管理（OpenAI WS 模式）；
//   - session_id/x-claude-code-session-id/x-grok-conv-id 等：逐请求会话隔离头，
//     固定值会造成会话串扰。
var headerOverrideBlockedNames = map[string]struct{}{
	"host":                     {},
	"content-length":           {},
	"content-type":             {},
	"transfer-encoding":        {},
	"connection":               {},
	"keep-alive":               {},
	"proxy-authenticate":       {},
	"proxy-authorization":      {},
	"proxy-connection":         {},
	"te":                       {},
	"trailer":                  {},
	"upgrade":                  {},
	"authorization":            {},
	"x-api-key":                {},
	"x-goog-api-key":           {},
	"cookie":                   {},
	"accept-encoding":          {},
	"sec-websocket-key":        {},
	"sec-websocket-version":    {},
	"sec-websocket-extensions": {},
	"sec-websocket-protocol":   {},
	"sec-websocket-accept":     {},
	"session_id":               {},
	"conversation_id":          {},
	"x-codex-turn-state":       {},
	"x-codex-turn-metadata":    {},
	"chatgpt-account-id":       {},
	"x-claude-code-session-id": {},
	"x-client-request-id":      {},
	"x-grok-conv-id":           {},
}

func IsHeaderOverrideBlockedName(lowerName string) bool {
	_, blocked := headerOverrideBlockedNames[lowerName]
	return blocked
}

// ResolveHeaderOverrides 解析并防御性过滤原始覆写表：保存路径已做校验，
// 这里兜底未经 Normalize 落库的数据（含名单扩充前保存的旧配置），非法条目直接跳过。
func ResolveHeaderOverrides(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	result := make(map[string]string, len(raw))
	for name, value := range raw {
		lowerName, value, err := NormalizeHeaderOverrideEntry(name, value)
		if err != nil || lowerName == "" || value == "" {
			continue
		}
		result[lowerName] = value
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// NormalizeHeaderOverrideCredentials 校验并原地规范化 credentials 中的请求头覆写字段。
// 供账号创建/更新/批量更新的保存路径调用；credentials 未携带相关字段时为 no-op。
// 规范化内容：header 名转小写并去除首尾空白，value 去除首尾空白，丢弃名和值均为空的条目。
func NormalizeHeaderOverrideCredentials(credentials map[string]any) error {
	if credentials == nil {
		return nil
	}
	if raw, ok := credentials[credKeyHeaderOverrideEnabled]; ok && raw != nil {
		if _, isBool := raw.(bool); !isBool {
			return infraerrors.New(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
				"header_override_enabled must be a boolean")
		}
	}
	raw, ok := credentials[credKeyHeaderOverrides]
	if !ok || raw == nil {
		return nil
	}

	var entries map[string]any
	switch m := raw.(type) {
	case map[string]any:
		entries = m
	case map[string]string:
		entries = make(map[string]any, len(m))
		for k, v := range m {
			entries[k] = v
		}
	default:
		return infraerrors.New(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header_overrides must be an object of header name to string value")
	}

	if len(entries) > maxHeaderOverrideEntries {
		return infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header_overrides supports at most %d entries", maxHeaderOverrideEntries)
	}

	normalized := make(map[string]any, len(entries))
	for name, rawValue := range entries {
		value, isString := rawValue.(string)
		if !isString {
			return infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
				"header %q value must be a string", name)
		}
		lowerName, value, err := NormalizeHeaderOverrideEntry(name, value)
		if err != nil {
			return err
		}
		if lowerName == "" {
			continue // 丢弃完全为空的占位行
		}
		if _, dup := normalized[lowerName]; dup {
			return infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
				"duplicate header name %q (matching is case-insensitive)", lowerName)
		}
		normalized[lowerName] = value
	}
	credentials[credKeyHeaderOverrides] = normalized
	return nil
}

// NormalizeHeaderOverrideEntry 校验并规范化单个覆写条目，保存路径（Normalize，err → 400）
// 与应用路径（ResolveHeaderOverrides，err → 跳过）共用同一套规则，避免两处校验漂移。
// 名和值均为空表示空占位行，返回 ("", "", nil)；空 value 的具名条目合法（模板占位）。
func NormalizeHeaderOverrideEntry(name, value string) (string, string, error) {
	lowerName := strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	if lowerName == "" {
		if value == "" {
			return "", "", nil
		}
		return "", "", infraerrors.New(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header name must not be empty")
	}
	if len(lowerName) > maxHeaderOverrideNameLength {
		return "", "", infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header name %q exceeds %d characters", lowerName, maxHeaderOverrideNameLength)
	}
	if !ValidHeaderName(lowerName) {
		return "", "", infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"invalid header name %q", lowerName)
	}
	if IsHeaderOverrideBlockedName(lowerName) {
		return "", "", infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header %q is not allowed to be overridden", lowerName)
	}
	if len(value) > maxHeaderOverrideValueLength {
		return "", "", infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header %q value exceeds %d characters", lowerName, maxHeaderOverrideValueLength)
	}
	if !httpguts.ValidHeaderFieldValue(value) {
		return "", "", infraerrors.Newf(infraerrors.Category(400), "INVALID_HEADER_OVERRIDE",
			"header %q has an invalid value", lowerName)
	}
	return lowerName, value, nil
}

// ValidHeaderName 只验证 HTTP token 语法，不附加账号权限或覆写禁止项。
func ValidHeaderName(name string) bool { return httpguts.ValidHeaderFieldName(name) }
