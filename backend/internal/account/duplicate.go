// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const maxAccountNameRunes = 100

const duplicateAccountOperationIDExtraKey = "duplicate_operation_id"

func duplicateAccountName(sourceName string) string {
	const suffix = " (Copy)"
	nameRunes := []rune(strings.TrimSpace(sourceName))
	maxBaseRunes := maxAccountNameRunes - len([]rune(suffix))
	if len(nameRunes) > maxBaseRunes {
		nameRunes = nameRunes[:maxBaseRunes]
	}
	return string(nameRunes) + suffix
}

func cloneAccountJSONMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	cloned := make(map[string]any, len(value))
	if err := json.Unmarshal(payload, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

var duplicateAccountDiscardedExtraKeys = map[string]struct{}{
	// 重试标识只属于创建当前复制件的操作，不得传递给后续复制件。
	duplicateAccountOperationIDExtraKey: {},
	// 外部同步标识只属于一个本地账号。
	"crs_account_id": {},
	"crs_kind":       {},
	"crs_synced_at":  {},
	// 本地配额用量与派生窗口时间必须从空状态开始。
	"quota_used":            {},
	"quota_daily_used":      {},
	"quota_weekly_used":     {},
	"quota_daily_start":     {},
	"quota_weekly_start":    {},
	"quota_daily_reset_at":  {},
	"quota_weekly_reset_at": {},
	// Codex 收敛 seed 由系统按账号生成，复制账号时必须重新生成。
	CodexFingerprintSeedExtraKey: {},
	// 上游观测、能力探测与临时调度状态不属于可复制配置。
	"model_rate_limits":                      {},
	"session_window_utilization":             {},
	"passive_usage_7d_utilization":           {},
	"passive_usage_7d_reset":                 {},
	"passive_usage_7d_oi_utilization":        {},
	"passive_usage_7d_oi_reset":              {},
	"passive_usage_sampled_at":               {},
	"grok_usage_snapshot":                    {},
	"grok_billing_snapshot":                  {},
	"qoder_quota_snapshot":                   {},
	"qoder_quota_updated_at":                 {},
	CNUsageMonitorSnapshotExtraKey:           {},
	"antigravity_credits_overages":           {},
	"antigravity_force_token_refresh":        {},
	"antigravity_force_token_refresh_at":     {},
	"antigravity_force_token_refresh_reason": {},
	"drive_storage_limit":                    {},
	"drive_storage_usage":                    {},
	"drive_tier_updated_at":                  {},
	"codex_primary_used_percent":             {},
	"codex_primary_reset_after_seconds":      {},
	"codex_primary_window_minutes":           {},
	"codex_secondary_used_percent":           {},
	"codex_secondary_reset_after_seconds":    {},
	"codex_secondary_window_minutes":         {},
	"codex_primary_over_secondary_percent":   {},
	"codex_usage_updated_at":                 {},
	"codex_5h_used_percent":                  {},
	"codex_5h_reset_after_seconds":           {},
	"codex_5h_window_minutes":                {},
	"codex_5h_reset_at":                      {},
	"codex_7d_used_percent":                  {},
	"codex_7d_reset_after_seconds":           {},
	"codex_7d_window_minutes":                {},
	"codex_7d_reset_at":                      {},
	"upstream_billing_probe":                 {},
	"upstream_billing_probe_enabled":         {},
}

func duplicateAccountExtra(value map[string]any) (map[string]any, error) {
	cloned, err := cloneAccountJSONMap(value)
	if err != nil {
		return nil, err
	}
	for key := range duplicateAccountDiscardedExtraKeys {
		delete(cloned, key)
	}
	return cloned, nil
}

func canDuplicateAccountType(accountType string) bool {
	switch accountType {
	case AccountTypeAPIKey, AccountTypeUpstream, AccountTypeBedrock, AccountTypeServiceAccount:
		return true
	default:
		return false
	}
}

func duplicateAccountGroups(source *Record) ([]GroupMembership, []int64) {
	if len(source.AccountGroups) > 0 {
		groups := make([]GroupMembership, 0, len(source.AccountGroups))
		groupIDs := make([]int64, 0, len(source.AccountGroups))
		for _, sourceGroup := range source.AccountGroups {
			groups = append(groups, GroupMembership{GroupID: sourceGroup.GroupID})
			groupIDs = append(groupIDs, sourceGroup.GroupID)
		}
		return groups, groupIDs
	}

	groups := make([]GroupMembership, 0, len(source.GroupIDs))
	groupIDs := append([]int64(nil), source.GroupIDs...)
	for _, groupID := range groupIDs {
		groups = append(groups, GroupMembership{GroupID: groupID})
	}
	return groups, groupIDs
}

func duplicateAccountOperationID(sourceID int64, actorScope, operationKey string) string {
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return ""
	}
	actorScope = strings.TrimSpace(actorScope)
	if actorScope == "" {
		actorScope = "admin:0"
	}
	payload := "admin.accounts.duplicate\x00" + actorScope + "\x00" + strconv.FormatInt(sourceID, 10) + "\x00" + operationKey
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", digest)
}

