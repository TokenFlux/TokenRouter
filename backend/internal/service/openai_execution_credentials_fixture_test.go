//go:build unit

package service

import (
	"context"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

func withOpenAIExecutionCredentialsForTest(s *OpenAIGatewayService, tokens ...*account.GrokTokenSource) *OpenAIGatewayService {
	s.BindAgentIdentity(gatewayprovider.NewExecutionAgentIdentity(&agentTaskCoordinatorForTest, s.accountRepo, registerAgentTaskForTest, s.Connections.InvalidateAccount))
	if s.executionCredentials == nil {
		s.executionCredentials = &account.OpenAIExecutionCredentials{}
	}
	if s.accountRepo != nil {
		s.executionCredentials.Parent = func(ctx context.Context, id int64) (*account.Record, error) {
			value, err := s.accountRepo.GetByID(ctx, id)
			return gatewayprovider.ExecutionRecord(value), err
		}
	}
	if s.requestCredentials == nil {
		s.requestCredentials = gatewaytestkit.RequestCredentials(s.accountRepo, s.executionCredentials, nil, s.runtimeBlockState())
	}
	s.requestCredentials.Source = s.executionCredentials
	if len(tokens) > 0 && tokens[0] != nil {
		s.executionCredentials.Grok = tokens[0].GetAccessToken
		s.requestCredentials.HasGrokTokenSource = true
		s.requestCredentials.Recovery.Invalidate = tokens[0].InvalidateToken
	}

	if s.Requests != nil {
		s.Requests.Identity = s.agentIdentity
		s.Requests.Credentials = s.executionCredentials
	}
	if s.Text != nil {
		s.Text.Credentials = s.requestCredentials
	}
	return s
}

// 原并发测试显式共享同一个真实协调器；测试进程之外没有默认实例。
var agentTaskCoordinatorForTest account.OpenAITaskCoordinator

// 本地协议夹具的地址覆盖只存在于测试构建，生产注册地址由 app 明确传入。
var openAIAgentIdentityAuthAPIBaseURL = "https://auth.openai.com/api/accounts"

func registerAgentTaskForTest(ctx context.Context, value *account.Record) (string, error) {
	return accountprovider.RegisterAgentIdentityTask(ctx, value, openAIAgentIdentityAuthAPIBaseURL)
}
