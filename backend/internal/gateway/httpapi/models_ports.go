package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// ModelsPorts 只取得目录与已选模型资源，不暴露完整执行器或账号凭据。
type ModelsPorts struct {
	Catalogue interface {
		ResolveRequestableModels(context.Context, *int64, string) routing.RequestableModelsResult
	}
	ReadAccess       func(*gin.Context) (*apikey.APIKey, bool)
	ReadPlatform     func(*gin.Context) (string, bool)
	ReadBilling      func(*gin.Context) (*billing.APIKeyBillingContext, bool)
	SelectModel      func(context.Context, *int64) (GeminiModelReader, error)
	CheckAntigravity func(context.Context, *int64) (bool, error)
	SafeSegment      func(string) bool
}

// Access 保留有效 Key 优先和请求独立副本，认证失败投影不进入此入口。
func (p ModelsPorts) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := p.ReadAccess(c)
	return apikey.CopyAPIKey(key), ok
}

func (p ModelsPorts) ForcedPlatform(c *gin.Context) (string, bool) {
	return p.ReadPlatform(c)
}

func (p ModelsPorts) Available() bool { return p.Catalogue != nil }

func (p ModelsPorts) Resolve(ctx context.Context, id *int64, platform string) routing.RequestableModelsResult {
	return p.Catalogue.ResolveRequestableModels(ctx, id, platform)
}

// PreferredSubscription 仅为目录展示选择已确认的指定订阅，保持原资格边界。
func (p ModelsPorts) PreferredSubscription(c *gin.Context) (*billing.UserSubscription, bool) {
	value, ok := p.ReadBilling(c)
	if !ok || value == nil || value.Mode != apikey.APIKeyBillingModeSubscription || value.Subscription == nil {
		return nil, false
	}
	return value.Subscription, true
}

func (p ModelsPorts) SelectGemini(ctx context.Context, id *int64) (GeminiModelReader, error) {
	return p.SelectModel(ctx, id)
}

func (p ModelsPorts) HasAntigravity(ctx context.Context, id *int64) (bool, error) {
	return p.CheckAntigravity(ctx, id)
}

func (p ModelsPorts) CapacityLimited(c *gin.Context, err error) {
	MarkOpsRoutingCapacityLimitedIfNoAvailable(c, err)
}

func (p ModelsPorts) SafeModelSegment(model string) bool { return p.SafeSegment(model) }
