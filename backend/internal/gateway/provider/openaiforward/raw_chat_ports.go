// RawChatPorts 将平台专有观察与单次原生 Chat 执行隔离。
package openaiforward

import (
	"context"
	"net/http"
	"time"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

type RawChatPorts interface {
	RawFallbackPorts
	GrokCacheIdentity([]byte, string, string) string
	ReplaceModel([]byte, string) []byte
	FastRaw(context.Context, string, []byte) ([]byte, error)
	StripViewImage([]byte) ([]byte, error)
	RawCredential(context.Context) (string, string, error)
	BridgeImages(context.Context, []byte, string) ([]byte, protocolopenai.ForwardUsage, bool, error)
	StripGrokCacheKey([]byte) ([]byte, error)
	GrokEffort([]byte, string) ([]byte, error)
	OllamaBody([]byte) []byte
	RawTarget() (string, error)
	UserAgent() string
	GrokUserAgent() string
	SendRaw(context.Context, string, []byte, bool, string, string, string) (*http.Response, error)
	GrokDecision(context.Context, *http.Response, []byte, string) RawGrokDecision
	ObserveGrokError(*http.Response, string, string)
	GrokRetry(int, []byte) RawGrokRetry
	GrokFailover(*http.Response, []byte, RawGrokRetry, bool) error
	ChatErrorResponse(*http.Response, string) (*Result, error)
	UpdateGrokUsage(context.Context, string, http.Header, int)
}

// RawGrokDecision 仅表达平台健康处置结果，不把可变账号交给编排层。
type RawGrokDecision struct{ Failover, Generic, RetrySame bool }

// RawGrokRetry 固化平台已有的同账号恢复预算。
type RawGrokRetry struct {
	Retryable bool
	Delay     time.Duration
	Deadline  time.Time
	Max       int
}
