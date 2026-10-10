package httpapi

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ModelsPorts 读取模型目录和已选目标的模型资源。
type ModelsPorts struct {
	Catalogue interface {
		ResolveRequestableModels(context.Context, *int64, string) routing.RequestableModelsResult
		ResolveSelectedModels(context.Context, *int64, string, []string) routing.RequestableModelsResult
	}
	ReadAccess       func(*gin.Context) (*apikey.APIKey, bool)
	ReadPlatform     func(*gin.Context) (string, bool)
	ReadBilling      func(*gin.Context) (*billing.APIKeyBillingContext, bool)
	SelectModel      func(context.Context, *int64) (GeminiModelReader, error)
	CheckAntigravity func(context.Context, *int64) (bool, error)
	SafeSegment      func(string) bool
}

// Access 优先读取通过认证的 Key，返回请求独立的副本。
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

// ResolveSelected 校验本次单型号查询需要的名称。
func (p ModelsPorts) ResolveSelected(ctx context.Context, id *int64, platform string, models []string) routing.RequestableModelsResult {
	return p.Catalogue.ResolveSelectedModels(ctx, id, platform, models)
}

// PreferredSubscription 为目录展示返回已确认的指定订阅。
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