func (s *Admin) findDuplicateByOperationID(ctx context.Context, operationID string) (*Record, error) {
	if operationID == "" {
		return nil, nil
	}
	accounts, err := s.accountRepo.FindByExtraField(ctx, duplicateAccountOperationIDExtraKey, operationID)
	if err != nil {
		return nil, fmt.Errorf("find duplicate account operation: %w", err)
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	account := accounts[0]
	return &account, nil
}

// RecoverDuplicateAccount 只读查找已提交的复制件。
// 当幂等协调器无法确认响应是否成功持久化时用于恢复，且绝不重复执行创建副作用。
func (s *Admin) RecoverDuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Record, error) {
	return s.findDuplicateByOperationID(ctx, duplicateAccountOperationID(id, actorScope, operationKey))
}

func cloneAccountValuePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// DuplicateAccount 从源配置创建已暂停调度的账号，不携带一级运行态字段。
// 凭据与 Extra 配置会深拷贝，避免新账号规范化时修改内存中的源账号。
// 链接型凭据影子账号不持有凭据，必须继续通过 CreateShadow 创建，因此不允许复制。
func (s *Admin) DuplicateAccount(ctx context.Context, id int64, actorScope, operationKey string) (*Record, error) {
	operationID := duplicateAccountOperationID(id, actorScope, operationKey)
	existing, err := s.RecoverDuplicateAccount(ctx, id, actorScope, operationKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	source, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if source.IsCredentialShadow() {
		return nil, infraerrors.BadRequest(
			"ACCOUNT_DUPLICATE_SHADOW_UNSUPPORTED",
			"linked credential shadow accounts cannot be duplicated; duplicate the parent account instead",
		)
	}
	if !canDuplicateAccountType(source.Type) {
		return nil, infraerrors.BadRequest(
			"ACCOUNT_DUPLICATE_CREDENTIAL_TYPE_UNSUPPORTED",
			"accounts with rotating or unsupported credential types cannot be duplicated",
		)
	}

	credentials, err := cloneAccountJSONMap(source.Credentials)
	if err != nil {
		return nil, fmt.Errorf("clone account credentials: %w", err)
	}
	extra, err := duplicateAccountExtra(source.Extra)
	if err != nil {
		return nil, fmt.Errorf("clone account extra configuration: %w", err)
	}
	if operationID != "" {
		if extra == nil {
			extra = make(map[string]any, 1)
		}
		extra[duplicateAccountOperationIDExtraKey] = operationID
	}

	var expiresAt *int64
	if source.ExpiresAt != nil {
		unix := source.ExpiresAt.Unix()
		expiresAt = &unix
	}
	autoPauseOnExpired := source.AutoPauseOnExpired
	groups, groupIDs := duplicateAccountGroups(source)
	proxyID := source.ProxyID
	if source.ProxyFallbackOriginID != nil {
		// 代理回退是临时运行态；复制时使用原始配置的代理。
		proxyID = source.ProxyFallbackOriginID
	}
	input := &CreateAccountInput{
		Name:                  duplicateAccountName(source.Name),
		Notes:                 cloneAccountValuePointer(source.Notes),
		Platform:              source.Platform,
		Type:                  source.Type,
		Credentials:           credentials,
		Extra:                 extra,
		ProxyID:               cloneAccountValuePointer(proxyID),
		Concurrency:           source.Concurrency,
		Priority:              source.Priority,
		RateMultiplier:        cloneAccountValuePointer(source.RateMultiplier),
		LoadFactor:            cloneAccountValuePointer(source.LoadFactor),
		GroupIDs:              groupIDs,
		ExpiresAt:             expiresAt,
		AutoPauseOnExpired:    &autoPauseOnExpired,
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	}
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	duplicate, err := BuildAccountForCreate(input, input.Extra, s.options.Creation)
	if err != nil {
		return nil, err
	}
	// 复制的凭据必须经管理员检查后，才能与源账号共享线上流量。
	duplicate.Schedulable = false
	if s.options.Duplicates == nil {
		return nil, errors.New("account duplicate repository is not configured")
	}
	if err := s.options.Duplicates.CreateWithAccountGroups(ctx, duplicate, groups); err != nil {
		return nil, fmt.Errorf("create duplicate account: %w", err)
	}
	for i := range groups {
		groups[i].AccountID = duplicate.ID
	}
	duplicate.AccountGroups = groups
	duplicate.GroupIDs = groupIDs
	return duplicate, nil
}
