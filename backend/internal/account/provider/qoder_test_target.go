package provider

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// Target 把已读取的账号封装为一次测试句柄，公开信息不暴露凭据。
func (s *QoderAccountTest) Target(value *account.Record) account.TestTarget {
	return qoderTestTarget{executor: s, record: value}
}

type qoderTestTarget struct {
	executor *QoderAccountTest
	record   *account.Record
}

func (t qoderTestTarget) Information() account.TestTargetInfo {
	return account.TestTargetInfo{AccountSnapshot: t.record.RoutingSnapshot(), APIProtocol: (account.ProtocolTarget{Record: t.record}).GetAPIProtocol()}
}

func (t qoderTestTarget) Execute(ctx context.Context, request account.PreparedTestRequest, sink account.TestEventSink) error {
	headers := make(http.Header)
	headers.Set("User-Agent", request.UserAgent)
	headers.Set("originator", request.Originator)
	run := NewTestRun(ctx, headers, sink)
	defer run.Cancel()
	return run.Result(t.executor.Execute(run, t.record, request.Model, request.Prompt))
}
