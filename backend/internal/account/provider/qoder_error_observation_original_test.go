package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

type qoderRateLimitRepoStub struct {
	rateLimitedID int64
	resetAt       time.Time
}

func (r *qoderRateLimitRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.resetAt = resetAt
	return nil
}

type qoderPolicyContextRepoStub struct {
	qoderRateLimitRepoStub
	rateLimitCtxErr error
}

func (r *qoderPolicyContextRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	r.rateLimitCtxErr = ctx.Err()
	return r.qoderRateLimitRepoStub.SetRateLimited(ctx, id, resetAt)
}

func TestQoderGatewayAgentLimitSetsRateLimitedUntilReset(t *testing.T) {
	repo := &qoderRateLimitRepoStub{}
	accountID := int64(77)
	err := &qoder.APIError{
		StatusCode:          http.StatusTooManyRequests,
		Code:                "115",
		AgentLimitResetTime: 1783841289162,
	}

	ObserveQoderUpstreamError(context.Background(), accountID, repo, err)

	require.Equal(t, int64(77), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

func TestQoderGatewayUpstreamErrorPolicyIgnoresCanceledRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &qoderPolicyContextRepoStub{}

	ObserveQoderUpstreamError(ctx, 79, repo, &qoder.APIError{StatusCode: http.StatusTooManyRequests})

	require.Equal(t, int64(79), repo.rateLimitedID)
	require.NoError(t, repo.rateLimitCtxErr)
}

func TestQoderGatewayNonStreamingSSEAgentLimitSetsRateLimited(t *testing.T) {
	accountID := int64(78)
	repo := &qoderRateLimitRepoStub{}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"code\\\":\\\"115\\\",\\\"message\\\":\\\"{\\\\\\\"agentLimitResetTime\\\\\\\":1783841289162}\\\"}\",\"statusCodeValue\":429,\"statusCode\":\"TOO_MANY_REQUESTS\"}\n\n",
		)),
	}

	events, err := qoder.ReadQoderSSEEvents(resp)
	if err != nil {
		ObserveQoderUpstreamError(context.Background(), accountID, repo, err)
	}

	require.Error(t, err)
	require.Empty(t, events)
	require.Equal(t, int64(78), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

// SetOverloaded 拒绝限流夹具中不应发生的另一类健康写入。
func (r *qoderRateLimitRepoStub) SetOverloaded(context.Context, int64, time.Time) error {
	panic("unexpected SetOverloaded call")
}
