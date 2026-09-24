package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"

	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) FirstOutputTimeout(reasoningEffort string) time.Duration {
	if p == nil || !p.Options.Configured || p.Options.OpenAIFirstOutputTimeoutSeconds <= 0 {
		return 0
	}
	seconds := p.Options.OpenAIFirstOutputTimeoutSeconds
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "high", "xhigh", "max":
		if override := p.Options.OpenAIHighEffortFirstOutputTimeoutSeconds; override > 0 {
			seconds = override
		}
	}
	return time.Duration(seconds) * time.Second
}

func (p *OpenAIResponseOutput) FirstOutputFailure(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	startTime time.Time,
	originalModel string,
	reasoningEffort string,
	timeout time.Duration,
	phase string,
	responseHeaders http.Header,
) *forwardcore.UpstreamFailoverError {
	elapsed := time.Since(startTime)
	logging.LegacyPrintf(
		"service.openai_gateway",
		"OpenAI first output timeout: account=%d model=%s effort=%s phase=%s elapsed=%s limit=%s",
		account.Record.ID, originalModel, reasoningEffort, phase, elapsed, timeout,
	)
	requestID := strings.TrimSpace(responseHeaders.Get("x-request-id"))
	AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
		Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, UpstreamRequestID: requestID,
		Kind: "first_output_timeout", Message: "OpenAI upstream produced no semantic output before the deadline",
		Detail: fmt.Sprintf("phase=%s elapsed_ms=%d timeout_ms=%d", phase, elapsed.Milliseconds(), timeout.Milliseconds()),
	})
	if p.Observer != nil {
		p.Observer.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), originalModel)

	}
	return &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusGatewayTimeout,
		ResponseBody:    []byte(`{"error":{"type":"first_output_timeout","message":"Upstream produced no output before the deadline"}}`),
		ResponseHeaders: responseHeaders.Clone(), SafeToFailoverAfterWrite: true,
	}
}
