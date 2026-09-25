// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// MaxTokenLength 限制 token 大小，避免超长 header 触发解析时的异常内存分配。
const MaxTokenLength = 8192

// RefreshTokenPrefix is the prefix for refresh tokens to distinguish them from access tokens.
const RefreshTokenPrefix = "rt_"

// JWTClaims JWT载荷数据
type JWTClaims struct {
	UserID       int64  `json:"user_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	TokenVersion int64  `json:"token_version"` // Used to invalidate tokens on password change
	// SessionID 会话 ID（与 refresh token family 对应），用于单会话撤销与 step-up 授权绑定。
	SessionID string `json:"sid,omitempty"`
	// BindingHash 会话指纹哈希（IP+UA），会话绑定开启时校验；空值表示旧 token（平滑升级）。
	BindingHash string `json:"bnd,omitempty"`
	jwt.RegisteredClaims
}

type PendingOAuthClaims struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	AffCode  string `json:"aff_code,omitempty"`
	Purpose  string `json:"purpose"`
	jwt.RegisteredClaims
}

type PendingOAuthIdentity struct {
	Email    string
	Username string
	AffCode  string
}

// TokenPair 包含Access Token和Refresh Token
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // Access Token有效期（秒）
}

// TokenPairWithUser extends TokenPair with user role for backend mode checks
type TokenPairWithUser struct {
	TokenPair
	UserRole string
}

// ValidateToken 验证JWT token并返回用户声明
func (s *SessionService) ValidateToken(tokenString string) (*JWTClaims, error) {
	// 先做长度校验，尽早拒绝异常超长 token，降低 DoS 风险。
	if len(tokenString) > MaxTokenLength {
		return nil, ErrTokenTooLarge
	}

	// 使用解析器并限制可接受的签名算法，防止算法混淆。
	parser := jwt.NewParser(jwt.WithTimeFunc(s.now), jwt.WithValidMethods([]string{
		jwt.SigningMethodHS256.Name,
		jwt.SigningMethodHS384.Name,
		jwt.SigningMethodHS512.Name,
	}))

	// 保留默认 claims 校验（exp/nbf），避免放行过期或未生效的 token。
	token, err := parser.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (any, error) {
		// 验证签名方法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(s.options.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			// token 过期但仍返回 claims（用于 RefreshToken 等场景）
			// jwt-go 在解析时即使遇到过期错误，token.Claims 仍会被填充
			if claims, ok := token.Claims.(*JWTClaims); ok {
				return claims, ErrTokenExpired
			}
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, ErrInvalidToken
}

// GenerateToken 生成JWT access token
// 使用新的access_token_expire_minutes配置项（如果配置了），否则回退到expire_hour。
// 会话指纹（IP/UA）从 ctx 中提取（由 HTTP 入口中间件注入），缺失时生成不带绑定的 token。
func (s *SessionService) GenerateToken(ctx context.Context, user *User) (string, error) {
	sessionID, err := RandomHexString(8)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return s.GenerateAccessToken(user, sessionID, SessionBindingHashFromContext(ctx))
}

// GenerateAccessToken 生成带会话 ID 与绑定指纹的 access token。
func (s *SessionService) GenerateAccessToken(user *User, sessionID, bindingHash string) (string, error) {
	now := s.now()
	var expiresAt time.Time
	if s.options.AccessTokenExpireMinutes > 0 {
		expiresAt = now.Add(time.Duration(s.options.AccessTokenExpireMinutes) * time.Minute)
	} else {
		// 向后兼容：使用旧的expire_hour配置
		expiresAt = now.Add(time.Duration(s.options.ExpireHour) * time.Hour)
	}

	claims := &JWTClaims{
		UserID:       user.ID,
		Email:        user.Email,
		Role:         user.Role,
		TokenVersion: ResolvedTokenVersion(user),
		SessionID:    sessionID,
		BindingHash:  bindingHash,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.options.Secret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return tokenString, nil
}

// GetAccessTokenExpiresIn 返回Access Token的有效期（秒）
// 用于前端设置刷新定时器
func (s *SessionService) GetAccessTokenExpiresIn() int {
	if s.options.AccessTokenExpireMinutes > 0 {
		return s.options.AccessTokenExpireMinutes * 60
	}
	return s.options.ExpireHour * 3600
}

// HashPassword 使用bcrypt加密密码
func (s *SessionService) HashPassword(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashedBytes), nil
}

// CheckPassword 验证密码是否匹配
func (s *SessionService) CheckPassword(password, hashedPassword string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// RefreshToken 刷新token
func (s *SessionService) RefreshToken(ctx context.Context, oldTokenString string) (string, error) {
	// 验证旧token（即使过期也允许，用于刷新）
	claims, err := s.ValidateToken(oldTokenString)
	if err != nil && !errors.Is(err, ErrTokenExpired) {
		return "", err
	}

	// 获取最新的用户信息
	user, err := s.userRepo.GetByID(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", ErrInvalidToken
		}
		s.observer.Printf("service.auth", "[Auth] Database error refreshing token: %v", err)
		return "", ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		return "", ErrUserNotActive
	}

	// Security: Check TokenVersion to prevent refreshing revoked tokens
	// This ensures tokens issued before a password change cannot be refreshed
	if claims.TokenVersion != ResolvedTokenVersion(user) {
		return "", ErrTokenRevoked
	}

	// 会话绑定检查：指纹变化的旧 token 不允许换发新 token。
	if s.settingService != nil && s.settingService.IsSessionBindingEnabled(ctx) && claims.BindingHash != "" {
		if current := SessionBindingHashFromContext(ctx); current != "" && current != claims.BindingHash {
			_ = s.RevokeSessionFamily(ctx, claims.SessionID)
			return "", ErrSessionBindingMismatch
		}
	}

	// 生成新token
	return s.GenerateToken(ctx, user)
}

// GenerateTokenPair 生成Access Token和Refresh Token对
// familyID: 可选的Token家族ID，用于Token轮转时保持家族关系
func (s *SessionService) GenerateTokenPair(ctx context.Context, user *User, familyID string) (*TokenPair, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, errors.New("refresh token cache not configured")
	}

	// 提前确定家族ID：作为 access token 的会话ID（sid），保证同一会话的
	// access/refresh token 可以互相关联（单会话撤销、step-up 授权绑定）。
	if familyID == "" {
		familyBytes := make([]byte, 16)
		if _, err := rand.Read(familyBytes); err != nil {
			return nil, fmt.Errorf("generate family id: %w", err)
		}
		familyID = hex.EncodeToString(familyBytes)
	}

	// 生成Access Token（携带会话ID与绑定指纹）
	accessToken, err := s.GenerateAccessToken(user, familyID, SessionBindingHashFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	// 生成Refresh Token
	refreshToken, err := s.GenerateRefreshToken(ctx, user, familyID)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    s.GetAccessTokenExpiresIn(),
	}, nil
}

// GenerateRefreshToken 生成并存储Refresh Token
func (s *SessionService) GenerateRefreshToken(ctx context.Context, user *User, familyID string) (string, error) {
	// 生成随机Token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	rawToken := RefreshTokenPrefix + hex.EncodeToString(tokenBytes)

	// 计算Token哈希（存储哈希而非原始Token）
	tokenHash := HashToken(rawToken)

	// 如果没有提供familyID，生成新的
	if familyID == "" {
		familyBytes := make([]byte, 16)
		if _, err := rand.Read(familyBytes); err != nil {
			return "", fmt.Errorf("generate family id: %w", err)
		}
		familyID = hex.EncodeToString(familyBytes)
	}

	now := s.now()
	ttl := time.Duration(s.options.RefreshTokenExpireDays) * 24 * time.Hour

	data := &RefreshTokenData{
		UserID:       user.ID,
		TokenVersion: ResolvedTokenVersion(user),
		FamilyID:     familyID,
		BindingHash:  SessionBindingHashFromContext(ctx),
		CreatedAt:    now,
		ExpiresAt:    now.Add(ttl),
	}

	// 存储Token数据
	if err := s.refreshTokenCache.StoreRefreshToken(ctx, tokenHash, data, ttl); err != nil {
		return "", fmt.Errorf("store refresh token: %w", err)
	}

	// 添加到用户Token集合
	if err := s.refreshTokenCache.AddToUserTokenSet(ctx, user.ID, tokenHash, ttl); err != nil {
		s.observer.Printf("service.auth", "[Auth] Failed to add token to user set: %v", err)
		// 不影响主流程
	}

	// 添加到家族Token集合
	if err := s.refreshTokenCache.AddToFamilyTokenSet(ctx, familyID, tokenHash, ttl); err != nil {
		s.observer.Printf("service.auth", "[Auth] Failed to add token to family set: %v", err)
		// 不影响主流程
	}

	return rawToken, nil
}

// RefreshTokenPair 使用Refresh Token刷新Token对
// 实现Token轮转：每次刷新都会生成新的Refresh Token，旧Token立即失效
func (s *SessionService) RefreshTokenPair(ctx context.Context, refreshToken string) (*TokenPairWithUser, error) {
	// 检查 refreshTokenCache 是否可用
	if s.refreshTokenCache == nil {
		return nil, ErrRefreshTokenInvalid
	}

	// 验证Token格式
	if !strings.HasPrefix(refreshToken, RefreshTokenPrefix) {
		return nil, ErrRefreshTokenInvalid
	}

	tokenHash := HashToken(refreshToken)

	// 获取Token数据
	data, err := s.refreshTokenCache.GetRefreshToken(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			// Token不存在，可能是已被使用（Token轮转）或已过期
			s.observer.Printf("service.auth", "[Auth] Refresh token not found, possible reuse attack")
			return nil, ErrRefreshTokenInvalid
		}
		s.observer.Printf("service.auth", "[Auth] Error getting refresh token: %v", err)
		return nil, ErrServiceUnavailable
	}

	// 检查Token是否过期
	if s.now().After(data.ExpiresAt) {
		// 删除过期Token
		_ = s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
		return nil, ErrRefreshTokenExpired
	}

	// 获取用户信息
	user, err := s.userRepo.GetByID(ctx, data.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// 用户已删除，撤销整个Token家族
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			return nil, ErrRefreshTokenInvalid
		}
		s.observer.Printf("service.auth", "[Auth] Database error getting user for token refresh: %v", err)
		return nil, ErrServiceUnavailable
	}

	// 检查用户状态
	if !user.IsActive() {
		// 用户被禁用，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		return nil, ErrUserNotActive
	}

	// 检查TokenVersion（密码更改后所有Token失效）
	if data.TokenVersion != ResolvedTokenVersion(user) {
		// TokenVersion不匹配，撤销整个Token家族
		_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
		return nil, ErrTokenRevoked
	}

	// 会话绑定检查：IP/UA 任一变化即撤销整个会话家族。
	// data.BindingHash 为空表示功能开启前签发的旧会话，放行并在轮转时补齐绑定。
	if s.settingService != nil && s.settingService.IsSessionBindingEnabled(ctx) && data.BindingHash != "" {
		if current := SessionBindingHashFromContext(ctx); current != "" && current != data.BindingHash {
			_ = s.refreshTokenCache.DeleteTokenFamily(ctx, data.FamilyID)
			s.observer.Printf("service.auth", "[Auth] Session binding mismatch on refresh for user %d, family revoked", data.UserID)
			return nil, ErrSessionBindingMismatch
		}
	}

	// 校验后再认领唯一消费权，避免并发请求或撤销后的旧快照继续签发。
	consumed, err := s.refreshTokenCache.ConsumeRefreshToken(ctx, tokenHash)
	if err != nil {
		s.observer.Printf("service.auth", "[Auth] Failed to delete old refresh token: %v", err)
		return nil, ErrServiceUnavailable
	}
	if !consumed {
		return nil, ErrRefreshTokenInvalid
	}

	// 生成新的Token对，保持同一个家族ID
	pair, err := s.GenerateTokenPair(ctx, user, data.FamilyID)
	if err != nil {
		return nil, err
	}
	return &TokenPairWithUser{
		TokenPair: *pair,
		UserRole:  user.Role,
	}, nil
}

// RevokeRefreshToken 撤销单个Refresh Token
func (s *SessionService) RevokeRefreshToken(ctx context.Context, refreshToken string) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	if !strings.HasPrefix(refreshToken, RefreshTokenPrefix) {
		return ErrRefreshTokenInvalid
	}

	tokenHash := HashToken(refreshToken)
	return s.refreshTokenCache.DeleteRefreshToken(ctx, tokenHash)
}

// RevokeSessionFamily 撤销单个会话家族（该会话的所有 refresh token）。
// 用于会话绑定失效等单会话级撤销场景，不影响用户的其他设备会话。
func (s *SessionService) RevokeSessionFamily(ctx context.Context, familyID string) error {
	if s.refreshTokenCache == nil || familyID == "" {
		return nil
	}
	return s.refreshTokenCache.DeleteTokenFamily(ctx, familyID)
}

// RevokeAllUserSessions 撤销用户的所有会话（所有Refresh Token）
// 用于密码更改或用户主动登出所有设备
func (s *SessionService) RevokeAllUserSessions(ctx context.Context, userID int64) error {
	if s.refreshTokenCache == nil {
		return nil // No-op if cache not configured
	}
	return s.refreshTokenCache.DeleteUserRefreshTokens(ctx, userID)
}

// RevokeAllUserTokens invalidates both stateless access tokens and refresh sessions.
//
// 注意：users 表没有 token_version 列（ResolvedTokenVersion 由 email+password_hash
// 指纹推导），因此对 user.TokenVersion 自增只影响内存副本。之前紧跟其后的整行
// Update 不写任何有效数据，却会用旧快照覆盖并发写入的列，故已移除。
// 会话撤销由下面的 refresh session 清理承担；改密路径通过 password_hash 变化
// 改变指纹，从而使旧 token 失效。
func (s *SessionService) RevokeAllUserTokens(ctx context.Context, userID int64) error {
	if _, err := s.userRepo.GetByID(ctx, userID); err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if err := s.RevokeAllUserSessions(ctx, userID); err != nil {
		s.observer.Printf("service.auth", "[Auth] Failed to revoke refresh sessions after token invalidation for user %d: %v", userID, err)
	}
	return nil
}

// HashToken 计算Token的SHA256哈希
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func ResolvedTokenVersion(user *User) int64 {
	if user == nil {
		return 0
	}
	if user.TokenVersionResolved {
		return user.TokenVersion
	}

	material := strings.ToLower(strings.TrimSpace(user.Email)) + "\n" + user.PasswordHash
	sum := sha256.Sum256([]byte(material))
	fingerprint := int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
	return user.TokenVersion ^ fingerprint
}

func RandomHexString(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 16
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreatePendingOAuthToken 生成短期 JWT，用于在用户补全邀请码时暂存 OAuth 身份。
func (s *SessionService) CreatePendingOAuthToken(email, username string) (string, error) {
	return s.CreatePendingOAuthTokenWithAffiliate(email, username, "")
}

// CreatePendingOAuthTokenWithAffiliate 生成带邀请返利码的待补全 OAuth token。
func (s *SessionService) CreatePendingOAuthTokenWithAffiliate(email, username, affiliateCode string) (string, error) {
	now := s.now()
	claims := &PendingOAuthClaims{
		Email:    email,
		Username: username,
		AffCode:  strings.TrimSpace(affiliateCode),
		Purpose:  PendingOAuthPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(PendingOAuthTokenTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.options.Secret))
}

// VerifyPendingOAuthToken validates a pending OAuth token and returns the embedded identity.
// Returns ErrInvalidToken when the token is invalid or expired.
func (s *SessionService) VerifyPendingOAuthToken(tokenStr string) (email, username string, err error) {
	identity, err := s.VerifyPendingOAuthTokenDetails(tokenStr)
	if err != nil {
		return "", "", err
	}
	return identity.Email, identity.Username, nil
}

func (s *SessionService) VerifyPendingOAuthTokenDetails(tokenStr string) (*PendingOAuthIdentity, error) {
	if len(tokenStr) > MaxTokenLength {
		return nil, ErrInvalidToken
	}
	parser := jwt.NewParser(jwt.WithTimeFunc(s.now), jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}))
	token, parseErr := parser.ParseWithClaims(tokenStr, &PendingOAuthClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(s.options.Secret), nil
	})
	if parseErr != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*PendingOAuthClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.Purpose != PendingOAuthPurpose {
		return nil, ErrInvalidToken
	}
	return &PendingOAuthIdentity{
		Email:    claims.Email,
		Username: claims.Username,
		AffCode:  strings.TrimSpace(claims.AffCode),
	}, nil
}

// SessionOptions 固化启动 JWT 配置，运行时会话绑定开关按请求读取。
type SessionOptions struct {
	Now                      func() time.Time
	Secret                   string
	ExpireHour               int
	AccessTokenExpireMinutes int
	RefreshTokenExpireDays   int
}
type SessionUserReader interface {
	GetByID(context.Context, int64) (*User, error)
}
type SessionSettings interface{ IsSessionBindingEnabled(context.Context) bool }

// SessionService 持有唯一会话流程；Redis 状态仍由注入缓存保存。
type SessionService struct {
	options           SessionOptions
	userRepo          SessionUserReader
	refreshTokenCache RefreshTokenCache
	settingService    SessionSettings
	observer          Observer
}

func NewSessionService(options SessionOptions, users SessionUserReader, cache RefreshTokenCache, settings SessionSettings, logf LogFunc) *SessionService {
	return &SessionService{options: options, userRepo: users, refreshTokenCache: cache, settingService: settings, observer: Observer{Log: logf}}
}

// PendingOAuthPurpose 是待补全 OAuth 注册 token 的用途标记。
const PendingOAuthPurpose = "pending_oauth_registration"

// PendingOAuthTokenTTL 是待补全 OAuth 注册 token 的有效期。
const PendingOAuthTokenTTL = 10 * time.Minute
