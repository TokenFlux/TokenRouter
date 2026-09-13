package account

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
)

// ManagedRecoveryStep 保留手动刷新原五次独立提交，禁止把恢复扩展成任意字段写入。
type ManagedRecoveryStep uint8

const (
	ManagedRecoveryError ManagedRecoveryStep = iota
	ManagedRecoveryRateLimit
	ManagedRecoveryQuotaScopes
	ManagedRecoveryModelLimits
	ManagedRecoveryTemporary
)

// ManagedRecoveryVersion 冻结交换前身份及待清理状态，只用于内部条件写入。
type ManagedRecoveryVersion struct {
	UsageObservationVersion
	ErrorMessage string         `json:"-"`
	Until        *time.Time     `json:"-"`
	Reason       string         `json:"-"`
	Extra        map[string]any `json:"-"`
}

type ManagedRecoveryWriter interface {
	ApplyManagedRecoveryStep(context.Context, ManagedRecoveryStep, ManagedRecoveryVersion) (bool, error)
}

// ManagedRecoveryUnblocker 使用原运行时代次，防止迟到恢复清除新安装的阻断。
type ManagedRecoveryUnblocker interface {
	ManagedRecoveryFence(int64) uint64
	ClearAccountSchedulingBlockIfFence(int64, uint64) bool
}

type ManagedCredentialRecovery interface {
	ClearManagedRefreshError(context.Context, *Record) (*Record, bool, error)
}

var ErrManagedRecoveryUnavailable = errors.New("managed refresh conditional recovery is not configured")

func ObserveManagedRecovery(value *Record) ManagedRecoveryVersion {
	return ManagedRecoveryVersion{UsageObservationVersion: ObserveUsageVersion(value), ErrorMessage: value.ErrorMessage,
		Until: clonePointer(value.TempUnschedulableUntil), Reason: value.TempUnschedulableReason, Extra: CloneValues(value.Extra)}
}

// Advance 只在对应提交成功后推进下一步条件，不修改调用方的账号或缓存。
func (v *ManagedRecoveryVersion) Advance(step ManagedRecoveryStep) {
	switch step {
	case ManagedRecoveryError:
		v.Status, v.ErrorMessage = StatusActive, ""
	case ManagedRecoveryRateLimit:
		v.RateLimitedAt, v.RateLimitResetAt, v.OverloadUntil = nil, nil, nil
	case ManagedRecoveryQuotaScopes:
		delete(v.Extra, "antigravity_quota_scopes")
	case ManagedRecoveryModelLimits:
		delete(v.Extra, "model_rate_limits")
	case ManagedRecoveryTemporary:
		v.Until, v.Reason = nil, ""
	}
}

func (v ManagedRecoveryVersion) Matches(value *Record) bool {
	if value == nil || !MatchesCredentialVersion(value, v.CredentialVersion) {
		return false
	}
	current := ObserveManagedRecovery(value)
	// 额外配置允许并发编辑，只比较此恢复拥有的两个键。
	for _, key := range []string{"antigravity_quota_scopes", "model_rate_limits"} {
		want, hasWant := v.Extra[key]
		got, hasGot := current.Extra[key]
		if hasWant != hasGot || !reflect.DeepEqual(want, got) {
			return false
		}
	}
	current.Extra, v.Extra = nil, nil
	return reflect.DeepEqual(current, v)
}

// ClearManagedRefreshError 沿用原独立写入与失败即停止，不撤销并发管理员的新状态。
func (s *Admin) ClearManagedRefreshError(ctx context.Context, value *Record) (*Record, bool, error) {
	if value == nil || value.Platform != PlatformAntigravity || value.Status != StatusError || !strings.Contains(value.ErrorMessage, "missing_project_id:") {
		return nil, false, nil
	}
	writer, ok := s.accountRepo.(ManagedRecoveryWriter)
	if !ok {
		return nil, false, ErrManagedRecoveryUnavailable
	}
	var blocker ManagedRecoveryUnblocker
	var fence uint64
	if s.options.RuntimeBlocker != nil {
		blocker, ok = s.options.RuntimeBlocker.(ManagedRecoveryUnblocker)
		if !ok {
			return nil, false, ErrManagedRecoveryUnavailable
		}
		fence = blocker.ManagedRecoveryFence(value.ID)
	}
	expected := ObserveManagedRecovery(value)
	for step := ManagedRecoveryError; step <= ManagedRecoveryTemporary; step++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		applied, err := writer.ApplyManagedRecoveryStep(ctx, step, expected)
		if err != nil || !applied {
			return nil, false, err
		}
		expected.Advance(step)
	}
	current, err := s.accountRepo.GetByID(ctx, value.ID)
	if err != nil || !expected.Matches(current) {
		return current, false, err
	}
	if blocker != nil && !blocker.ClearAccountSchedulingBlockIfFence(value.ID, fence) {
		return current, false, nil
	}
	return current, true, nil
}
