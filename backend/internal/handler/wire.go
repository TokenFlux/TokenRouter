package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// ProvideOpenAIGatewayHandler 创建并注入 Grok 媒体资格探测器。
func ProvideOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	concurrencyService *scheduler.ConcurrencyService,
	billingCacheService *admission.FundingAdmission,
	apiKeyService *apikey.APIKeyService,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	errorPassthroughService *errorpolicy.ErrorPassthroughService,
	contentModerationService *moderationcore.ContentModerationService,
	opsService *ops.OpsService,
	grokQuotaService *account.GrokQuotaService,
	cfg *config.Config,
	queue *ops.ErrorLogQueue,
	prompts *promptpolicy.Service,
	resources *gatewayhttp.OpenAIHTTPResources,
) *OpenAIGatewayHandler {
	h := NewOpenAIGatewayHandler(gatewayService, concurrencyService, billingCacheService, apiKeyService,
		usageRecordWorkerPool, errorPassthroughService, contentModerationService, opsService, cfg, prompts, resources)
	h.grokMediaEligibilityProber = grokQuotaService
	if queue != nil {
		h.opsErrorQueue = queue
	}
	return h
}
