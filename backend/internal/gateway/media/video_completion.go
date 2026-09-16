// 公共视频完成规则独立于 HTTP、原生平台客户端和资金提交。
package media

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/tidwall/gjson"
)

// VideoCompletion 只包含完成计费所需观测，不携带账号或用户实体。
type VideoCompletion struct {
	RequestID, ResponseID, Model, BillingModel, UpstreamModel, VideoResolution string
	ImageCount, VideoCount, VideoDurationSeconds                               int
	Duration                                                                   time.Duration
}

// VideoNotice 由入口按原日志级别与字段记录；核心不安装日志后端。
type VideoNotice struct {
	AccountID       int64
	Kind            string
	TaskID          string
	DurationSeconds int
	Err             error
}
type VideoObserver interface{ ObserveVideo(VideoNotice) }

func videoNotice(o VideoObserver, n VideoNotice) {
	if o != nil {
		o.ObserveVideo(n)
	}
}
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// PrepareCompletion 先读快照再领取；无足够观测不消耗领取权。
func (s *VideoTasks) PrepareCompletion(ctx context.Context, userID, keyID int64, taskRequestID string, statusResult *VideoCompletion, now func() time.Time, observer VideoObserver) *VideoCompletion {
	if now == nil {
		now = time.Now
	}
	if statusResult == nil || statusResult.VideoCount <= 0 {
		return nil
	}
	taskRequestID = firstNonEmpty(taskRequestID, statusResult.ResponseID)
	if taskRequestID == "" {
		return nil
	}
	pending, err := s.LoadGrokVideoPendingBilling(ctx, taskRequestID, userID, keyID)
	if err != nil {
		videoNotice(observer, VideoNotice{Kind: "load_failed", TaskID: taskRequestID, Err: err})
	}
	if pending == nil {
		if statusResult.VideoDurationSeconds <= 0 {
			videoNotice(observer, VideoNotice{Kind: "missing_pending", TaskID: taskRequestID})
			return nil
		}
		videoNotice(observer, VideoNotice{Kind: "without_pending", TaskID: taskRequestID, DurationSeconds: statusResult.VideoDurationSeconds})
	}
	claimed, err := s.ClaimGrokVideoBilling(ctx, taskRequestID, userID, keyID)
	if err != nil {
		videoNotice(observer, VideoNotice{Kind: "claim_failed", TaskID: taskRequestID, Err: err})
		return nil
	}
	if !claimed {
		videoNotice(observer, VideoNotice{Kind: "already_claimed", TaskID: taskRequestID})
		return nil
	}
	// 合并创建快照：分辨率只来自请求，模型和时长仅用于补齐状态响应缺失值。
	merged := *statusResult
	if pending != nil {
		if strings.TrimSpace(merged.Model) == "" {
			merged.Model = firstNonEmpty(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if strings.TrimSpace(merged.BillingModel) == "" {
			merged.BillingModel = firstNonEmpty(pending.BillingModel, pending.Model, merged.Model)
		}
		if strings.TrimSpace(merged.UpstreamModel) == "" {
			merged.UpstreamModel = pending.UpstreamModel
		}
		// 官方状态不返回分辨率，始终优先采用创建请求值。
		if strings.TrimSpace(pending.VideoResolution) != "" {
			merged.VideoResolution = pending.VideoResolution
		}
		if merged.VideoDurationSeconds <= 0 {
			merged.VideoDurationSeconds = pending.VideoDurationSeconds
		}
		if strings.TrimSpace(merged.ResponseID) == "" {
			merged.ResponseID = taskRequestID
		}
	}
	if strings.TrimSpace(merged.Model) == "" {
		merged.Model = "grok-imagine-video"
	}
	if strings.TrimSpace(merged.BillingModel) == "" {
		merged.BillingModel = merged.Model
	}
	// 强制使用任务级持久 ID，使 usage_billing_dedup 能覆盖多次轮询和请求上下文局部 ID。
	merged.RequestID = StableGrokVideoBillingRequestID(firstNonEmpty(merged.ResponseID, taskRequestID))
	merged.ResponseID = firstNonEmpty(merged.ResponseID, taskRequestID)
	merged.VideoCount = 1
	// 纯视频结算不保留旧 ImageCount，避免误入图片计价分支。
	merged.ImageCount = 0
	// 创建请求省略分辨率时使用官方默认 480p。
	merged.VideoResolution = pricing.NormalizeVideoBillingResolutionOrDefault(merged.VideoResolution)
	// 状态和创建请求均未提供时长时使用官方默认 8 秒。
	merged.VideoDurationSeconds = pricing.NormalizeVideoBillingDurationSecondsOrDefault(merged.VideoDurationSeconds)
	// 异步视频耗时从创建受理计算到首次发现完成，不能只记录单次轮询的短耗时。
	if pending != nil {
		if e2e := GrokVideoE2EDuration(pending.CreatedAt, now()); e2e > 0 {
			merged.Duration = e2e
		}
	}

	return &merged
}

// IsGrokVideoStatusBillable 匹配官方成功条件：status 为 done 且 video.url 非空。
// pending、expired、failed 或缺少视频地址的 done 状态均不可计费。
func IsGrokVideoStatusBillable(statusBody []byte) bool {
	if len(statusBody) == 0 || !gjson.ValidBytes(statusBody) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(gjson.GetBytes(statusBody, "status").String()), "done") {
		return false
	}
	return strings.TrimSpace(gjson.GetBytes(statusBody, "video.url").String()) != ""
}

// ExtractGrokVideoBillingFromStatusBody 根据官方 done 状态构建用量单位。
// 字段优先级遵循官方文档：时长取 video.duration（秒），模型取顶层 model。
//   - 分辨率：状态响应不提供，依次回退到创建时待计费快照和默认 480p
func ExtractGrokVideoBillingFromStatusBody(statusBody []byte, pending *GrokVideoPendingBilling, requestID, observedResponseID string) *VideoCompletion {
	if !IsGrokVideoStatusBillable(statusBody) {
		return nil
	}
	model := ""
	billingModel := ""
	upstreamModel := ""
	resolution := ""
	durationSeconds := 0

	if gjson.ValidBytes(statusBody) {
		// 官方模型字段位于顶层。
		model = strings.TrimSpace(gjson.GetBytes(statusBody, "model").String())
		// 官方时长字段为 video.duration，单位是秒。
		if v := gjson.GetBytes(statusBody, "video.duration"); v.Exists() && v.Type == gjson.Number {
			durationSeconds = int(v.Int())
			if durationSeconds == 0 && v.Float() > 0 {
				// 此 API 通常不会返回不足一秒的值，仍接受上方截断后的整数结果。
				durationSeconds = int(v.Float())
			}
		}
	}
	if pending != nil {
		if model == "" {
			model = firstNonEmpty(pending.BillingModel, pending.Model, pending.OriginalModel)
		}
		if billingModel == "" {
			billingModel = firstNonEmpty(pending.BillingModel, pending.Model)
		}
		if upstreamModel == "" {
			upstreamModel = pending.UpstreamModel
		}
		// 官方状态不含分辨率，因此存在创建请求值时始终采用该值。
		resolution = pending.VideoResolution
		if durationSeconds <= 0 {
			durationSeconds = pending.VideoDurationSeconds
		}
	}
	if model == "" {
		// 状态省略模型时使用官方默认视频模型族。
		model = "grok-imagine-video"
	}
	if billingModel == "" {
		billingModel = model
	}
	// 文档约定分辨率仅来自请求；为空时由处理器应用官方默认值 480p。
	if resolution != "" {
		resolution = pricing.NormalizeVideoBillingResolutionOrDefault(resolution)
	}
	if durationSeconds > 0 {
		durationSeconds = pricing.NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds)
	}
	responseID := observedResponseID
	if responseID == "" {
		responseID = strings.TrimSpace(requestID)
	}
	return &VideoCompletion{

		ResponseID: responseID,

		Model: model,

		BillingModel: billingModel,

		UpstreamModel: upstreamModel,

		VideoCount: 1,

		VideoResolution: resolution,

		VideoDurationSeconds: durationSeconds,
	}
}
