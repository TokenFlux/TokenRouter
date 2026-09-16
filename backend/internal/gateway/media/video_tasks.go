// 视频任务拥有归属、创建快照和完成认领；缓存协议由 session Adapter 唯一实现。
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

const VideoPendingTTL = 24 * time.Hour
const VideoClaimTTL = 48 * time.Hour
const grokMediaVideoRequestOwnerSource = "grok_video_request"

// VideoOptions 仅投影绑定有效期，不传递完整应用配置。
type VideoOptions struct{ StickyTTL time.Duration }

// VideoTasks 无本地状态，复用唯一 session 存储。
type VideoTasks struct {
	owners  session.GatewayCache
	billing session.GrokVideoBillingCache
	options VideoOptions
}

func NewVideoTasks(owners session.GatewayCache, billing session.GrokVideoBillingCache, options VideoOptions) *VideoTasks {
	return &VideoTasks{owners: owners, billing: billing, options: options}
}
func videoSessionHash(seed string) string {
	current, _ := scheduler.DeriveSessionHashes(seed)
	return current
}
func videoSessionCacheKey(hash string) string {
	if strings.TrimSpace(hash) == "" {
		return ""
	}
	return "openai:" + strings.TrimSpace(hash)
}
func derefGroupID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// GrokVideoPendingBilling 是创建任务时保存的快照，用于状态轮询首次发现已完成视频地址时计费。
// 状态响应可能省略模型或时长，此时先回退到该快照，再回退到默认值。
type GrokVideoPendingBilling struct {
	Model                string `json:"model"`
	BillingModel         string `json:"billing_model,omitempty"`
	UpstreamModel        string `json:"upstream_model,omitempty"`
	VideoResolution      string `json:"video_resolution,omitempty"`
	VideoDurationSeconds int    `json:"video_duration_seconds,omitempty"`
	OriginalModel        string `json:"original_model,omitempty"`
	// CreatedAt 是网关接受异步创建请求的时间，采用 RFC3339Nano UTC 格式。
	// 延迟计费的 duration_ms 从该时刻计算到首次观测到官方 done 和 video.url，
	// 观测来源可以是状态轮询或内容下载，而不是仅计算单次发现请求的耗时。
	CreatedAt string `json:"created_at,omitempty"`
}

func GrokMediaVideoRequestSessionHash(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	ownerSeed := fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
	return "grok-video:" + videoSessionHash(ownerSeed)
}

func (s *VideoTasks) BindGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID, accountID int64,
) error {
	if s == nil || s.owners == nil {
		return fmt.Errorf("grok video request binding cache is unavailable")
	}
	sessionHash := GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID)
	cacheKey := videoSessionCacheKey(sessionHash)
	if cacheKey == "" || accountID <= 0 {
		return fmt.Errorf("grok video request binding is invalid")
	}
	// 视频任务可能在 WebSocket 粘性 TTL（默认一小时）之后才完成。
	// 绑定时间至少覆盖待计费快照，确保较晚的状态或内容轮询仍可解析账号。
	ttl := VideoPendingTTL
	if s.options.StickyTTL > 0 {
		if sticky := s.options.StickyTTL; sticky > ttl {
			ttl = sticky
		}
	}
	if err := s.owners.SetSessionAccountID(ctx, derefGroupID(groupID), cacheKey, accountID, ttl); err != nil {
		return err
	}
	if groupID == nil || *groupID <= 0 {
		return nil
	}
	written, err := s.owners.SetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash, *groupID, ttl)
	if err != nil {
		return err
	}
	if !written {
		ownerGroupID, getErr := s.owners.GetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash)
		if getErr != nil {
			return getErr
		}
		if ownerGroupID != *groupID {
			return fmt.Errorf("grok video request binding belongs to another group")
		}
		return s.owners.RefreshSessionOwnerTTL(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash, ttl)
	}
	return nil
}

// ResolveGrokMediaVideoRequestGroup 返回创建视频任务时保存的分组归属。
func (s *VideoTasks) ResolveGrokMediaVideoRequestGroup(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	if s == nil || s.owners == nil {
		return 0, fmt.Errorf("grok video request binding cache is unavailable")
	}
	sessionHash := GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID)
	if sessionHash == "" {
		return 0, fmt.Errorf("grok video request binding is invalid")
	}
	return s.owners.GetSessionOwnerGroupID(ctx, userID, grokMediaVideoRequestOwnerSource, sessionHash)
}

