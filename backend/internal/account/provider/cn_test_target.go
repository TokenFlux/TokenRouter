package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// Target 固定本次账号快照，适配器只根据已经准备好的测试路由执行。
func (s *CNAccountTest) Target(value *account.Record) account.TestTarget {
	return cnTestTarget{executor: s, record: value}
}

type cnTestTarget struct {
	executor *CNAccountTest
	record   *account.Record
}

func (t cnTestTarget) Information() account.TestTargetInfo {
	return account.TestTargetInfo{AccountSnapshot: t.record.RoutingSnapshot(), APIProtocol: (account.ProtocolTarget{Record: t.record}).GetAPIProtocol()}
}

func (t cnTestTarget) Execute(ctx context.Context, request account.PreparedTestRequest, sink account.TestEventSink) error {
	run := NewTestRun(ctx, make(http.Header), sink)
	run.Automatic = request.Automatic
	run.Headers.Set("User-Agent", request.UserAgent)
	defer run.Cancel()
	var err error
	switch request.Route {
	case account.TestRouteCNAdaptive:
		err = t.executor.ExecuteAdaptive(run, t.record, request.Model, request.Prompt)
	case account.TestRouteCNResponses:
		err = t.executor.Responses.Execute(run, t.record, request.Model, request.Prompt, request.Mode, request.TestType)
	default:
		err = t.executor.Execute(run, t.record, request.Model, request.Prompt)
	}
	return run.Result(err)
}
