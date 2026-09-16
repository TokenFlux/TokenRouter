// Embeddings 单次执行拥有请求、响应体与输出；账号切换和计费由网关决定。
package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
)

type EmbeddingsTarget struct {
	AccountID             int64
	Model, URL, UserAgent string
	Token                 string      `json:"-"`
	ForwardHeaders        http.Header `json:"-"`
	StartedAt             time.Time
	RequestContext        func(context.Context) (context.Context, context.CancelFunc)
	Enter                 func() (func(), error)
	ApplyHeaders          func(http.Header)
	Do                    func(*http.Request) (*http.Response, error)
	TransportError        func(error) error
	ReadErrorBody         func(*http.Response) []byte
	HTTPError             func(*http.Response, []byte) error
	ReadBody              func(io.Reader) ([]byte, error)
	ReadFailure           func(error) error
	WriteHeaders          func(http.Header, http.Header)
}

func (t *EmbeddingsTarget) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.AccountID
}
func (t *EmbeddingsTarget) String() string {
	return fmt.Sprintf("openai embeddings target account=%d", t.TargetID())
}
func (t *EmbeddingsTarget) GoString() string { return t.String() }

type EmbeddingsExecutor struct{}

func (EmbeddingsExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	t, ok := input.Target.(*EmbeddingsTarget)
	if !ok || t == nil {
		return result, errors.New("openai embeddings target is not configured")
	}
	if input.Protocol != protocol.ProtocolEmbeddings {
		return result, errors.New("unsupported embeddings protocol")
	}
	if t.Enter != nil {
		done, err := t.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := t.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	defer func() {
		result.Duration = time.Since(started)
		result.Cancelled = errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded)
		if failure != nil {
			result.FailureClass = "upstream"
		}
	}()
	upstreamCtx, release := t.RequestContext(ctx)
	request, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, t.URL, bytes.NewReader(input.Body))
	release()
	if err != nil {
		return result, fmt.Errorf("build upstream request: %w", err)
	}
	request = request.WithContext(upstream.WithHTTPUpstreamProfile(request.Context(), upstream.HTTPUpstreamProfileOpenAI))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+t.Token)
	request.Header.Set("Accept", "application/json")
	for key, values := range t.ForwardHeaders {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if t.UserAgent != "" {
		request.Header.Set("user-agent", t.UserAgent)
	}
	t.ApplyHeaders(request.Header)
	resp, err := t.Do(request)
	if err != nil {
		return result, t.TransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body := t.ReadErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return result, t.HTTPError(resp, body)
	}
	body, err := t.ReadBody(resp.Body)
	if err != nil {
		return result, t.ReadFailure(err)
	}
	output := upstream.NewOutputContext(sink)
	if !output.Writer.Written() {
		if resp.Header != nil {
			t.WriteHeaders(output.Writer.Header(), resp.Header)
		}
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		output.Writer.Header().Set("Content-Type", contentType)
		output.Writer.WriteHeader(resp.StatusCode)
		_, _ = output.Writer.Write(body)
	}
	usage := wire.ExtractEmbeddingsUsage(body)
	result.RequestID = FirstNonEmptyString(resp.Header.Get("x-request-id"), resp.Header.Get("request-id"))
	result.UpstreamHeaders = resp.Header
	result.Model = input.ResponseModel
	result.UpstreamModel = t.Model
	result.Usage = upstream.TokenUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
	}
	result.ImageInputTokens = usage.ImageInputTokens
	result.HasUsage = gjson.GetBytes(body, "usage").IsObject()
	result.Served = len(gjson.GetBytes(body, "data").Array()) > 0
	result.HTTPCommitted = output.Writer.Written()
	return result, nil
}
