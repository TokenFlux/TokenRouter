package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAICountTarget 只允许读取选择快照和执行计数，不交付账号凭据。
type OpenAICountTarget interface {
	Snapshot() account.AccountSnapshot
	ForwardCount(context.Context, *gin.Context, []byte, string) error
}

// InputTokensTarget 保留单次尝试的原生预检与重试预算。
type InputTokensTarget interface {
	Snapshot() account.AccountSnapshot
	RetryLimit() int
	ForwardInputTokens(context.Context, *gin.Context, []byte) error
}

type InputTokensSelection struct {
	Target  InputTokensTarget
	Release func()
}

// OpenAITokenExecution 是计数所需的受控执行能力；不包含资金写入或完成提交。
type OpenAITokenExecution interface {
	PlanTokenRoute(context.Context, *apikey.APIKey, string) routing.RoutePlan
	TokenSessionHash(*gin.Context, []byte) string
	SelectCount(context.Context, *int64, string, string, string) (OpenAICountTarget, error)
	SelectInputTokens(context.Context, *int64, string, string, string, map[int64]struct{}, string) (InputTokensSelection, error)
}

type TokenFunding interface {
	CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
}

// OpenAITokenPorts 固定绑定只读用例和共享实例，每个请求单独构造尝试状态。
type OpenAITokenPorts struct {
	Diagnoser           routing.ModelAvailabilityDiagnoser
	ResolvedDiagnoser   routing.ModelAvailabilityDiagnoser
	Execution           OpenAITokenExecution
	Funding             TokenFunding
	MissingDependencies []string
	Rules               ErrorRuleMatcher
}

func (p OpenAITokenPorts) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	return apikey.CopyAPIKey(key), ok
}

func (p OpenAITokenPorts) Dependencies(c *gin.Context, log *zap.Logger) bool {
	if len(p.MissingDependencies) == 0 {
		return true
	}
	if log == nil {
		log = RequestLogger(c, "handler.openai_gateway.responses")
	}
	log.Error("openai.handler_dependencies_missing", zap.Strings("missing_dependencies", p.MissingDependencies))
	if c != nil && c.Writer != nil && !c.Writer.Written() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "Service temporarily unavailable"}})
	}
	return false
}

func (OpenAITokenPorts) AllowsMessages(key *apikey.APIKey) bool {
	key = apikey.CopyAPIKey(key)
	return key == nil || key.Group == nil || key.Group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
}

func (OpenAITokenPorts) PolicyDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

func (OpenAITokenPorts) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}

func (OpenAITokenPorts) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}

func (p OpenAITokenPorts) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return p.Execution.PlanTokenRoute(ctx, apikey.CopyAPIKey(key), model)
}

func (OpenAITokenPorts) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
}

func (OpenAITokenPorts) MessageAccountModel(ctx context.Context, key *apikey.APIKey, model string) string {
	return ResolveOpenAIMessagesAccountLayerModelForRequest(ctx, apikey.CopyAPIKey(key), model)
}

func (OpenAITokenPorts) MappedBodyCache(body []byte) func(bool, string) []byte {
	return requeststate.NewModelMappedBodyCache(body, protocolopenai.ReplaceModelInBody)
}

func (p OpenAITokenPorts) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	keyCopy := apikey.CopyAPIKey(key)
	return p.Funding.CheckKey(ctx, keyCopy, sub, admission.QuotaPlatform(ctx, keyCopy), false)
}

func (OpenAITokenPorts) Platform(key *apikey.APIKey) string {
	return OpenAICompatibleRequestPlatform(apikey.CopyAPIKey(key))
}

func (p OpenAITokenPorts) SessionHash(c *gin.Context, _ OpenAISessionInput, body []byte) string {
	return p.Execution.TokenSessionHash(c, body)
}

func (OpenAITokenPorts) AuthLatency(c *gin.Context, ms int64) {
	SetOpsLatencyMs(c, OpsAuthLatencyMsKey, ms)
}

// tokenSelectionError 保留模型不支持与容量不足的独立观测口径。
func tokenSelectionError(c *gin.Context, diagnose routing.ModelAvailabilityDiagnoser, key *apikey.APIKey, routingModel, displayModel string) SelectionErrorResponse {
	var group *int64
	if key != nil {
		group = key.GroupID
	}
	result := ClassifySelectionError(c.Request.Context(), diagnose, group, routingModel, displayModel, OpenAICompatibleRequestPlatform(key))
	if result.ModelNotFound {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
	}
	return result
}

// OpenAICompatibleSelectionErrorForLog 保持 Grok 计数选择日志的原平台替换。
func OpenAICompatibleSelectionErrorForLog(err error, platform string) error {
	if err == nil || platform != "grok" {
		return err
	}
	message := strings.ReplaceAll(err.Error(), "OpenAI accounts", "Grok accounts")
	if message == err.Error() {
		return err
	}
	return fmt.Errorf("%s", message)
}
