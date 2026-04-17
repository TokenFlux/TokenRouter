//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func newAuthServiceForPendingOAuthTest() *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret-pending-oauth",
			ExpireHour: 1,
		},
	}
	return NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil)
}

// TestVerifyPendingOAuthToken_ValidToken 验证正常签发的 pending token 可以被成功解析。
func TestVerifyPendingOAuthToken_ValidToken(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()

	token, err := svc.CreatePendingOAuthToken("user@example.com", "alice")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	email, username, err := svc.VerifyPendingOAuthToken(token)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", email)
	require.Equal(t, "alice", username)
}

// TestVerifyPendingOAuthToken_RegularJWTRejected 用普通 access token 尝试验证，应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_RegularJWTRejected(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()

	// 签发一个普通 access token（JWTClaims，无 Purpose 字段）
	accessToken, err := svc.GenerateToken(&User{
		ID:    1,
		Email: "user@example.com",
		Role:  RoleUser,
	})
	require.NoError(t, err)

	_, _, err = svc.VerifyPendingOAuthToken(accessToken)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestVerifyPendingOAuthToken_WrongPurpose 手动构造 purpose 字段不匹配的 JWT，应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_WrongPurpose(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()

	now := time.Now()
	claims := &pendingOAuthClaims{
		Email:    "user@example.com",
		Username: "alice",
		Purpose:  "some_other_purpose",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte(svc.cfg.JWT.Secret))
	require.NoError(t, err)

	_, _, err = svc.VerifyPendingOAuthToken(tokenStr)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestVerifyPendingOAuthToken_MissingPurpose 手动构造无 purpose 字段的 JWT（模拟旧 token），应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_MissingPurpose(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()

	now := time.Now()
	claims := &pendingOAuthClaims{
		Email:    "user@example.com",
		Username: "alice",
		Purpose:  "", // 旧 token 无此字段，反序列化后为零值
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte(svc.cfg.JWT.Secret))
	require.NoError(t, err)

	_, _, err = svc.VerifyPendingOAuthToken(tokenStr)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestVerifyPendingOAuthToken_ExpiredToken 过期 token 应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_ExpiredToken(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()

	past := time.Now().Add(-1 * time.Hour)
	claims := &pendingOAuthClaims{
		Email:    "user@example.com",
		Username: "alice",
		Purpose:  pendingOAuthPurpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(past),
			IssuedAt:  jwt.NewNumericDate(past.Add(-10 * time.Minute)),
			NotBefore: jwt.NewNumericDate(past.Add(-10 * time.Minute)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte(svc.cfg.JWT.Secret))
	require.NoError(t, err)

	_, _, err = svc.VerifyPendingOAuthToken(tokenStr)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestVerifyPendingOAuthToken_WrongSecret 不同密钥签发的 token 应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_WrongSecret(t *testing.T) {
	other := NewAuthService(nil, nil, nil, nil, &config.Config{
		JWT: config.JWTConfig{Secret: "other-secret"},
	}, nil, nil, nil, nil, nil, nil)

	token, err := other.CreatePendingOAuthToken("user@example.com", "alice")
	require.NoError(t, err)

	svc := newAuthServiceForPendingOAuthTest()
	_, _, err = svc.VerifyPendingOAuthToken(token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

// TestVerifyPendingOAuthToken_TooLong 超长 token 应返回 ErrInvalidToken。
func TestVerifyPendingOAuthToken_TooLong(t *testing.T) {
	svc := newAuthServiceForPendingOAuthTest()
	giant := make([]byte, maxTokenLength+1)
	for i := range giant {
		giant[i] = 'a'
	}
	_, _, err := svc.VerifyPendingOAuthToken(string(giant))
	require.ErrorIs(t, err, ErrInvalidToken)
}
