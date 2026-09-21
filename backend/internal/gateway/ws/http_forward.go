// HTTP 入站选择 WS 传输后的同账号恢复由此唯一拥有，不创建连接池或账号切换循环。
package ws

import (
	"context"
	"fmt"
	"strings"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// HTTPForwardInput 固化当前请求的模型、图片计费投影与恢复上限。
type HTTPForwardInput struct {
	AccountID                                        int64
	AccountType, UpstreamModel, BillingModel         string
	ImageBillingModel, ImageSizeTier, ImageInputSize string
	LineageEntryBody                                 []byte
	Stream                                           bool
	RetryLimit, IDLogLimit                           int
}

// HTTPForwardPort 仅执行一次平台尝试或一次明确状态操作；不接受旧实体。
type HTTPForwardPort interface {
	Execute(context.Context, map[string]any, int, string, *bool) (*ForwardResult, error)
	OutputCommitted() bool
	AgentTaskRecovered(error) bool
	ClassifyError(error) (string, bool)
	PayloadString(map[string]any, string) string
	EncryptedDigests([]byte) []string
	MarkEncrypted([]byte, []string)
	TruncateID(string, int) string
	NormalizeLog(string) string
	ClassifyPrevious(string) string
	RetryBudget() time.Duration
	RetryBackoff(int) time.Duration
	RecordExhausted()
	RecordRetry(time.Duration)
	RecordNonRetryable()
	FallbackError(string, error) error
	WriteFailure(error)
	Debug(string)
	Info(string)
}

// RunHTTPForward 保持 previous-response、失效密文和 Agent Identity 的独立一次恢复标记。
func RunHTTPForward(ctx context.Context, wsReqBody map[string]any, in HTTPForwardInput, p HTTPForwardPort) (*ForwardResult, error) {

	_, hasPreviousResponseID := wsReqBody["previous_response_id"]
	p.Debug(fmt.Sprintf(
		"forward_start account_id=%d account_type=%s model=%s stream=%v has_previous_response_id=%v",
		in.AccountID,
		in.AccountType,
		in.UpstreamModel,
		in.Stream,
		hasPreviousResponseID,
	))
	maxAttempts := in.RetryLimit + 1
	wsAttempts := 0
	var wsResult *ForwardResult
	var wsErr error
	wsLastFailureReason := ""
	agentTaskRecoveryTried := false
	wsPrevResponseRecoveryTried := false
	wsInvalidEncryptedContentRecoveryTried := false
	recoverPrevResponseNotFound := func(attempt int) bool {
		if wsPrevResponseRecoveryTried {
			return false
		}
		previousResponseID := p.PayloadString(wsReqBody, "previous_response_id")
		if previousResponseID == "" {
			p.Info(fmt.Sprintf(
				"reconnect_prev_response_recovery_skip account_id=%d attempt=%d reason=missing_previous_response_id previous_response_id_present=false",
				in.AccountID,
				attempt,
			))
			return false
		}
		if wire.HasFunctionCallOutput(wsReqBody) {
			p.Info(fmt.Sprintf(
				"reconnect_prev_response_recovery_skip account_id=%d attempt=%d reason=has_function_call_output previous_response_id_present=true",
				in.AccountID,
				attempt,
			))
			return false
		}
		delete(wsReqBody, "previous_response_id")
		wsPrevResponseRecoveryTried = true
		p.Info(fmt.Sprintf(
			"reconnect_prev_response_recovery account_id=%d attempt=%d action=drop_previous_response_id retry=1 previous_response_id=%s previous_response_id_kind=%s",
			in.AccountID,
			attempt,
			p.TruncateID(previousResponseID, in.IDLogLimit),
			p.NormalizeLog(p.ClassifyPrevious(previousResponseID)),
		))
		return true
	}
	recoverInvalidEncryptedContent := func(attempt int) bool {
		if wsInvalidEncryptedContentRecoveryTried {
			return false
		}
		// 写入 lineage 后，同一失效密文在后续 turn 进场时被预剥离，不再重复
		// 触发上游拒绝与重连。摘要取自进场形态的 body（密文项只可能来自
		// 客户端进场请求，重复摘要幂等）。
		invalidDigests := p.EncryptedDigests(in.LineageEntryBody)
		removedReasoningItems := wire.TrimEncryptedReasoningItems(wsReqBody)
		if !removedReasoningItems {
			p.Info(fmt.Sprintf(
				"reconnect_invalid_encrypted_content_recovery_skip account_id=%d attempt=%d reason=missing_encrypted_state_items",
				in.AccountID,
				attempt,
			))
			return false
		}
		if len(invalidDigests) > 0 {
			p.MarkEncrypted(in.LineageEntryBody, invalidDigests)
		}
		previousResponseID := p.PayloadString(wsReqBody, "previous_response_id")
		hasFunctionCallOutput := wire.HasFunctionCallOutput(wsReqBody)
		if previousResponseID != "" && !hasFunctionCallOutput {
			delete(wsReqBody, "previous_response_id")
		}
		wsInvalidEncryptedContentRecoveryTried = true
		p.Info(fmt.Sprintf(
			"reconnect_invalid_encrypted_content_recovery account_id=%d attempt=%d action=drop_encrypted_state_items retry=1 previous_response_id_present=%v previous_response_id=%s previous_response_id_kind=%s has_function_call_output=%v dropped_previous_response_id=%v",
			in.AccountID,
			attempt,
			previousResponseID != "",
			p.TruncateID(previousResponseID, in.IDLogLimit),
			p.NormalizeLog(p.ClassifyPrevious(previousResponseID)),
			hasFunctionCallOutput,
			previousResponseID != "" && !hasFunctionCallOutput,
		))
		return true
	}
	retryBudget := p.RetryBudget()
	retryStartedAt := time.Now()
wsRetryLoop:
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		wsAttempts = attempt
		wsResult, wsErr = p.Execute(ctx, wsReqBody, attempt, wsLastFailureReason, &agentTaskRecoveryTried)
		if wsErr == nil {
			break
		}
		if p.OutputCommitted() {
			break
		}
		if p.AgentTaskRecovered(wsErr) {
			continue
		}

		reason, retryable := p.ClassifyError(wsErr)
		if reason != "" {
			wsLastFailureReason = reason
		}

		if reason == "previous_response_not_found" && recoverPrevResponseNotFound(attempt) {
			continue
		}
		if reason == "invalid_encrypted_content" && recoverInvalidEncryptedContent(attempt) {
			continue
		}
		if retryable && attempt < maxAttempts {
			backoff := p.RetryBackoff(attempt)
			if retryBudget > 0 && time.Since(retryStartedAt)+backoff > retryBudget {
				p.RecordExhausted()
				p.Info(fmt.Sprintf(
					"reconnect_budget_exhausted account_id=%d attempts=%d max_retries=%d reason=%s elapsed_ms=%d budget_ms=%d",
					in.AccountID,
					attempt,
					in.RetryLimit,
					p.NormalizeLog(reason),
					time.Since(retryStartedAt).Milliseconds(),
					retryBudget.Milliseconds(),
				))
				break
			}
			p.RecordRetry(backoff)
			p.Info(fmt.Sprintf(
				"reconnect_retry account_id=%d retry=%d max_retries=%d reason=%s backoff_ms=%d",
				in.AccountID,
				attempt,
				in.RetryLimit,
				p.NormalizeLog(reason),
				backoff.Milliseconds(),
			))
			if backoff > 0 {
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					wsErr = p.FallbackError("retry_backoff_canceled", ctx.Err())
					break wsRetryLoop
				case <-timer.C:
				}
			}
			continue
		}
		if retryable {
			p.RecordExhausted()
			p.Info(fmt.Sprintf(
				"reconnect_exhausted account_id=%d attempts=%d max_retries=%d reason=%s",
				in.AccountID,
				attempt,
				in.RetryLimit,
				p.NormalizeLog(reason),
			))
		} else if reason != "" {
			p.RecordNonRetryable()
			p.Info(fmt.Sprintf(
				"reconnect_stop account_id=%d attempt=%d reason=%s",
				in.AccountID,
				attempt,
				p.NormalizeLog(reason),
			))
		}
		break
	}
	if wsErr == nil {
		firstTokenMs := int64(0)
		hasFirstTokenMs := wsResult != nil && wsResult.FirstTokenMs != nil
		if hasFirstTokenMs {
			firstTokenMs = int64(*wsResult.FirstTokenMs)
		}
		requestID := ""
		if wsResult != nil {
			requestID = strings.TrimSpace(wsResult.RequestID)
		}
		p.Debug(fmt.Sprintf(
			"forward_succeeded account_id=%d request_id=%s stream=%v has_first_token_ms=%v first_token_ms=%d ws_attempts=%d",
			in.AccountID,
			requestID,
			in.Stream,
			hasFirstTokenMs,
			firstTokenMs,
			wsAttempts,
		))
		wsResult.UpstreamModel = in.UpstreamModel

		if wsResult.BillingModel == "" {
			wsResult.BillingModel = in.BillingModel
		}
		if wsResult.ImageCount > 0 {
			wsResult.ImageSize = in.ImageSizeTier
			wsResult.ImageInputSize = in.ImageInputSize
			wsResult.BillingModel = in.ImageBillingModel
		}
		return wsResult, nil
	}
	p.WriteFailure(wsErr)
	return nil, wsErr
}