func (s *VideoTasks) ResolveGrokMediaVideoRequestAccount(
	ctx context.Context,
	groupID *int64,
	requestID string,
	userID, apiKeyID int64,
) (int64, error) {
	if s == nil || s.owners == nil {
		return 0, fmt.Errorf("grok video request binding cache is unavailable")
	}
	cacheKey := videoSessionCacheKey(GrokMediaVideoRequestSessionHash(requestID, userID, apiKeyID))
	if cacheKey == "" {
		return 0, fmt.Errorf("grok video request binding is invalid")
	}
	return s.owners.GetSessionAccountID(ctx, derefGroupID(groupID), cacheKey)
}

// GrokVideoPendingCreatedAtNow 为待计费记录生成任务受理时间戳。
func GrokVideoPendingCreatedAtNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// GrokVideoE2EDuration 返回从任务受理到发现完成的实际经过时间。
// CreatedAt 缺失或无法解析时返回零，由调用方保留仅轮询耗时。
func GrokVideoE2EDuration(createdAt string, discoveredAt time.Time) time.Duration {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return 0
	}
	if discoveredAt.IsZero() {
		discoveredAt = time.Now()
	}
	var created time.Time
	var err error
	if created, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		if created, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return 0
		}
	}
	if created.IsZero() {
		return 0
	}
	d := discoveredAt.Sub(created)
	if d < 0 {
		return 0
	}
	return d
}

func VideoPendingKey(requestID string, userID, apiKeyID int64) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || userID <= 0 || apiKeyID <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d:%s", userID, apiKeyID, requestID)
}

// StoreGrokVideoPendingBilling 持久化创建时计费参数，供状态轮询延迟计费。
func (s *VideoTasks) StoreGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
	pending GrokVideoPendingBilling,
) error {
	if s == nil || s.owners == nil {
		return fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := VideoPendingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video pending billing key is invalid")
	}
	pending.Model = strings.TrimSpace(pending.Model)
	pending.BillingModel = strings.TrimSpace(pending.BillingModel)
	pending.UpstreamModel = strings.TrimSpace(pending.UpstreamModel)
	pending.OriginalModel = strings.TrimSpace(pending.OriginalModel)
	if pending.VideoResolution != "" {
		pending.VideoResolution = pricing.NormalizeVideoBillingResolutionOrDefault(pending.VideoResolution)
	}
	if pending.VideoDurationSeconds > 0 {
		pending.VideoDurationSeconds = pricing.NormalizeVideoBillingDurationSecondsOrDefault(pending.VideoDurationSeconds)
	}
	// 缺少任务受理时间时始终补写，确保延迟计费的 duration_ms 为端到端耗时。
	if strings.TrimSpace(pending.CreatedAt) == "" {
		pending.CreatedAt = GrokVideoPendingCreatedAtNow()
	} else {
		pending.CreatedAt = strings.TrimSpace(pending.CreatedAt)
	}
	payload, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	cache := s.billing
	if cache == nil {
		return fmt.Errorf("grok video pending billing cache is unavailable")
	}
	return cache.SetGrokVideoPendingBilling(ctx, key, payload, VideoPendingTTL)
}

// LoadGrokVideoPendingBilling 返回创建时快照，未命中时可能为 nil。
func (s *VideoTasks) LoadGrokVideoPendingBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (*GrokVideoPendingBilling, error) {
	if s == nil || s.owners == nil {
		return nil, fmt.Errorf("grok video pending billing cache is unavailable")
	}
	key := VideoPendingKey(requestID, userID, apiKeyID)
	if key == "" {
		return nil, fmt.Errorf("grok video pending billing key is invalid")
	}
	cache := s.billing
	if cache == nil {
		return nil, fmt.Errorf("grok video pending billing cache is unavailable")
	}
	payload, err := cache.GetGrokVideoPendingBilling(ctx, key)
	if err != nil || len(payload) == 0 {
		return nil, err
	}
	var pending GrokVideoPendingBilling
	if err := json.Unmarshal(payload, &pending); err != nil {
		return nil, err
	}
	return &pending, nil
}

