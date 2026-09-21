// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	errors "errors"
	fmt "fmt"
	strings "strings"

	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
)

func ApplyArchiveDefaults(item *transfer.DataAccount, defaults *transfer.OpenAIOAuthImportDefaults) {
	if defaults == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(item.Platform), PlatformOpenAI) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(item.Type), AccountTypeOAuth) {
		return
	}

	if !item.NotesSet && defaults.Account.Notes != nil {
		item.Notes = clonePointer(defaults.Account.Notes)
	}
	if !item.ConcurrencySet && defaults.Account.Concurrency != nil {
		item.Concurrency = clonePointer(defaults.Account.Concurrency)
	}
	if !item.PrioritySet && defaults.Account.Priority != nil {
		item.Priority = clonePointer(defaults.Account.Priority)
	}
	if !item.RateMultiplierSet && defaults.Account.RateMultiplier != nil {
		item.RateMultiplier = clonePointer(defaults.Account.RateMultiplier)
	}
	if !item.ExpiresAtSet && defaults.Account.ExpiresAt != nil {
		item.ExpiresAt = clonePointer(defaults.Account.ExpiresAt)
	}
	if !item.AutoPauseOnExpiredSet && defaults.Account.AutoPauseOnExpired != nil {
		item.AutoPauseOnExpired = clonePointer(defaults.Account.AutoPauseOnExpired)
	}

	mergeArchiveDefaults(&item.Credentials, defaults.Credentials)
	mergeArchiveDefaults(&item.Extra, defaults.Extra)
}
func mergeArchiveDefaults(target *map[string]any, defaults map[string]any) {
	if len(defaults) == 0 {
		return
	}
	if *target == nil {
		*target = map[string]any{}
	}
	for key, value := range defaults {
		// 只按顶层键做缺失合并；null、false、0、空数组都算已提供。
		if _, exists := (*target)[key]; !exists {
			(*target)[key] = CloneValues(map[string]any{key: value})[key]
		}
	}
}
func ValidateArchiveAccount(item transfer.DataAccount) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("account name is required")
	}
	if strings.TrimSpace(item.Platform) == "" {
		return errors.New("account platform is required")
	}
	if strings.TrimSpace(item.Type) == "" {
		return errors.New("account type is required")
	}
	if len(item.Credentials) == 0 {
		return errors.New("account credentials is required")
	}
	switch item.Type {
	case AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeUpstream,
		AccountTypeBedrock, AccountTypeServiceAccount, AccountTypeCosy:
	default:
		return fmt.Errorf("account type is invalid: %s", item.Type)
	}
	platform := strings.ToLower(strings.TrimSpace(item.Platform))
	if platform == PlatformQoder && item.Type != AccountTypeCosy {
		return fmt.Errorf("qoder accounts require %s account type", AccountTypeCosy)
	}
	if platform != PlatformQoder && item.Type == AccountTypeCosy {
		return fmt.Errorf("%s account type requires %s platform", AccountTypeCosy, PlatformQoder)
	}
	if item.RateMultiplier != nil && *item.RateMultiplier < 0 {
		return errors.New("rate_multiplier must be >= 0")
	}
	if item.Concurrency != nil && *item.Concurrency < 0 {
		return errors.New("concurrency must be >= 0")
	}
	if item.Priority != nil && *item.Priority < 0 {
		return errors.New("priority must be >= 0")
	}
	return nil
}

// ArchiveIdentityHints 是供应商解码后的最小投影，不能用于认证或授权。
type ArchiveIdentityHints struct{ Email, PlanType, ChatGPTAccountID, ChatGPTUserID, OrganizationID string }

// ArchiveIDToken 只选择原 OpenAI OAuth 导入的可选身份提示，不扩展其它导入入口。
func ArchiveIDToken(item *transfer.DataAccount) string {
	if item == nil || item.Credentials == nil || strings.ToLower(strings.TrimSpace(item.Platform)) != PlatformOpenAI || strings.ToLower(strings.TrimSpace(item.Type)) != AccountTypeOAuth {
		return ""
	}
	token, _ := item.Credentials["id_token"].(string)
	if strings.TrimSpace(token) == "" {
		return ""
	}
	return token
}

// FillArchiveIdentity 只填原先缺失的字符串，不覆盖显式账号信息或增加 token 验证策略。
func FillArchiveIdentity(item *transfer.DataAccount, hints *ArchiveIdentityHints) {
	if item == nil || hints == nil || item.Credentials == nil {
		return
	}
	set := func(key, value string) {
		if value == "" {
			return
		}
		if existing, _ := item.Credentials[key].(string); existing == "" {
			item.Credentials[key] = value
		}
	}
	set("email", hints.Email)
	set("plan_type", hints.PlanType)
	set("chatgpt_account_id", hints.ChatGPTAccountID)
	set("chatgpt_user_id", hints.ChatGPTUserID)
	set("organization_id", hints.OrganizationID)
}
