package provider

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/bstats"
)

// client_test.go verifies the official bStats wire contract against local HTTP
// fixtures. Deterministic system observations expose the exact outbound field
// whitelist, while blocked and failed transports exercise cancellation without
// contacting the public endpoint or adding fake deployment statistics.

// roundTripFunc permits controlled transport responses without a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

// TestClientWireProtocolAndLogging decodes a compressed report received locally.
// It checks the complete whitelist, official headers, both registered version
// charts, and opt-in payload and response diagnostics including the instance UUID.
func TestClientWireProtocolAndLogging(t *testing.T) {
	var received map[string]any
	var requestHeaders http.Header
	var contentLength int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHeaders = r.Header.Clone()
		contentLength = r.ContentLength
		require.Equal(t, http.MethodPost, r.Method)
		require.True(t, r.Close)
		reader, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		defer func() { require.NoError(t, reader.Close()) }()
		require.NoError(t, json.NewDecoder(reader).Decode(&received))
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()
	var logs []string
	client := NewClient("v0.6.0", func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) })
	require.Equal(t, reportURL, client.endpoint)
	require.Equal(t, 10*time.Second, client.client.Timeout)
	client.endpoint = server.URL
	client.platform = func(context.Context) (platformData, error) {
		return platformData{OSName: "Linux", OSVersion: "6.1", OSArch: "amd64", CoreCount: 4}, nil
	}
	id := "fe5233bb-2ed6-4a60-99da-45b42c6c29ec"
	require.NoError(t, client.Submit(context.Background(), id, bstats.Config{Enabled: true, LogSentData: true, LogResponseStatusText: true}))
	require.Greater(t, contentLength, int64(0))
	require.Equal(t, "application/json", requestHeaders.Get("Accept"))
	require.Equal(t, "application/json", requestHeaders.Get("Content-Type"))
	require.Equal(t, "gzip", requestHeaders.Get("Content-Encoding"))
	require.Equal(t, "Metrics-Service/1", requestHeaders.Get("User-Agent"))
	require.Equal(t, "close", requestHeaders.Get("Connection"))
	require.Empty(t, requestHeaders.Get("Accept-Encoding"))
	require.Equal(t, map[string]any{
		"osName": "Linux", "osVersion": "6.1", "osArch": "amd64", "coreCount": float64(4),
		"serverUUID": id, "metricsVersion": "3.2.1",
		"service": map[string]any{"id": float64(35120), "customCharts": []any{
			map[string]any{"chartId": "tokenrouter_version", "data": map[string]any{"value": "v0.6.0"}},
			map[string]any{"chartId": "go_version", "data": map[string]any{"value": runtime.Version()}},
		}},
	}, received)
	require.Len(t, logs, 2)
	require.Contains(t, logs[0], id)
	require.Contains(t, logs[1], `{"success":true}`)
	logs = nil
	require.NoError(t, client.Submit(context.Background(), id, bstats.Config{Enabled: true}))
	require.Empty(t, logs)
}

// RoundTrip delegates the request to the configured transport fixture callback.
func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestClientCancellationAndTimeout blocks transport until its request is cancelled.
// Both the production client's bounded timeout and caller cancellation must
// release the submission with an error and without an internal retry.
func TestClientCancellationAndTimeout(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelCaller), func(t *testing.T) {
			client := NewClient("v-test", nil)
			client.platform = func(context.Context) (platformData, error) { return platformData{}, nil }
			client.client.Timeout = 20 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if cancelCaller {
					cancel()
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})
			err := client.Submit(ctx, "fixture", bstats.Config{})
			if cancelCaller {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, context.DeadlineExceeded)
			}
			require.Equal(t, 1, calls)
		})
	}
}

// TestClientFailuresAndEmptyChart supplies collection failures, rejected responses,
// and oversized response bodies. Empty application versions omit only their
// custom chart, and none of these paths retries or logs a successful response.
func TestClientFailuresAndEmptyChart(t *testing.T) {
	client := NewClient("", nil)
	client.platform = func(context.Context) (platformData, error) { return platformData{}, errors.New("unsupported") }
	require.ErrorContains(t, client.Submit(context.Background(), "fixture", bstats.Config{}), "unsupported")
	client.platform = func(context.Context) (platformData, error) { return platformData{}, nil }
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusFound, http.StatusOK} {
		client.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			reader, err := gzip.NewReader(r.Body)
			require.NoError(t, err)
			defer func() { require.NoError(t, reader.Close()) }()
			var payload report
			require.NoError(t, json.NewDecoder(reader).Decode(&payload))
			require.Len(t, payload.Service.CustomCharts, 1)
			require.Equal(t, "go_version", payload.Service.CustomCharts[0].ChartID)
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxResponseBytes+1)))}, nil
		})
		require.Error(t, client.Submit(context.Background(), "fixture", bstats.Config{}))
	}
}
