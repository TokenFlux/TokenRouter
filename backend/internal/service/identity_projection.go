// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// IdentityUser 将旧用户投影为身份模型，关联实体不反向带入身份核心。
func IdentityUser(u *User) *identity.User {
	if u == nil {
		return nil
	}
	return &identity.User{Subscriptions: u.Subscriptions, ID: u.ID, Email: u.Email, Username: u.Username, Notes: u.Notes, AvatarURL: u.AvatarURL, AvatarSource: u.AvatarSource, AvatarMIME: u.AvatarMIME, AvatarByteSize: u.AvatarByteSize, AvatarSHA256: u.AvatarSHA256, PasswordHash: u.PasswordHash, Role: u.Role, Balance: u.Balance, FrozenBalance: u.FrozenBalance, Concurrency: u.Concurrency, Status: u.Status, AllowedGroups: u.AllowedGroups, DisabledPublicGroups: u.DisabledPublicGroups, GroupRestrictionsLoaded: u.GroupRestrictionsLoaded, TokenVersion: u.TokenVersion, TokenVersionResolved: u.TokenVersionResolved, SignupSource: u.SignupSource, LastLoginAt: u.LastLoginAt, LastActiveAt: u.LastActiveAt, LastUsedAt: u.LastUsedAt, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DeletedAt: u.DeletedAt, GroupRates: u.GroupRates, TotpSecretEncrypted: u.TotpSecretEncrypted, TotpEnabled: u.TotpEnabled, TotpEnabledAt: u.TotpEnabledAt, BalanceNotifyEnabled: u.BalanceNotifyEnabled, BalanceNotifyThresholdType: u.BalanceNotifyThresholdType, BalanceNotifyThreshold: u.BalanceNotifyThreshold, BalanceNotifyExtraEmails: u.BalanceNotifyExtraEmails, TotalRecharged: u.TotalRecharged, RPMLimit: u.RPMLimit, APIKeyLimit: u.APIKeyLimit, UserGroupRPMOverride: u.UserGroupRPMOverride}
}

// UserFromIdentity 为尚未迁移的消费者恢复旧用户形状。
func UserFromIdentity(u *identity.User) *User {
	if u == nil {
		return nil
	}
	return &User{Subscriptions: u.Subscriptions, ID: u.ID, Email: u.Email, Username: u.Username, Notes: u.Notes, AvatarURL: u.AvatarURL, AvatarSource: u.AvatarSource, AvatarMIME: u.AvatarMIME, AvatarByteSize: u.AvatarByteSize, AvatarSHA256: u.AvatarSHA256, PasswordHash: u.PasswordHash, Role: u.Role, Balance: u.Balance, FrozenBalance: u.FrozenBalance, Concurrency: u.Concurrency, Status: u.Status, AllowedGroups: u.AllowedGroups, DisabledPublicGroups: u.DisabledPublicGroups, GroupRestrictionsLoaded: u.GroupRestrictionsLoaded, TokenVersion: u.TokenVersion, TokenVersionResolved: u.TokenVersionResolved, SignupSource: u.SignupSource, LastLoginAt: u.LastLoginAt, LastActiveAt: u.LastActiveAt, LastUsedAt: u.LastUsedAt, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, DeletedAt: u.DeletedAt, GroupRates: u.GroupRates, TotpSecretEncrypted: u.TotpSecretEncrypted, TotpEnabled: u.TotpEnabled, TotpEnabledAt: u.TotpEnabledAt, BalanceNotifyEnabled: u.BalanceNotifyEnabled, BalanceNotifyThresholdType: u.BalanceNotifyThresholdType, BalanceNotifyThreshold: u.BalanceNotifyThreshold, BalanceNotifyExtraEmails: u.BalanceNotifyExtraEmails, TotalRecharged: u.TotalRecharged, RPMLimit: u.RPMLimit, APIKeyLimit: u.APIKeyLimit, UserGroupRPMOverride: u.UserGroupRPMOverride}
}

// identitySessionUsers 保持旧构造器的只读用户端口，生产装配迁入 app 后退出。
type identitySessionUsers struct{ Repository UserRepository }

func (p identitySessionUsers) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	u, e := p.Repository.GetByID(ctx, id)
	return IdentityUser(u), e
}

// 安全字段写入使用原仓储事务上下文，完整用户仓储迁移后由新实现直接提供。
func (p identitySessionUsers) UpdateTotpSecret(ctx context.Context, id int64, secret *string) error {
	return p.Repository.UpdateTotpSecret(ctx, id, secret)
}
func (p identitySessionUsers) EnableTotp(ctx context.Context, id int64) error {
	return p.Repository.EnableTotp(ctx, id)
}
func (p identitySessionUsers) DisableTotp(ctx context.Context, id int64) error {
	return p.Repository.DisableTotp(ctx, id)
}

// ApplyIdentityUser 只更新身份投影字段，保留旧消费者持有的 Key 关联。
func ApplyIdentityUser(dst *User, src *identity.User) {
	if dst == nil || src == nil {
		return
	}
	keys := dst.APIKeys
	*dst = *UserFromIdentity(src)
	dst.APIKeys = keys
}

// usersFromIdentity 与 usersToIdentity 仅投影旧聚合返回形状，不拥有用户状态。
func usersFromIdentity(v []identity.User) []User {
	if v == nil {
		return nil
	}
	out := make([]User, len(v))
	for i := range v {
		out[i] = *UserFromIdentity(&v[i])
	}
	return out
}
