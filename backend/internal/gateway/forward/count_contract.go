package forward

import (
	"context"
)

// CountPorts 仅提供 token 计数的单账号执行能力，不包含选槽或资金接口。
type CountPorts interface {
	ResolveModel(context.Context, string) string
	ReplaceModel([]byte, string) []byte
	IsCountClaudeCode(context.Context, string) bool
	NormalizeOAuth([]byte, string, NormalizeOptions) ([]byte, string)
	RewriteCache(context.Context, []byte) []byte
	RewriteTools([]byte) ([]byte, bool)
	ToolsLast([]byte) []byte
	Credential(context.Context) error
	TokenKind() string
	BuildCount(context.Context, []byte, string, bool, bool) ([]byte, error)
	SendCount(context.Context, bool) (*ExchangeResponse, error)
	ReadCount() ([]byte, error)
	IsTooLarge(error) bool
	RectifyCount(context.Context, []byte, string) bool
	FilterCountRetry([]byte, string) []byte
	CountError(int, string, string)
	CountSuccess(int, map[string][]string, []byte, bool)
	SetError(int, string, string)
	UnsupportedCount(int, []byte) bool
	CountHealth(context.Context, int, map[string][]string, []byte, string) ErrorDecision
	CountFailover(int, map[string][]string, []byte, bool) error
	CountURL() string
	Observe(Notice)
	Sanitize(string) string
	Log(string)
	Truncate(string, int) string
	TruncateBytes([]byte, int) string
}
