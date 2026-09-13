package dto

import (
	"time"

	accountdto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	usagedto "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
)

type User = identitydto.User[APIKey]

type AdminUser = identitydto.AdminUser[APIKey]

type APIKey = keydto.APIKey[Group]

type APIKeyCompositeGroup = keydto.APIKeyCompositeGroup[Group]

type Group = routingdto.Group

type GroupCapacity = routingdto.GroupCapacity

type SubscriptionPlan = billinghttpapi.SubscriptionPlan

type SubscriptionPlanGroup = billinghttpapi.SubscriptionPlanGroup

type AdminGroup = routingdto.AdminGroup[AccountGroup]

type Account = accountdto.Account

type AccountGroup = accountdto.AccountGroup

type Proxy = egresshttp.Proxy

type ProxyWithAccountCount = egresshttp.ProxyWithAccountCount

type AdminProxy = egresshttp.AdminProxy

type AdminProxyWithAccountCount = egresshttp.AdminProxyWithAccountCount

type ProxyAccountSummary = egresshttp.ProxyAccountSummary

type RedeemCode = billinghttpapi.RedeemCode

type AdminRedeemCode = billinghttpapi.AdminRedeemCode

type NullableTimeField = billinghttpapi.NullableTimeField

type BatchUpdateRedeemCodeFields = billinghttpapi.BatchUpdateRedeemCodeFields

type BatchUpdateRedeemCodesRequest = billinghttpapi.BatchUpdateRedeemCodesRequest

type UsageLog = usagedto.UsageLog

type AdminUsageLog = usagedto.AdminUsageLog

type UsageLogTiming = usagedto.UsageLogTiming

type UsageCleanupFilters = usagedto.UsageCleanupFilters

type UsageCleanupTask = usagedto.UsageCleanupTask

type AccountSummary = usagedto.AccountSummary

type Setting struct {
	ID        int64     `json:"id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserSubscription = billinghttpapi.UserSubscription

type AdminUserSubscription = billinghttpapi.AdminUserSubscription

type BulkAssignResult = billinghttpapi.BulkAssignResult

// PromoCode 注册优惠码
type PromoCode struct {
	ID          int64      `json:"id"`
	Code        string     `json:"code"`
	BonusAmount float64    `json:"bonus_amount"`
	MaxUses     int        `json:"max_uses"`
	UsedCount   int        `json:"used_count"`
	Status      string     `json:"status"`
	ExpiresAt   *time.Time `json:"expires_at"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PromoCodeUsage 优惠码使用记录
type PromoCodeUsage struct {
	ID          int64     `json:"id"`
	PromoCodeID int64     `json:"promo_code_id"`
	UserID      int64     `json:"user_id"`
	BonusAmount float64   `json:"bonus_amount"`
	UsedAt      time.Time `json:"used_at"`

	User *User `json:"user,omitempty"`
}
