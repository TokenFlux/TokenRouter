package provider

import (
	"context"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// AntigravityAccountTest 保留静态凭据分流，原生重试和额度规则通过既有探测端口执行。
type AntigravityAccountTest struct {
	Gemini    *GeminiAccountTest
	Anthropic *AnthropicAccountTest
	Probe     func(context.Context, *account.Record, account.PreparedTestRequest) (*antigravity.TestConnectionResult, error)
}

func (s *AntigravityAccountTest) Target(value *account.Record) account.TestTarget {
	return antigravityTestTarget{executor: s, record: value}
}

type antigravityTestTarget struct {
	executor *AntigravityAccountTest
	record   *account.Record
}

func (t antigravityTestTarget) Information() account.TestTargetInfo {
	return account.TestTargetInfo{AccountSnapshot: t.record.RoutingSnapshot(), APIProtocol: (account.ProtocolTarget{Record: t.record}).GetAPIProtocol()}
}
func (t antigravityTestTarget) Execute(ctx context.Context, request account.PreparedTestRequest, sink account.TestEventSink) error {
	image, explicit := account.AccountTestTypeFromArgs(request.TestType)
	if t.record.Type == account.AccountTypeAPIKey {
		if (explicit && image == account.AccountTestTypeImage) || strings.HasPrefix(strings.ToLower(request.Model), "gemini-") {
			return t.executor.Gemini.Target(t.record).Execute(ctx, request, sink)
		}
		return t.executor.Anthropic.Target(t.record).Execute(ctx, request, sink)
	}
	run := NewTestRun(ctx, make(http.Header), sink)
	defer run.Cancel()
	if explicit && image == account.AccountTestTypeImage {
		return run.Result((TestStreamOutput{}).Error(run, "Image tests are not supported for this Antigravity account type"))
	}
	if request.Model == "" {
		request.Model = "claude-sonnet-4-5"
	}
	if t.executor.Probe == nil {
		return run.Result((TestStreamOutput{}).Error(run, "Antigravity gateway service not configured"))
	}
	run.Begin(true)
	(TestStreamOutput{}).SendEvent(run, account.TestEvent{Type: "test_start", Model: request.Model})
	result, err := t.executor.Probe(run.Context, t.record, request)
	if err != nil {
		return run.Result((TestStreamOutput{}).Error(run, err.Error()))
	}
	if result.Text != "" {
		(TestStreamOutput{}).SendEvent(run, account.TestEvent{Type: "content", Text: result.Text})
	}
	(TestStreamOutput{}).SendEvent(run, account.TestEvent{Type: "test_complete", Success: true})
	return run.Result(nil)
}
