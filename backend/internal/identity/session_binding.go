// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	context "context"
	sha256 "crypto/sha256"
	hex "encoding/hex"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	strings "strings"
)

// ErrSessionBindingMismatch 会话绑定的 IP/UA 发生变化，会话已失效。
var ErrSessionBindingMismatch = infraerrors.Unauthorized("SESSION_BINDING_MISMATCH", "session network fingerprint changed, please login again")

// SessionBinding 会话指纹：登录时的客户端 IP 与 User-Agent。
// 会话绑定开启时，两者任一变化即导致会话失效（防止凭证被盗后异地重放）。
type SessionBinding struct {
	IP        string
	UserAgent string
}

// Hash 计算绑定指纹哈希（IP 与 UA 合并，任一变化哈希即变化）。
func (b *SessionBinding) Hash() string {
	if b == nil {
		return ""
	}
	ip := strings.TrimSpace(b.IP)
	ua := strings.TrimSpace(b.UserAgent)
	if ip == "" && ua == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip + "\n" + ua))
	return hex.EncodeToString(sum[:16])
}

type SessionBindingCtxKey struct{}

// WithSessionBinding 将会话指纹注入 context（由 HTTP 入口中间件调用）。
func WithSessionBinding(ctx context.Context, binding *SessionBinding) context.Context {
	if binding == nil {
		return ctx
	}
	return context.WithValue(ctx, SessionBindingCtxKey{}, binding)
}

// SessionBindingFromContext 从 context 提取会话指纹；不存在时返回 nil。
func SessionBindingFromContext(ctx context.Context) *SessionBinding {
	if ctx == nil {
		return nil
	}
	binding, _ := ctx.Value(SessionBindingCtxKey{}).(*SessionBinding)
	return binding
}

// SessionBindingHashFromContext 提取指纹哈希，缺失时返回空串。
func SessionBindingHashFromContext(ctx context.Context) string {
	return SessionBindingFromContext(ctx).Hash()
}
