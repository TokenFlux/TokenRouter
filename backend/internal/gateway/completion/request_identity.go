package completion

import "strings"

// RequestIdentity 在 HTTP 仍存活时提取上下文标识；核心不读取业务 context key。
type RequestIdentity struct{ Client, Local, Upstream, PayloadHash string }

func ForcedRequestID(id string) bool {
	id = strings.TrimSpace(id)
	return strings.HasPrefix(id, "web_search:") || strings.HasPrefix(id, "grok-video:") || strings.HasPrefix(id, "grok_audio:") || strings.HasPrefix(id, "grok_realtime:")
}

// ResolveRequestID 保持任务标识、客户端标识、本地标识、上游标识的既有优先级。
func ResolveRequestID(in RequestIdentity, generate func() string) string {
	upstream := strings.TrimSpace(in.Upstream)
	if upstream != "" && ForcedRequestID(upstream) {
		return upstream
	}
	if client := strings.TrimSpace(in.Client); client != "" {
		return "client:" + client
	}
	if local := strings.TrimSpace(in.Local); local != "" {
		return "local:" + local
	}
	if upstream != "" {
		return upstream
	}
	return "generated:" + generate()
}
func PayloadFingerprint(in RequestIdentity) string {
	if hash := strings.TrimSpace(in.PayloadHash); hash != "" {
		return hash
	}
	if client := strings.TrimSpace(in.Client); client != "" {
		return "client:" + client
	}
	if local := strings.TrimSpace(in.Local); local != "" {
		return "local:" + local
	}
	return ""
}
func StableAudioRequestID(id string, generate func() string) string {
	return stableRequestID("grok_audio:", id, generate)
}
func StableRealtimeRequestID(id string, generate func() string) string {
	return stableRequestID("grok_realtime:", id, generate)
}
func stableRequestID(prefix, id string, generate func() string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, prefix) {
		return id
	}
	if id == "" {
		id = generate()
	}
	return prefix + id
}
