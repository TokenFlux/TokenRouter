// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

var ErrAuthIdentityOwnershipConflict = identitypostgres.ErrAuthIdentityOwnershipConflict

var ErrAuthIdentityChannelOwnershipConflict = identitypostgres.ErrAuthIdentityChannelOwnershipConflict

var ErrAuthIdentityChannelProviderMismatch = identitypostgres.ErrAuthIdentityChannelProviderMismatch

type ProviderGrantReason = identitypostgres.ProviderGrantReason

const ProviderGrantReasonSignup = identitypostgres.ProviderGrantReasonSignup

const ProviderGrantReasonFirstBind = identitypostgres.ProviderGrantReasonFirstBind

type AuthIdentityKey = identitypostgres.AuthIdentityKey

type AuthIdentityChannelKey = identitypostgres.AuthIdentityChannelKey

type CreateAuthIdentityInput = identitypostgres.CreateAuthIdentityInput

type BindAuthIdentityInput = identitypostgres.BindAuthIdentityInput

type CreateAuthIdentityResult = identitypostgres.CreateAuthIdentityResult

type UserAuthIdentityLookup = identitypostgres.UserAuthIdentityLookup

type ProviderGrantRecordInput = identitypostgres.ProviderGrantRecordInput

type IdentityAdoptionDecisionInput = identitypostgres.IdentityAdoptionDecisionInput

// WithUserProfileIdentityTx 转接身份存储，原事务 context 原样传递。
func (r *userRepository) WithUserProfileIdentityTx(ctx context.Context, fn func(txCtx context.Context) error) error {
	return r.UserStore.WithUserProfileIdentityTx(ctx, fn)
}

// CreateAuthIdentity 转接身份存储，原事务 context 原样传递。
func (r *userRepository) CreateAuthIdentity(ctx context.Context, input CreateAuthIdentityInput) (*CreateAuthIdentityResult, error) {
	return r.UserStore.CreateAuthIdentity(ctx, input)
}

// GetUserByCanonicalIdentity 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetUserByCanonicalIdentity(ctx context.Context, key AuthIdentityKey) (*UserAuthIdentityLookup, error) {
	return r.UserStore.GetUserByCanonicalIdentity(ctx, key)
}

// GetUserByChannelIdentity 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetUserByChannelIdentity(ctx context.Context, key AuthIdentityChannelKey) (*UserAuthIdentityLookup, error) {
	return r.UserStore.GetUserByChannelIdentity(ctx, key)
}

// ListUserAuthIdentities 转接身份存储，原事务 context 原样传递。
func (r *userRepository) ListUserAuthIdentities(ctx context.Context, userID int64) ([]service.UserAuthIdentityRecord, error) {
	return r.UserStore.ListUserAuthIdentities(ctx, userID)
}

// UnbindUserAuthProvider 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) error {
	return r.UserStore.UnbindUserAuthProvider(ctx, userID, provider)
}

// BindAuthIdentityToUser 转接身份存储，原事务 context 原样传递。
func (r *userRepository) BindAuthIdentityToUser(ctx context.Context, input BindAuthIdentityInput) (*CreateAuthIdentityResult, error) {
	return r.UserStore.BindAuthIdentityToUser(ctx, input)
}

// RecordProviderGrant 转接身份存储，原事务 context 原样传递。
func (r *userRepository) RecordProviderGrant(ctx context.Context, input ProviderGrantRecordInput) (bool, error) {
	return r.UserStore.RecordProviderGrant(ctx, input)
}

// UpsertIdentityAdoptionDecision 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpsertIdentityAdoptionDecision(ctx context.Context, input IdentityAdoptionDecisionInput) (*dbent.IdentityAdoptionDecision, error) {
	return r.UserStore.UpsertIdentityAdoptionDecision(ctx, input)
}

// GetIdentityAdoptionDecisionByPendingAuthSessionID 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetIdentityAdoptionDecisionByPendingAuthSessionID(ctx context.Context, pendingAuthSessionID int64) (*dbent.IdentityAdoptionDecision, error) {
	return r.UserStore.GetIdentityAdoptionDecisionByPendingAuthSessionID(ctx, pendingAuthSessionID)
}

// UpdateUserLastLoginAt 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateUserLastLoginAt(ctx context.Context, userID int64, loginAt time.Time) error {
	return r.UserStore.UpdateUserLastLoginAt(ctx, userID, loginAt)
}

// UpdateUserLastActiveAt 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	return r.UserStore.UpdateUserLastActiveAt(ctx, userID, activeAt)
}

// GetUserAvatar 转接身份存储，原事务 context 原样传递。
func (r *userRepository) GetUserAvatar(ctx context.Context, userID int64) (*service.UserAvatar, error) {
	return r.UserStore.GetUserAvatar(ctx, userID)
}

// UpsertUserAvatar 转接身份存储，原事务 context 原样传递。
func (r *userRepository) UpsertUserAvatar(ctx context.Context, userID int64, input service.UpsertUserAvatarInput) (*service.UserAvatar, error) {
	return r.UserStore.UpsertUserAvatar(ctx, userID, input)
}

// DeleteUserAvatar 转接身份存储，原事务 context 原样传递。
func (r *userRepository) DeleteUserAvatar(ctx context.Context, userID int64) error {
	return r.UserStore.DeleteUserAvatar(ctx, userID)
}
