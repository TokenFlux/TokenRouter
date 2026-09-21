package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// Target 将本次请求状态与账号记录隔离，不向公开测试信息暴露凭据。
func (s *OpenAIAccountTest) Target(value *account.Record) account.TestTarget {
	return openaiTestTarget{executor: s, record: value}
}

type openaiTestTarget struct {
	executor *OpenAIAccountTest
	record   *account.Record
}

func (t openaiTestTarget) Information() account.TestTargetInfo {
	return account.TestTargetInfo{AccountSnapshot: t.record.RoutingSnapshot(), APIProtocol: (account.ProtocolTarget{Record: t.record}).GetAPIProtocol()}
}

func (t openaiTestTarget) Execute(ctx context.Context, request account.PreparedTestRequest, sink account.TestEventSink) error {
	headers := make(http.Header)
	headers.Set("User-Agent", request.UserAgent)
	headers.Set("originator", request.Originator)
	run := NewTestRun(ctx, headers, sink)
	defer run.Cancel()
	run.Automatic = request.Automatic
	run.RequestedProtocol = account.TextProtocol(request.Protocol)
	if t.executor.Prepare != nil {
		if err := t.executor.Prepare(run, t.record); err != nil {
			return run.Result((TestStreamOutput{}).Error(run, err.Error()))
		}
	}
	return run.Result(t.executor.Execute(run, t.record, request.Model, request.Prompt, request.Mode, request.TestType))
}
