package session

import (
	"net/textproto"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/tidwall/gjson"
)

// QoderExplicitSessionSeed 保留 Header 优先级与首值语义，然后读取原报文的显式种子。
func QoderExplicitSessionSeed(headers map[string][]string, body []byte) string {
	for _, name := range []string{"session_id", "conversation_id", "x-session-id", "x-conversation-id", "X-Claude-Code-Session-Id"} {
		values := headers[textproto.CanonicalMIMEHeaderKey(name)]
		if len(values) > 0 {
			if value := strings.TrimSpace(values[0]); value != "" {
				return value
			}
		}
	}
	for _, path := range []string{"session_id", "conversation_id", "previous_response_id", "prompt_cache_key"} {
		if value := strings.TrimSpace(gjson.GetBytes(body, path).String()); value != "" {
			return value
		}
	}
	return ""
}

// QoderHashFromSeed 复用调度哈希格式，不建立新的缓存命名空间。
func QoderHashFromSeed(seed string) string {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return ""
	}
	hash, _ := scheduler.DeriveSessionHashes("qoder:" + seed)
	return hash
}

// QoderRequestHash 在缺少显式种子时复用原消息摘要及来源因子。
func QoderRequestHash(headers map[string][]string, body []byte, wire string, identity *requeststate.SessionContext, observe func(string, ...any)) string {
	if seed := QoderExplicitSessionSeed(headers, body); seed != "" {
		return QoderHashFromSeed(seed)
	}
	parsed, err := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), wire)
	if err != nil {
		return ""
	}
	parsed.SessionContext = identity
	generated := GenerateSessionHash(parsed, observe)
	if generated == "" {
		return ""
	}
	return QoderHashFromSeed("fallback:" + generated)
}
