package selection

import (
	context "context"
	fmt "fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Probe 连接已绑定的原生选择器与账号测试，保留平台分发及 Gemini 兜底。
type Probe struct {
	AccountTest     AccountTester
	gatewaySvc      *Generic
	openAIGateway   *Compatible
	geminiCompatSvc *Gemini
}

// AccountTester 只执行已选账号的原生测试，不持有管理聚合服务。
type AccountTester interface {
	RunTestBackgroundWithPromptAndUserAgent(context.Context, int64, string, string, string) (*account.ScheduledTestResult, error)
}

func (s Probe) Select(ctx context.Context, due routing.GroupAvailabilityProbeDueGroup, model string) (int64, error) {
	account, err := s.selectProbeAccount(ctx, due, model)
	if err != nil {
		return 0, err
	}
	return account.Record.ID, nil
}
func (s Probe) Test(ctx context.Context, id int64, model, prompt, userAgent string) (*routing.ProbeExecutionResult, error) {
	result, err := s.AccountTest.RunTestBackgroundWithPromptAndUserAgent(ctx, id, model, prompt, userAgent)
	if result == nil {
		return nil, err
	}
	return &routing.ProbeExecutionResult{Status: result.Status, LatencyMs: result.LatencyMs, ErrorMessage: result.ErrorMessage, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt}, err
}
func (s Probe) selectProbeAccount(ctx context.Context, due routing.GroupAvailabilityProbeDueGroup, modelID string) (*gatewayprovider.ExecutionAccount, error) {
	groupID := due.GroupID
	switch due.Platform {
	case capability.PlatformOpenAI:
		if s.openAIGateway == nil {
			return nil, fmt.Errorf("openai gateway service not configured")
		}
		return s.openAIGateway.SelectAccountForModel(ctx, &groupID, "", modelID)
	case capability.PlatformGemini:
		if s.geminiCompatSvc != nil {
			return s.geminiCompatSvc.SelectAccountForModel(ctx, &groupID, "", modelID)
		}
		if s.gatewaySvc != nil {
			return s.gatewaySvc.SelectAccountForModel(ctx, &groupID, "", modelID)
		}
		return nil, fmt.Errorf("gemini gateway service not configured")
	default:
		if s.gatewaySvc == nil {
			return nil, fmt.Errorf("gateway service not configured")
		}
		return s.gatewaySvc.SelectAccountForModel(ctx, &groupID, "", modelID)
	}
}

// NewProbe 固定平台选择与账号测试端口，不构造额外测试服务或调度状态。
func NewProbe(test AccountTester, generic *Generic, compatible *Compatible, gemini *Gemini) Probe {
	return Probe{AccountTest: test, gatewaySvc: generic, openAIGateway: compatible, geminiCompatSvc: gemini}
}
