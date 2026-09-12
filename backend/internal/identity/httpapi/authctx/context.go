// 本文件维护 authctx 的所属能力；兼容入口复用唯一实现。
package authctx

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	gin "github.com/gin-gonic/gin"
)

const (
	ContextKeyUser              = "user"
	ContextKeyUserRole          = "user_role"
	ContextKeyAuthEmail         = "auth_email"
	ContextKeySessionID         = "session_id"
	principalKey                = "identity_principal"
	MaxPersistentUserAgentBytes = 512
)

// AuthSubject 只供旧 context 消费者投影；新身份使用 Principal。
type AuthSubject struct {
	UserID      int64
	Concurrency int
}

// authenticationRecord 是请求认证的唯一来源，旧字段仅为尚未迁出的直接读取者保留。
// API Key 的行为身份与旧付款用户投影分开保存，不能把付款人的管理员角色赋给 Key 身份。
type authenticationRecord struct {
	Principal  identity.Principal
	Subject    AuthSubject
	HasSubject bool
	Role       string
	HasRole    bool
}

func SetPrincipal(c *gin.Context, p identity.Principal, concurrency int, email string) {
	subject := AuthSubject{UserID: p.UserID, Concurrency: concurrency}
	c.Set(principalKey, authenticationRecord{Principal: p, Subject: subject, HasSubject: true, Role: p.Role, HasRole: true})
	c.Set(ContextKeyUser, subject)
	c.Set(ContextKeyUserRole, p.Role)
	c.Set(ContextKeyAuthEmail, email)
	c.Set(ContextKeySessionID, p.SessionID)
}
func recordFromContext(c *gin.Context) (authenticationRecord, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return authenticationRecord{}, false
	}
	r, ok := v.(authenticationRecord)
	return r, ok
}
func GetPrincipal(c *gin.Context) (identity.Principal, bool) {
	r, ok := recordFromContext(c)
	return r.Principal, ok
}
func GetAuthSubjectFromContext(c *gin.Context) (AuthSubject, bool) {
	if r, ok := recordFromContext(c); ok {
		return r.Subject, r.HasSubject
	}
	v, ok := c.Get(ContextKeyUser)
	if !ok {
		return AuthSubject{}, false
	}
	s, ok := v.(AuthSubject)
	return s, ok
}
func GetUserRoleFromContext(c *gin.Context) (string, bool) {
	if r, ok := recordFromContext(c); ok {
		return r.Role, r.HasRole
	}
	v, ok := c.Get(ContextKeyUserRole)
	if !ok {
		return "", false
	}
	role, ok := v.(string)
	return role, ok
}

// SetAuthenticatedPrincipal 固定旧网关已完成的付款/并发投影，同时以真实行为身份保存 Principal。
func SetAuthenticatedPrincipal(c *gin.Context, p identity.Principal) {
	record := authenticationRecord{Principal: p}
	if v, ok := c.Get(ContextKeyUser); ok {
		record.Subject, record.HasSubject = v.(AuthSubject)
	}
	if v, ok := c.Get(ContextKeyUserRole); ok {
		record.Role, record.HasRole = v.(string)
	}
	c.Set(principalKey, record)
}
