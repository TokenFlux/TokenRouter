// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	errors "errors"
	httpguts "golang.org/x/net/http/httpguts"
	strings "strings"
)

const ollamaCloudUsageMaxSessionBytes = 16 * 1024

func NormalizeOllamaCloudUsageCookie(raw string) (string, error) {
	if len(raw) > ollamaCloudUsageMaxSessionBytes {
		return "", errors.New("session is too large")
	}
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("session contains invalid header characters")
	}
	if raw == "" {
		return "", errors.New("session cannot be empty")
	}
	if !httpguts.ValidHeaderFieldValue(raw) {
		return "", errors.New("session contains invalid header characters")
	}
	blockedAttributes := map[string]struct{}{
		"domain": {}, "path": {}, "expires": {}, "max-age": {}, "samesite": {}, "secure": {}, "httponly": {}, "partitioned": {},
	}
	parts := strings.Split(raw, ";")
	normalized := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		name, value, ok := strings.Cut(part, "=")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || value == "" || !httpguts.ValidHeaderFieldName(name) || strings.HasPrefix(name, "$") {
			return "", errors.New("session must be a Cookie header containing name=value pairs")
		}
		lowerName := strings.ToLower(name)
		if _, blocked := blockedAttributes[lowerName]; blocked {
			return "", errors.New("paste a Cookie header, not a Set-Cookie value with attributes")
		}
		if _, duplicate := seen[lowerName]; duplicate {
			return "", errors.New("session contains duplicate cookie names")
		}
		if strings.ContainsAny(value, ";\r\n") {
			return "", errors.New("session contains an invalid cookie value")
		}
		seen[lowerName] = struct{}{}
		if isAllowedOllamaCloudSessionCookie(name) {
			normalized = append(normalized, name+"="+value)
		}
	}
	if len(normalized) == 0 {
		return "", errors.New("session does not contain an allowed Ollama session cookie")
	}
	return strings.Join(normalized, "; "), nil
}
func isAllowedOllamaCloudSessionCookie(name string) bool {
	switch name {
	case "wos-session", "__Secure-session", "session", "ollama_session", "__Host-ollama_session":
		return true
	}
	for _, base := range []string{
		"next-auth.session-token",
		"__Secure-next-auth.session-token",
		"authjs.session-token",
		"__Secure-authjs.session-token",
	} {
		if name == base {
			return true
		}
		if suffix, ok := strings.CutPrefix(name, base+"."); ok && suffix != "" {
			validShard := true
			for _, char := range suffix {
				if char < '0' || char > '9' {
					validShard = false
					break
				}
			}
			if validShard {
				return true
			}
		}
	}
	return false
}
