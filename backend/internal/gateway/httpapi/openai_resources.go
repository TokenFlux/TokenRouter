package httpapi

import (
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// OpenAIImageAdmissionOptions 只包含静态技术参数，nil 保留未配置时的旁路。
type OpenAIImageAdmissionOptions struct {
	Enabled    bool
	Limit      int
	Wait       bool
	Timeout    time.Duration
	MaxWaiting int
}

// OpenAIHTTPResources 只引用 app 持有的唯一并发服务与本地图片限制器。
type OpenAIHTTPResources struct {
	Concurrency  *ConcurrencyHelper
	Images       *scheduler.ImageConcurrencyLimiter
	ImageOptions *OpenAIImageAdmissionOptions
}

func (r *OpenAIHTTPResources) AcquireUser(c *gin.Context, userID int64, limit int, stream bool, started *bool, log *zap.Logger) (func(), bool) {
	ctx := c.Request.Context()
	release, err := r.Concurrency.AcquireUserSlotWithWait(c, userID, limit, stream, started)
	if err != nil {
		log.Warn("openai.user_slot_acquire_failed", zap.Error(err))
		status, kind, code, message := ConcurrencyErrorResponse(err, "user")
		DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, kind, code, message, *started, false)
		return nil, false
	}
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, release), true
}

func (r *OpenAIHTTPResources) AcquireImage(c *gin.Context, started bool) (func(), bool) {
	if r == nil || r.Images == nil || r.ImageOptions == nil {
		return nil, true
	}
	options := r.ImageOptions
	release, ok := r.Images.Acquire(c.Request.Context(), options.Enabled, options.Limit, options.Wait, options.Timeout, options.MaxWaiting)
	if ok {
		return release, true
	}
	DefaultOpenAIErrorOutput().StreamError(c, http.StatusTooManyRequests, "rate_limit_error", "Image generation concurrency limit exceeded, please retry later", started)
	return nil, false
}
