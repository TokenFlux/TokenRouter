// Package googleforward 组合 Google 平台的请求准备、凭据和单次执行。
// 平台重试仍由 upstream 持有，HTTP 输出与账号健康写入分别交给对应端口。
package googleforward

import (
	"context"
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// Options 由 app 投影启动配置；动态修复开关仍在原调用位置读取。
type Options struct {
	Configured           bool
	LogErrorBody         bool
	LogErrorBodyMaxBytes int
	DebugHeaders         bool
	ResponseReadLimit    int64
	MaxLineSize          int
	StreamInterval       int
	StreamKeepalive      int
	URLAllowlistEnabled  bool
	AllowInsecureHTTP    bool
	AllowedHosts         []string
	AllowPrivateHosts    bool
}

// LogConfig 保留错误日志的缺省截断长度。
func (o Options) LogConfig() (bool, int) {
	n := 2048
	if o.LogErrorBodyMaxBytes > 0 {
		n = o.LogErrorBodyMaxBytes
	}
	return o.Configured && o.LogErrorBody, n
}
func (o Options) ErrorDetail(body []byte) string {
	enabled, n := o.LogConfig()
	if !enabled {
		return ""
	}
	return logredact.TruncateUTF8(string(body), n)
}

// Output 只表示同步 HTTP 交换与错误观察，不持有业务状态或重试循环。
type Output interface {
	RequestContext() context.Context
	GetHeader(string) string
	Header(string, string)
	WriteHeaders(http.Header, http.Header, *egress.CompiledHeaderFilter)
	Raw(int, string, []byte)
	Sink() upstream.OutputSink
	Commit()
	Count(int)
	GeminiErrorBody(int, string, []byte)
	ReadBody(io.Reader, int64) ([]byte, error)
	Observe(ops.OpsUpstreamErrorEvent)
	SetError(int, string, string)
	FeatureDenied()
	ClaudeError(int, string, string) error
	GoogleError(int, string) error
	ChatError(int, string, string) error
	GeminiCustomCodeSkippedError(*provider.ExecutionAccount, int, string, []byte, func()) error
	GeminiNativeUpstreamError(*provider.ExecutionAccount, *http.Response, []byte, string, bool) error
	GeminiMappedError(*provider.ExecutionAccount, int, string, []byte) error
	GeminiOpenAICompatMappedError(*provider.ExecutionAccount, int, string, []byte, gemini.OpenAICompatProtocol) error
	GeminiOpenAICompatError(gemini.OpenAICompatProtocol, int, string, string) error
	AntigravityCompatError(int, string, string) error
	MappedAntigravityCompatError(*provider.ExecutionAccount, int, string, []byte) error
	MapAntigravityCollectionError(error) error
	MappedClaudeError(*provider.ExecutionAccount, int, string, []byte) error
}

// Gemini 只组合凭据、传输和错误观察；不持有另一平台服务或完整配置。
type Gemini struct {
	Options      Options
	Tokens       *account.GeminiTokenSource
	Health       *accountprovider.UpstreamHealth
	Errors       *accountprovider.GeminiErrorObserver
	Transport    httpclient.UpstreamTransport
	HeaderFilter *egress.CompiledHeaderFilter
	Enter        func() (func(), error)
}

// Antigravity 使用应用已有的健康与重试实例，每次调用独立构造平台输入。
type Antigravity struct {
	Options   Options
	Tokens    *account.AntigravityTokenSource
	Retry     *accountprovider.AntigravityRetry
	Errors    *accountprovider.AntigravityErrorObserver
	Store     provider.ExecutionAccountStore
	Transport httpclient.UpstreamTransport
	Gateway   *gateway.RuntimeSettings
	Routing   *routing.RuntimeSettings
	Sticky    session.GatewayCache
	Enter     func() (func(), error)
}

// attempt 的图片计数与工具映射只属于一次平台调用，切换账号时重新创建。
type attempt struct {
	Output
	ToolNames *anthropic.ToolNameRewrite
	Images    int
}

func (a *attempt) reverseTools(body []byte) []byte {
	return anthropic.RestoreToolNamesInBytes(body, a.ToolNames)
}
func (a *attempt) observeImages(body []byte) {
	if n := gemini.CountGeminiInlineImageOutputs(body); n > a.Images {
		a.Images = n
	}
}
func (a *attempt) imageCount(original, mapped string) int {
	if a.Images > 0 {
		return a.Images
	}
	if antigravity.IsImageGenerationModel(original) || antigravity.IsImageGenerationModel(mapped) {
		return 1
	}
	return 0
}
func messagesResult(value *forward.Result) *forward.MessagesResult {
	if value == nil {
		return nil
	}
	copy := forward.MessagesResult(*value)
	return &copy
}
