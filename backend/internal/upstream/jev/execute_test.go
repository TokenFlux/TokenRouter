package jev

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type outputProbe struct {
	body []byte
	fail bool
}

// TestRetryAfterResetTime 检查秒数、HTTP 日期及非法重试时间。
func TestRetryAfterResetTime(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	for _, value := range []string{"17", now.Add(17 * time.Second).Format(http.TimeFormat)} {
		reset := RetryAfterResetTime(http.Header{"Retry-After": []string{value}}, now)
		require.NotNil(t, reset)
		require.Equal(t, now.Add(17*time.Second), *reset)
	}
	for _, value := range []string{"", "invalid", "-1", "0", "NaN", "Inf", "1e100", now.Add(-time.Hour).Format(http.TimeFormat)} {
		require.Nil(t, RetryAfterResetTime(http.Header{"Retry-After": []string{value}}, now), value)
	}
}

func (s *outputProbe) Begin(upstream.OutputHead) error { return nil }
func (s *outputProbe) Emit(e upstream.OutputEvent) error {
	s.body = append(s.body, e.Data...)
	if s.fail {
		return errors.New("client disconnected")
	}
	return nil
}

// TestExecuteKeepsUsageAfterClientWriteFailure 防止下游断开后丢弃已返回的上游用量。
func TestExecuteKeepsUsageAfterClientWriteFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/systemone", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "configured", r.Header.Get("X-Test"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"model":"jev-latest"`)
		w.Header().Set("X-Request-Id", "upstream-test")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":0.9}},"usage":{"input_tokens":100,"output_tokens":20},"extra":"kept"}`)
	}))
	defer server.Close()
	for _, fail := range []bool{false, true} {
		sink := &outputProbe{fail: fail}
		target := &Target{ProviderID: 1, Model: "jev-latest", URL: EndpointURL(server.URL, "systemone"), Token: "test-key", Do: server.Client().Do, ApplyHeaders: func(h http.Header) { h.Set("X-Test", "configured") }}
		result, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolSystemOne, Body: systemone.ProbeBody("jev-latest", ""), ResponseModel: "client-alias", Target: target}, sink)
		require.Equal(t, fail, err != nil)
		require.True(t, result.Served)
		require.True(t, result.HasUsage)
		require.Equal(t, 100, result.Usage.InputTokens)
		require.Equal(t, "jev-1.13.0", result.UpstreamResponseModel)
		require.Contains(t, string(sink.body), `"model":"client-alias"`)
		require.Contains(t, string(sink.body), `"extra":"kept"`)
	}
}

func TestExecuteInvalidUsageStillServesAnswers(t *testing.T) {
	sink := &outputProbe{}
	target := &Target{Model: "jev-latest", URL: "https://example.com/v1/systemone", Do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"model":"jev-latest","answers":{"available":{"type":"noul","noul":1}}}`))}, nil
	}}
	r, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolSystemOne, Body: systemone.ProbeBody("jev-latest", ""), Target: target}, sink)
	require.NoError(t, err)
	require.True(t, r.Served)
	require.False(t, r.HasUsage)
	require.NotEmpty(t, sink.body)
}

func TestExecuteHTTPFailureAndResponseLimit(t *testing.T) {
	for _, status := range []int{401, 422, 429, 529, 503} {
		target := &Target{URL: "https://example.com/v1/systemone", Do: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"17"}}, Body: io.NopCloser(strings.NewReader(`{"error":"busy"}`))}, nil
		}}
		sink := &outputProbe{}
		r, err := (Executor{}).Execute(context.Background(), upstream.AttemptInput{Protocol: protocol.ProtocolSystemOne, Body: systemone.ProbeBody("jev-latest", ""), Target: target}, sink)
		var failure *HTTPError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, status, failure.Status)
		require.Equal(t, "17", failure.Header.Get("Retry-After"))
		require.False(t, r.Served)
		require.Empty(t, sink.body)
	}
	for _, base := range []string{"https://example.com", "https://example.com/v1", "https://example.com/v1/"} {
		require.Equal(t, "https://example.com/v1/systemone", EndpointURL(base, "systemone"))
	}
}
