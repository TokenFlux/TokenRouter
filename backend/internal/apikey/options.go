// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	context "context"
	"time"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	contact "github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"
	team "github.com/TokenFlux/TokenRouter/internal/team"
)

// APIKeyAuthCacheConfig API Key 认证缓存配置
type APIKeyAuthCacheConfig struct {
	L1Size             int                    `mapstructure:"l1_size"`
	L1TTLSeconds       int                    `mapstructure:"l1_ttl_seconds"`
	L2TTLSeconds       int                    `mapstructure:"l2_ttl_seconds"`
	NegativeTTLSeconds int                    `mapstructure:"negative_ttl_seconds"`
	JitterPercent      int                    `mapstructure:"jitter_percent"`
	Singleflight       bool                   `mapstructure:"singleflight"`
	LookupConcurrency  int                    `mapstructure:"lookup_concurrency"`
	InvalidAbuse       InvalidAuthAbuseConfig `mapstructure:"invalid_abuse"`
}

type InvalidAuthAbuseConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	Threshold     int  `mapstructure:"threshold"`
	WindowSeconds int  `mapstructure:"window_seconds"`
	BlockSeconds  int  `mapstructure:"block_seconds"`
	Capacity      int  `mapstructure:"capacity"`
}

// Options 是认证缓存、Key 生成及团队开关的启动快照。
type Options struct {
	Now             func() time.Time
	Calendar        timezone.Calendar
	APIKeyAuth      APIKeyAuthCacheConfig
	Default         struct{ APIKeyPrefix string }
	Team            struct{ Enabled bool }
	GroupFastPolicy func(string, bool) string
}
type User = identity.User
type NotifyEmailEntry = contact.Entry
type Team = team.Team
type TeamMembership = team.TeamMembership
type UserSubscription = billing.UserSubscription
type UserSubscriptionRepository interface {
	GetByID(context.Context, int64) (*UserSubscription, error)
	ListActiveByUserID(context.Context, int64) ([]UserSubscription, error)
}
type UserGroupRateRepository = billing.UserGroupRateRepository
type UserRepository interface {
	GetByID(context.Context, int64) (*User, error)
}
type GroupRepository interface {
	GetByID(context.Context, int64) (*routing.Group, error)
	GetByIDLite(context.Context, int64) (*routing.Group, error)
	ListActive(context.Context) ([]routing.Group, error)
	FindDefault(context.Context, string) (*routing.Group, error)
}
type TeamRepository interface {
	GetContextByUserID(context.Context, int64) (*team.TeamContext, error)
}
type ConcurrencyReader interface {
	GetAPIKeyConcurrencyBatch(context.Context, []int64) (map[int64]int, error)
}

const (
	StatusActive      = "active"
	PlatformAnthropic = capability.PlatformAnthropic
	PlatformOpenAI    = capability.PlatformOpenAI
	PlatformGemini    = capability.PlatformGemini
	TeamStatusActive  = team.TeamStatusActive
	TeamRoleOwner     = team.TeamRoleOwner
)

var (
	ErrUserNotFound              = identity.ErrUserNotFound
	ErrUserNotActive             = identity.ErrUserNotActive
	ErrInsufficientPerms         = identity.ErrInsufficientPerms
	ErrTeamFeatureDisabled       = team.ErrTeamFeatureDisabled
	ErrTeamMembershipRequired    = team.ErrTeamMembershipRequired
	ErrTeamNotFound              = team.ErrTeamNotFound
	ErrTeamSuspended             = team.ErrTeamSuspended
	ErrTeamMemberDailyExceeded   = billing.ErrTeamMemberDailyExceeded
	ErrTeamMemberWeeklyExceeded  = billing.ErrTeamMemberWeeklyExceeded
	ErrTeamMemberMonthlyExceeded = billing.ErrTeamMemberMonthlyExceeded
)

func cloneGroupClientProtocols(values []protocol.ProtocolID) []protocol.ProtocolID {
	if values == nil {
		return nil
	}
	out := make([]protocol.ProtocolID, len(values))
	copy(out, values)
	return out
}
func subscriptionPlanIncludesGroup(plan *billing.SubscriptionPlan, id int64) bool {
	return billing.SubscriptionAllowsGroup(&billing.UserSubscription{Plan: plan}, id)
}

// SetGroupFastPolicy 注入分组 Fast 策略的只读计算。
func (s *APIKeyService) SetGroupFastPolicy(policy func(string, bool) string) {
	s.groupFastPolicy = policy
}
func (s *APIKeyService) groupPolicy(g *routing.Group) string {
	if s.groupFastPolicy != nil {
		return s.groupFastPolicy(g.OpenAIFastPolicy, g.ForceOpenAIFast)
	}
	if s.cfg != nil && s.cfg.GroupFastPolicy != nil {
		return s.cfg.GroupFastPolicy(g.OpenAIFastPolicy, g.ForceOpenAIFast)
	}
	// 独立调用者可传入已经归一化的投影。
	return g.OpenAIFastPolicy
}