// ClaimGrokVideoBilling 对已完成视频请求仅返回一次 true，避免状态轮询重复计费。
// 采用失败关闭策略，领取失败时按已计费处理。
func (s *VideoTasks) ClaimGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) (bool, error) {
	if s == nil || s.owners == nil {
		return false, fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := VideoPendingKey(requestID, userID, apiKeyID)
	if key == "" {
		return false, fmt.Errorf("grok video billing claim key is invalid")
	}
	cache := s.billing
	if cache == nil {
		return false, fmt.Errorf("grok video billing claim cache is unavailable")
	}
	return cache.ClaimGrokVideoBilled(ctx, key, VideoClaimTTL)
}

// ReleaseGrokVideoBilling 在持久化 RecordUsage 失败后释放领取记录，
// 使后续状态或内容轮询能够重试计费。
func (s *VideoTasks) ReleaseGrokVideoBilling(
	ctx context.Context,
	requestID string,
	userID, apiKeyID int64,
) error {
	if s == nil || s.owners == nil {
		return fmt.Errorf("grok video billing claim cache is unavailable")
	}
	key := VideoPendingKey(requestID, userID, apiKeyID)
	if key == "" {
		return fmt.Errorf("grok video billing claim key is invalid")
	}
	cache := s.billing
	if cache == nil {
		return fmt.Errorf("grok video billing claim cache is unavailable")
	}
	return cache.ReleaseGrokVideoBilled(ctx, key)
}

// StableGrokVideoBillingRequestID 为单个异步视频任务生成持久化 usage_logs 去重键，
// 该键并非每次轮询各自的网关请求 ID。
func StableGrokVideoBillingRequestID(taskRequestID string) string {
	taskRequestID = strings.TrimSpace(taskRequestID)
	if taskRequestID == "" {
		return ""
	}
	if strings.HasPrefix(taskRequestID, "grok-video:") {
		return taskRequestID
	}
	return "grok-video:" + taskRequestID
}

// TrackCreated 保留创建后的尽力绑定与一次快照重试；失败不能把已提交响应改成失败。
func (s *VideoTasks) TrackCreated(ctx context.Context, groupID *int64, taskID string, userID, keyID, accountID int64, pending GrokVideoPendingBilling, observer VideoObserver) {
	if err := s.BindGrokMediaVideoRequestAccount(ctx, groupID, taskID, userID, keyID, accountID); err != nil {
		videoNotice(observer, VideoNotice{Kind: "bind_failed", TaskID: taskID, AccountID: accountID, Err: err})
	}
	if err := s.StoreGrokVideoPendingBilling(ctx, taskID, userID, keyID, pending); err != nil {
		videoNotice(observer, VideoNotice{Kind: "store_retry", TaskID: taskID, AccountID: accountID, Err: err})
		if retryErr := s.StoreGrokVideoPendingBilling(ctx, taskID, userID, keyID, pending); retryErr != nil {
			videoNotice(observer, VideoNotice{Kind: "store_failed", TaskID: taskID, AccountID: accountID, Err: retryErr})
		}
	}
}

// VideoBinding 只投影认证快照内的分组关系，不包含分组策略或凭据。
type VideoBinding struct {
	GroupID  int64
	Platform string
	Present  bool
}
type VideoOwner struct {
	GroupID, AccountID int64
	BindingIndex       int
}

// ResolveCompositeVideo 先读取创建时归属，再兼容历史任务的当前映射扫描顺序。
func (s *VideoTasks) ResolveCompositeVideo(ctx context.Context, taskID string, userID, keyID int64, bindings []VideoBinding) (VideoOwner, error) {
	var lookupErr error
	ownerID, err := s.ResolveGrokMediaVideoRequestGroup(ctx, taskID, userID, keyID)
	if err == nil && ownerID > 0 {
		accountID, accountErr := s.ResolveGrokMediaVideoRequestAccount(ctx, &ownerID, taskID, userID, keyID)
		if accountErr == nil && accountID > 0 {
			index := -1
			for i, binding := range bindings {
				if binding.GroupID == ownerID && binding.Present && binding.Platform == "grok" {
					index = i
					break
				}
			}
			return VideoOwner{GroupID: ownerID, AccountID: accountID, BindingIndex: index}, nil
		}
		lookupErr = accountErr
	} else if err != nil {
		lookupErr = err
	}
	for i, binding := range bindings {
		if !binding.Present || binding.Platform != "grok" {
			continue
		}
		id := binding.GroupID
		accountID, err := s.ResolveGrokMediaVideoRequestAccount(ctx, &id, taskID, userID, keyID)
		if err != nil {
			lookupErr = err
			continue
		}
		if accountID <= 0 {
			continue
		}
		return VideoOwner{GroupID: id, AccountID: accountID, BindingIndex: i}, nil
	}
	if lookupErr == nil {
		lookupErr = fmt.Errorf("grok video request binding not found")
	}
	return VideoOwner{}, lookupErr
}
