// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

func (s *Admin) CreateAccount(ctx context.Context, input *CreateAccountInput) (*Record, error) {
	accountExtra := maps.Clone(input.Extra)
	DiscardDeprecatedAccountExtra(accountExtra)
	if err := NormalizeUpstreamUsageExtra(accountExtra); err != nil {
		return nil, err
	}
	accountExtra, err := NormalizeGrokMediaEligibilityExtra(input.Platform, accountExtra)
	if err != nil {
		return nil, err
	}
	if err := ValidateUpstreamRequestIDHeaderExtra(accountExtra); err != nil {
		return nil, err
	}

	// 绑定分组
	groupIDs := input.GroupIDs
	// 如果没有指定分组,自动绑定对应平台的默认分组
	if len(groupIDs) == 0 && !input.SkipDefaultGroupBind {
		defaultGroup, err := s.defaultGroup(ctx, input.Platform)
		if err == nil && defaultGroup != nil {
			groupIDs = []int64{defaultGroup.ID}
		}
	}

	// 检查混合渠道风险（除非用户已确认）
	if len(groupIDs) > 0 && !input.SkipMixedChannelCheck {
		if err := s.checkMixedChannelRisk(ctx, 0, input.Platform, groupIDs); err != nil {
			return nil, err
		}
	}

	// 校验并规范化请求头覆写配置（header 名小写化、格式检查）
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	// OAuth 兑换后不得持久化临时 SSO 或密码。
	input.Credentials = SanitizeStoredCredentials(input.Platform, input.Credentials)

	account, err := BuildAccountForCreate(input, accountExtra, s.options.Creation)
	if err != nil {
		return nil, err
	}
	// 只有新建账号需要生成并持久化机器身份；编辑旧账号时必须保留兼容回退语义。
	if account.IsQoderCosy() {
		s.options.Credentials.Prepare(account)
	}
	s.attachProxyForValidation(ctx, account)
	if err := s.options.Credentials.Validate(ctx, account); err != nil {
		return nil, err
	}
	if err := s.accountRepo.Create(ctx, account); err != nil {
		return nil, err
	}

	// 绑定分组
	if len(groupIDs) > 0 {
		if err := s.accountRepo.BindGroups(ctx, account.ID, groupIDs); err != nil {
			return nil, err
		}
	}

	// 后置任务使用自己的账号值，不与返回给 HTTP 的可变对象共享。
	if account.Type == AccountTypeOAuth && (account.Platform == PlatformOpenAI || account.Platform == PlatformAntigravity) {
		value := CloneRecord(account)
		s.options.Background("service/admin_account.go:CreateAccount", func() {
			defer func() {
				if r := recover(); r != nil {
					event := "create_account_openai_privacy_panic"
					if value.Platform == PlatformAntigravity {
						event = "create_account_antigravity_privacy_panic"
					}
					s.options.Error(event, "account_id", value.ID, "recover", r)
				}
			}()
			if value.Platform == PlatformOpenAI {
				s.options.Privacy.EnsureOpenAIPrivacy(context.Background(), value)
			} else {
				s.options.Privacy.EnsureAntigravityPrivacy(context.Background(), value)
			}
		})
	}

	return account, nil
}

// checkMixedChannelRisk 检查分组中是否存在混合渠道（Antigravity + Anthropic）
// 如果存在混合，返回错误提示用户确认
func (s *Admin) checkMixedChannelRisk(ctx context.Context, currentAccountID int64, currentAccountPlatform string, groupIDs []int64) error {
	// 判断当前账号的渠道类型（基于 platform 字段，而不是 type 字段）
	currentPlatform := getAccountPlatform(currentAccountPlatform)
	if currentPlatform == "" {
		// 不是 Antigravity 或 Anthropic，无需检查
		return nil
	}

	// 检查每个分组中的其他账号
	for _, groupID := range groupIDs {
		accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
		if err != nil {
			return fmt.Errorf("get accounts in group %d: %w", groupID, err)
		}

		// 检查是否存在不同渠道的账号
		for _, account := range accounts {
			if currentAccountID > 0 && account.ID == currentAccountID {
				continue // 跳过当前账号
			}

			otherPlatform := getAccountPlatform(account.Platform)
			if otherPlatform == "" {
				continue // 不是 Antigravity 或 Anthropic，跳过
			}

			// 检测混合渠道
			if currentPlatform != otherPlatform {
				group, _ := s.groupReference(ctx, groupID)
				groupName := fmt.Sprintf("Group %d", groupID)
				if group != nil {
					groupName = group.Name
				}

				return &MixedChannelError{
					GroupID:         groupID,
					GroupName:       groupName,
					CurrentPlatform: currentPlatform,
					OtherPlatform:   otherPlatform,
				}
			}
		}
	}

	return nil
}

// CheckMixedChannelRisk checks whether target groups contain mixed channels for the current account platform.
func (s *Admin) CheckMixedChannelRisk(ctx context.Context, currentAccountID int64, currentAccountPlatform string, groupIDs []int64) error {
	return s.checkMixedChannelRisk(ctx, currentAccountID, currentAccountPlatform, groupIDs)
}

// getAccountPlatform 根据账号 platform 判断混合渠道检查用的平台标识
func getAccountPlatform(accountPlatform string) string {
	switch strings.ToLower(strings.TrimSpace(accountPlatform)) {
	case PlatformAntigravity:
		return "Antigravity"
	case PlatformAnthropic, "claude":
		return "Anthropic"
	default:
		return ""
	}
}

// MixedChannelError 混合渠道错误
type MixedChannelError struct {
	GroupID         int64
	GroupName       string
	CurrentPlatform string
	OtherPlatform   string
}

func (e *MixedChannelError) Error() string {
	return fmt.Sprintf("mixed_channel_warning: Group '%s' contains both %s and %s accounts. Using mixed channels in the same context may cause thinking block signature validation issues, which will fallback to non-thinking mode for historical messages.",
		e.GroupName, e.CurrentPlatform, e.OtherPlatform)
}
func (s *Admin) defaultGroup(ctx context.Context, platform string) (*GroupReference, error) {
	if s.options.Groups == nil {
		return nil, nil
	}
	return s.options.Groups.DefaultGroup(ctx, platform)
}
func (s *Admin) groupReference(ctx context.Context, id int64) (*GroupReference, error) {
	if s.options.Groups == nil {
		return nil, nil
	}
	return s.options.Groups.GetGroup(ctx, id)
}
func (s *Admin) attachProxyForValidation(ctx context.Context, value *Record) {
	if s.options.Proxies == nil || value == nil || value.Proxy != nil || value.ProxyID == nil || *value.ProxyID <= 0 {
		return
	}
	if proxy, err := s.options.Proxies.GetByID(ctx, *value.ProxyID); err == nil && proxy != nil {
		value.Proxy = proxy
	}
}
