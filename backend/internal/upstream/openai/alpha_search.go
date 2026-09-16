// Alpha Search 单次执行管理网络/输出，账号选择和资金完成仍由调用方负责。
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// AlphaSearchTarget 不暴露完整账号，准备好的请求禁止序列化或日志展开。
type AlphaSearchTarget struct {
	AccountID         int64
	Request           *http.Request `json:"-"`
	ResponsesFallback bool
	Model             string
	Enter             func() (func(), error)
	Do                func(*http.Request) (*http.Response, error)
	Latency           func(time.Duration)
	TransportError    func(error) error
	ReadBody          func(io.Reader) ([]byte, error)
	HTTPError         func(*http.Response, []byte) error
	UpdateQuota       func(http.Header)
	Headers           func(http.Header, http.Header)
}

func (t *AlphaSearchTarget) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.AccountID
}
func (t *AlphaSearchTarget) String() string {
	return fmt.Sprintf("openai alpha search target account=%d", t.TargetID())
}
func (t *AlphaSearchTarget) GoString() string { return t.String() }

// AlphaSearchExecutor 保留独立搜索与 PAT Responses 回退的错误/用量差异。
type AlphaSearchExecutor struct{}

func (AlphaSearchExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	t, ok := input.Target.(*AlphaSearchTarget)
	if !ok || t == nil || t.Request == nil {
		return result, errors.New("openai alpha search target is not configured")
	}
	if input.Protocol != protocol.ProtocolAlphaSearch {
		return result, errors.New("unsupported alpha search protocol")
	}
	if t.Enter != nil {
		done, err := t.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := time.Now()
	resp, err := t.Do(t.Request)
	t.Latency(time.Since(started))
	if err != nil {
		return result, t.TransportError(err)
	}
	// 错误处理可能回卷 resp.Body；始终释放真正承载连接的原始响应体。
	originalBody := resp.Body
	defer func() { _ = originalBody.Close() }()
	// 耗时仍在响应释放前取得；失败观测不改变旧错误返回和计费决策。
	defer func() {
		result.Duration = time.Since(started)
		result.Cancelled = errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded)
		if failure != nil {
			result.FailureClass = "upstream"
		}
	}()
	body, err := t.ReadBody(resp.Body)
	if err != nil {
		if t.ResponsesFallback {
			return result, fmt.Errorf("read alpha search responses fallback response: %w", err)
		}
		return result, fmt.Errorf("read alpha search response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		if err := t.HTTPError(resp, body); err != nil {
			return result, err
		}
	}
	output := upstream.NewDeferredOutputContext(sink)
	success := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if !t.ResponsesFallback || success {
		t.UpdateQuota(resp.Header)
	}
	if t.ResponsesFallback && success {
		converted, err := OpenAIAlphaSearchResponseFromResponsesSSE(body)
		if err != nil {
			return result, err
		}
		output.Data(http.StatusOK, "application/json", converted)
	} else {
		t.Headers(output.Writer.Header(), resp.Header)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		output.Data(resp.StatusCode, contentType, body)
	}
	result.HTTPCommitted = output.Writer.Written()
	if !success {
		return result, nil
	}
	result.RequestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
	result.UpstreamHeaders = resp.Header
	result.Model = input.ResponseModel
	result.UpstreamModel = t.Model
	result.SearchCount = 1
	result.Served = true
	return result, nil
}
