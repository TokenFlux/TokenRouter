//go:build unit

// 本文件只验证已约定的完成失败边界，存储原子性另由真实集成测试证明。
package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

func TestS09QoderNativeChainCompletionFailures(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		billingFail, logFail bool
	}{{name: "settlement-failure", billingFail: true}, {name: "record-failure", logFail: true}} {
		t.Run(tc.name, func(t *testing.T) {
			account, platform, client := newQoderGatewayForwardTestService()
			client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "fixture failure"})
			usageRepo := &openAIRecordUsageLogRepoStub{}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			if tc.billingFail {
				billingRepo.err = errors.New("fixture billing unavailable")
			}
			if tc.logFail {
				usageRepo.err = MarkUsageLogCreateNotPersisted(errors.New("fixture usage unavailable"))
			}
			completion := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			rec := httptest.NewRecorder()
			body := []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			// 与生产桥接使用相同目标投影和结果转换，实际调用保留的完成用例。
			executor, input := platform.PrepareQoderAttempt(nil, account, body, protocol.ProtocolOpenAIChatCompletions, "auto")
			completed, released, bound := 0, 0, 0
			var completionErr error
			ports := gateway.RequestPorts{CanFailover: func(error) bool { return true }, Select: func(context.Context, map[int64]struct{}) (*gateway.Selection, error) {
				return &gateway.Selection{Acquired: true, Release: func() { released++ }, Executor: executor, Input: input, Bind: func(context.Context, upstream.AttemptResult) { bound++ }, Complete: func(ctx context.Context, r upstream.AttemptResult) {
					completed++
					completionErr = completion.RecordUsage(ctx, &RecordUsageInput{Result: ForwardResultFromAttempt(r), APIKey: &APIKey{ID: 505}, User: &User{ID: 605}, Account: account})
				}}, nil
			}}
			err := gateway.NewQoderUseCase(3, time.Second).Run(context.Background(), gateway.Request{Stream: true}, ports, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: rec}})
			require.Error(t, err)
			require.Contains(t, rec.Body.String(), "served")
			require.Len(t, client.requests, 1)
			require.Equal(t, 1, completed)
			require.Equal(t, 1, released)
			require.Zero(t, bound)
			require.Equal(t, 1, billingRepo.calls)
			require.Equal(t, 1, usageRepo.calls)
			require.Equal(t, 12, usageRepo.lastLog.InputTokens)
			require.Equal(t, 3, usageRepo.lastLog.OutputTokens)
			if tc.billingFail {
				require.ErrorIs(t, completionErr, billingRepo.err)
				require.Zero(t, usageRepo.lastLog.ActualCost)
			} else {
				require.NoError(t, completionErr)
			}
		})
	}
}
