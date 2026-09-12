package identity

import (
	"context"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

// TestSessionClockControlsSigningAndValidation 确认签发、有效期与库内 JWT 校验使用同一注入时钟。
func TestSessionClockControlsSigningAndValidation(t *testing.T) {
	now := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := NewSessionService(SessionOptions{Secret: "s05-clock-fixture", ExpireHour: 1, Now: func() time.Time { return now }}, nil, nil, nil, nil)
	token, err := svc.GenerateAccessToken(&User{ID: 1, Email: "clock@example.com", Role: "user"}, "sid", "")
	require.NoError(t, err)
	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	require.Equal(t, now.Unix(), claims.IssuedAt.Unix())
	require.Equal(t, now.Add(time.Hour).Unix(), claims.ExpiresAt.Unix())
	now = now.Add(2 * time.Hour)
	_, err = svc.ValidateToken(token)
	require.ErrorIs(t, err, ErrTokenExpired)
}

func TestTotpClockUsesOriginalValidationWindow(t *testing.T) {
	now := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	secret := "JBSWY3DPEHPK3PXP"
	svc := NewTotpService(nil, nil, nil, nil, nil, nil, func() time.Time { return now })
	code, err := totp.GenerateCode(secret, now)
	require.NoError(t, err)
	require.True(t, svc.validateCode(code, secret))
	now = now.Add(30 * time.Second)
	require.True(t, svc.validateCode(code, secret))
	now = now.Add(60 * time.Second)
	require.False(t, svc.validateCode(code, secret))
}

type clockEmailCache struct {
	EmailCache
	value *VerificationCodeData
}

func (c *clockEmailCache) SetNotifyVerifyCode(_ context.Context, _ string, data *VerificationCodeData, _ time.Duration) error {
	c.value = data
	return nil
}

// TestNotifyClockKeepsSeparateReadPoints 保留原 CreatedAt 和 ExpiresAt 的两次取时，不在装配时缓存时间。
func TestNotifyClockKeepsSeparateReadPoints(t *testing.T) {
	base := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	calls := 0
	clock := func() time.Time { v := base.Add(time.Duration(calls) * time.Second); calls++; return v }
	cache := &clockEmailCache{}
	require.NoError(t, ProfileSaveNotifyVerifyCodeWithClock(context.Background(), cache, "clock@example.com", "123456", clock))
	require.Equal(t, 2, calls)
	require.Equal(t, base, cache.value.CreatedAt)
	require.Equal(t, base.Add(time.Second+VerifyCodeTTL), cache.value.ExpiresAt)
}
