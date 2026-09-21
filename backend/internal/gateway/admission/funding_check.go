package admission

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// FundingCheck 只提供已投影权益检查，不向网关暴露资金缓存实现。
type FundingCheck interface {
	Check(context.Context, billing.CheckInput) error
}

// RPMCheck 由调度模块拥有计数与覆盖规则。
type RPMCheck interface {
	Check(context.Context, *scheduler.RPMUser, *scheduler.RPMGroup) error
}

// FundingAdmission 固定绑定两个用例，保持资金错误先于 RPM 消费。
type FundingAdmission struct {
	funds  FundingCheck
	rpm    RPMCheck
	simple func() bool
}

func NewFundingAdmission(funds FundingCheck, rpm RPMCheck, simple func() bool) *FundingAdmission {
	return &FundingAdmission{funds: funds, rpm: rpm, simple: simple}
}

// Check 首先检查资金，再读取原运行模式并执行 RPM；不在资金拒绝时累计次数。
func (a *FundingAdmission) Check(ctx context.Context, input billing.CheckInput, user *scheduler.RPMUser, group *scheduler.RPMGroup) error {
	if err := a.CheckFunding(ctx, input); err != nil {
		return err
	}
	if a.simple != nil && a.simple() {
		return nil
	}
	if a.rpm == nil || user == nil {
		return nil
	}
	return a.rpm.Check(ctx, user, group)
}

// CheckFunding 用于原本只复查权益的等待后边界，不增加第二次 RPM 消费。
func (a *FundingAdmission) CheckFunding(ctx context.Context, input billing.CheckInput) error {
	return a.funds.Check(ctx, input)
}

// CheckKey 只从当前有效 Key 投影准入字段；Key 必须已完成认证及最终分组授权。
func (a *FundingAdmission) CheckKey(ctx context.Context, key *apikey.APIKey, subscription *billing.UserSubscription, platform string, afterWait bool) error {
	var payer *billing.UserSummary
	var user *scheduler.RPMUser
	if key.User != nil {
		payer = &billing.UserSummary{ID: key.User.ID}
		user = &scheduler.RPMUser{ID: key.User.ID, RPMLimit: key.User.RPMLimit, UserGroupRPMOverride: key.User.UserGroupRPMOverride}
	}
	var group *billing.GroupSnapshot
	var rpmGroup *scheduler.RPMGroup
	if key.Group != nil {
		group = &billing.GroupSnapshot{ID: key.Group.ID}
		rpmGroup = &scheduler.RPMGroup{ID: key.Group.ID, RPMLimit: key.Group.RPMLimit}
	}
	input := billing.CheckInput{
		Payer: payer,
		Key:   &billing.KeySnapshot{ID: key.ID, BillingMode: key.BillingMode, RateLimit5h: key.RateLimit5h, RateLimit1d: key.RateLimit1d, RateLimit7d: key.RateLimit7d},
		Group: group, Subscription: subscription, Platform: platform,
	}
	if afterWait {
		return a.CheckFunding(ctx, input)
	}
	return a.Check(ctx, input, user, rpmGroup)
}
