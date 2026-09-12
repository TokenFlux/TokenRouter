package apikey

import (
	"context"
	"fmt"
)

// @project-doc docs/architecture/gateway_request_lifecycle.md#apikey_authentication
// AccessSnapshot 明确区分凭据所有者、付款用户与行为成员；资金来源由 billing 另行解析。
// key 仅属于这次认证，不能反向写入 L1/L2 快照。
type AccessSnapshot struct {
	KeyID       int64
	OwnerUserID int64
	PayerUserID int64
	ActorUserID int64
	TeamID      *int64
	key         *APIKey
}

// KeyView 为同一次请求的网关提供访问策略投影，不包含旧 service 实体。
func (a *AccessSnapshot) KeyView() *APIKey {
	if a == nil {
		return nil
	}
	return a.key
}

// AuthenticationInput 提供 HTTP 层已经解析的地址与消费入口意图，不读取请求体。
type AuthenticationInput struct {
	ClientIP          string
	CheckMemberLimits bool
}
type AuthenticationFailureKind string

const (
	AuthenticationLookup       AuthenticationFailureKind = "lookup"
	AuthenticationDisabled     AuthenticationFailureKind = "disabled"
	AuthenticationTeam         AuthenticationFailureKind = "team"
	AuthenticationMemberLimit  AuthenticationFailureKind = "member_limit"
	AuthenticationIP           AuthenticationFailureKind = "ip"
	AuthenticationUserMissing  AuthenticationFailureKind = "user_missing"
	AuthenticationUserInactive AuthenticationFailureKind = "user_inactive"
)

// AuthenticationFailure 只携带失败阶段，协议状态码和错误 envelope 由 HTTP 适配器决定。
type AuthenticationFailure struct {
	Kind     AuthenticationFailureKind
	Cause    error
	ClientIP string
}

func (e *AuthenticationFailure) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return string(e.Kind)
}
func (e *AuthenticationFailure) Unwrap() error { return e.Cause }

// Authenticate 保留 Key/团队/成员限额/IP/付款用户的既有检查顺序。
// 失败时若返回非空快照，它只供诊断使用，不代表通过认证。
// Key 过期与额度耗尽仍由之后的资金准入处理，非消费入口可以读取已有数据。
func (s *APIKeyService) Authenticate(ctx context.Context, credential string, input AuthenticationInput) (*AccessSnapshot, error) {
	key, err := s.GetByKey(ctx, credential)
	if err != nil {
		return nil, &AuthenticationFailure{Kind: AuthenticationLookup, Cause: err}
	}
	access := &AccessSnapshot{KeyID: key.ID, OwnerUserID: key.UserID, ActorUserID: key.UserID, TeamID: clonePointer(key.TeamID), key: key}
	if key.User != nil {
		access.PayerUserID = key.User.ID
	}
	if key.ActorUser != nil {
		access.ActorUserID = key.ActorUser.ID
	}
	fail := func(kind AuthenticationFailureKind, err error) (*AccessSnapshot, error) {
		return access, &AuthenticationFailure{Kind: kind, Cause: err}
	}
	if !key.IsActive() && key.Status != StatusAPIKeyExpired && key.Status != StatusAPIKeyQuotaExhausted {
		return fail(AuthenticationDisabled, nil)
	}
	if err := s.ValidateTeamKeyLifecycle(key); err != nil {
		return fail(AuthenticationTeam, err)
	}
	if input.CheckMemberLimits {
		if err := s.CheckTeamMemberLimits(key); err != nil {
			return fail(AuthenticationMemberLimit, err)
		}
	}
	if len(key.IPWhitelist) > 0 || len(key.IPBlacklist) > 0 {
		if allowed, _ := CheckIPRestrictionWithCompiledRules(input.ClientIP, key.CompiledIPWhitelist, key.CompiledIPBlacklist); !allowed {
			ip := input.ClientIP
			if ip == "" {
				ip = "unknown"
			}
			return access, &AuthenticationFailure{Kind: AuthenticationIP, ClientIP: ip, Cause: ipAccessDenied(ip)}
		}
	}
	if key.User == nil {
		return fail(AuthenticationUserMissing, nil)
	}
	if !key.User.IsActive() {
		return fail(AuthenticationUserInactive, nil)
	}
	return access, nil
}

// ipAccessDenied 保留既有公开错误文本；HTTP 适配独立决定响应形状。
type ipAccessDenied string

func (e ipAccessDenied) Error() string { return fmt.Sprintf("Access denied. Your IP is %s", string(e)) }
