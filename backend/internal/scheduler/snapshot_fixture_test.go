//go:build unit

package scheduler

import (
	"context"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type snapshotTestAccount struct {
	ID          int64
	Name        string
	Platform    string
	Status      string
	Schedulable bool
	GroupIDs    []int64
}

func (a snapshotTestAccount) SnapshotMetadata() SnapshotMetadata {
	return SnapshotMetadata{ID: a.ID, Name: a.Name, Platform: a.Platform, GroupIDs: slices.Clone(a.GroupIDs)}
}
func snapshotTestData(value SnapshotAccount) *snapshotTestAccount {
	switch v := value.(type) {
	case snapshotTestAccount:
		return &v
	case *snapshotTestAccount:
		return v
	default:
		panic("unexpected snapshot fixture")
	}
}

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)
const PlatformAnthropic = capability.PlatformAnthropic
const PlatformOpenAI = capability.PlatformOpenAI
const PlatformGemini = capability.PlatformGemini
const PlatformAntigravity = capability.PlatformAntigravity
const PlatformQoder = capability.PlatformQoder
const PlatformGrok = capability.PlatformGrok

// ptrInt64 保留原测试的可选分组输入。
func ptrInt64(value int64) *int64 { return &value }

// retirementAccountSource 保留原平台夹具的过滤与可控数据库屏障。
type retirementAccountSource struct {
	SnapshotAccountSource
	accounts         []SnapshotAccount
	listPlatformFunc func(context.Context, string) ([]SnapshotAccount, error)
}

func (r *retirementAccountSource) ListSchedulableByPlatform(ctx context.Context, platform string) ([]SnapshotAccount, error) {
	if r.listPlatformFunc != nil {
		return r.listPlatformFunc(ctx, platform)
	}
	var out []SnapshotAccount
	for _, a := range r.accounts {
		if a.SnapshotMetadata().Platform == platform {
			out = append(out, a)
		}
	}
	return out, nil
}
func (r *retirementAccountSource) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]SnapshotAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}
func (r *retirementAccountSource) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]SnapshotAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}
func (r *retirementAccountSource) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]SnapshotAccount, error) {
	var out []SnapshotAccount
	for _, a := range r.accounts {
		for _, platform := range platforms {
			if a.SnapshotMetadata().Platform == platform {
				out = append(out, a)
				break
			}
		}
	}
	return out, nil
}
func (r *retirementAccountSource) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, _ int64, platforms []string) ([]SnapshotAccount, error) {
	return r.ListSchedulableByPlatforms(ctx, platforms)
}
func (r *retirementAccountSource) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]SnapshotAccount, error) {
	return r.ListSchedulableByPlatforms(ctx, platforms)
}
