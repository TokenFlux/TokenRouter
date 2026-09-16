// Voice 单次执行持有响应资源，输出由同步 sink 完成；资金与错误策略通过外层处理。
package grok

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type VoiceTarget struct {
	AccountID                           int64
	Endpoint, BaseEndpoint, ContentType string
	Request                             *http.Request `json:"-"`
	Do                                  func(*http.Request) (*http.Response, error)
	Enter                               func() (func(), error)
	AfterExchange                       func(time.Duration, error) error
	BeforeResponse                      func(*http.Response) (bool, error)
	ReadBody                            func(io.Reader) ([]byte, error)
	CopyHeaders                         func(http.Header, http.Header)
}

func (t *VoiceTarget) TargetID() int64  { return t.AccountID }
func (*VoiceTarget) String() string     { return "grok voice target" }
func (t *VoiceTarget) GoString() string { return t.String() }

type VoiceExecutor struct{}

func (VoiceExecutor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, failure error) {
	target, ok := input.Target.(*VoiceTarget)
	if !ok || target == nil || target.Do == nil || target.Request == nil || target.ReadBody == nil {
		return result, errors.New("grok voice target is not configured")
	}
	switch input.Protocol {
	case protocol.ProtocolTTS, protocol.ProtocolSTT, protocol.ProtocolCustomVoices:
	default:
		return result, errors.New("unsupported grok voice protocol")
	}
	if target.Enter != nil {
		done, err := target.Enter()
		if err != nil {
			return result, err
		}
		defer done()
	}
	started := time.Now()
	result.Model = target.BaseEndpoint
	result.UpstreamModel = target.BaseEndpoint
	resp, err := target.Do(target.Request)
	if target.AfterExchange != nil {
		err = target.AfterExchange(time.Since(started), err)
	}
	if err != nil {
		return result, err
	}
	defer func() { _ = resp.Body.Close() }()
	if target.BeforeResponse != nil {
		handled, err := target.BeforeResponse(resp)
		if handled || err != nil {
			return result, err
		}
	}
	data, err := target.ReadBody(resp.Body)
	if err != nil {
		return result, err
	}
	if sink != nil {
		output := upstream.NewOutputContext(sink)
		if target.CopyHeaders != nil {
			target.CopyHeaders(output.Writer.Header(), resp.Header)
		}
		contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/json"
		}
		output.NextEvent(len(data) > 0, true)
		output.Data(resp.StatusCode, contentType, data)
		result.ClientDisconnect = output.Err() != nil
	}
	result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	result.UpstreamHeaders = resp.Header
	result.AudioUsage = EstimateGrokVoiceAudioUsage(target.BaseEndpoint, input.Body, target.ContentType, data, time.Since(started))
	result.HasUsage = result.AudioUsage != nil
	result.Served = len(data) > 0
	result.Duration = time.Since(started)
	return result, nil
}
