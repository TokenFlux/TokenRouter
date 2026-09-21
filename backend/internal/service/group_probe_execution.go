// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	fmt "fmt"

	"github.com/TokenFlux/TokenRouter/internal/account"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// GroupProbeExecution 保留尚未迁出的平台选择/测试执行，S07/S09 继续拆分。
type GroupProbeExecution struct {
	AccountTest     GroupAccountTester
	gatewaySvc      *GatewayService
	openAIGateway   *OpenAIGatewayService
	geminiCompatSvc *GeminiMessagesCompatService
}

// GroupAccountTester 只执行已选账号的原生测试，不持有旧管理聚合服务。
type GroupAccountTester interface {
	RunTestBackgroundWithPromptAndUserAgent(context.Context, int64, string, string, string) (*account.ScheduledTestResult, error)
}

func NewGroupProbeExecution(test GroupAccountTester, gateway *GatewayService, openai *OpenAIGatewayService, gemini *GeminiMessagesCompatService) GroupProbeExecution {
	return GroupProbeExecution{AccountTest: test, gatewaySvc: gateway, openAIGateway: openai, geminiCompatSvc: gemini}
}
func (s GroupProbeExecution) Select(ctx context.Context, due routing.GroupAvailabilityProbeDueGroup, model string) (int64, error) {
	account, err := s.selectProbeAccount(ctx, due, model)
	if err != nil {
		return 0, err
	}
	return account.ID, nil
}
func (s GroupProbeExecution) Test(ctx context.Context, id int64, model, prompt, userAgent string) (*routing.ProbeExecutionResult, error) {
	result, err := s.AccountTest.RunTestBackgroundWithPromptAndUserAgent(ctx, id, model, prompt, userAgent)
	if result == nil {
		return nil, err
	}
	return &routing.ProbeExecutionResult{Status: result.Status, LatencyMs: result.LatencyMs, ErrorMessage: result.ErrorMessage, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt}, err
}
func (s GroupProbeExecution) selectProbeAccount(ctx context.Context, due routing.GroupAvailabilityProbeDueGroup, modelID string) (*Account, error) {
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
