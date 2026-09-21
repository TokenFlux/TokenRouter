//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

// totpVMUserRepoStub 仅实现 TOTP 验证方式测试所需方法；未桩方法调用即 panic（嵌入 nil 接口）。
type totpVMUserRepoStub struct {
	identity.UserRepository

	user          *identity.User
	totpDisabled  bool
	disableCalled bool
}

func (s *totpVMUserRepoStub) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	if s.user == nil {
		return nil, errors.New("user not found")
	}
	return s.user, nil
}

func (s *totpVMUserRepoStub) DisableTotp(ctx context.Context, userID int64) error {
	s.disableCalled = true
	s.totpDisabled = true
	return nil
}

type totpVMSettingRepoStub struct {
	settings.Repository
	values map[string]string
}

func (s *totpVMSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", errors.New("setting not found")
	}
	return v, nil
}

func newTotpVMService(t *testing.T, user *identity.User, emailVerifyEnabled bool) (*identity.TotpService, *totpVMUserRepoStub) {
	t.Helper()
	userRepo := &totpVMUserRepoStub{user: user}
	values := map[string]string{}
	if emailVerifyEnabled {
		values[identity.SettingKeyEmailVerifyEnabled] = "true"
	}
	settingSvc := identity.NewRuntimeSettings(&totpVMSettingRepoStub{values: values}, settings.ErrSettingNotFound)
	return identity.NewTotpService(userRepo, nil, nil, totpVerificationSettings{runtime: settingSvc}, nil, nil), userRepo
}

func TestGetVerificationMethodAdminAlwaysPassword(t *testing.T) {
	admin := &identity.User{ID: 1, Email: "admin@example.com", Role: identity.RoleAdmin}
	svc, _ := newTotpVMService(t, admin, true)

	method, err := svc.GetVerificationMethod(context.Background(), admin.ID)
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
}

func TestGetVerificationMethodRegularUserFollowsEmailVerifySetting(t *testing.T) {
	user := &identity.User{ID: 2, Email: "user@example.com", Role: identity.RoleUser}

	svcEmailOn, _ := newTotpVMService(t, user, true)
	method, err := svcEmailOn.GetVerificationMethod(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "email", method.Method)

	svcEmailOff, _ := newTotpVMService(t, user, false)
	method, err = svcEmailOff.GetVerificationMethod(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
}

func TestTotpDisableAdminUsesPasswordEvenWithEmailVerifyEnabled(t *testing.T) {
	admin := &identity.User{ID: 1, Email: "admin@example.com", Role: identity.RoleAdmin, TotpEnabled: true}
	require.NoError(t, admin.SetPassword("correct-password"))
	svc, userRepo := newTotpVMService(t, admin, true)

	// 缺密码 → 要求密码（而非邮箱验证码）。
	err := svc.Disable(context.Background(), admin.ID, "", "")
	require.ErrorIs(t, err, identity.ErrPasswordRequired)

	// 密码错误 → 拒绝。
	err = svc.Disable(context.Background(), admin.ID, "", "wrong-password")
	require.ErrorIs(t, err, identity.ErrPasswordIncorrect)

	// 密码正确 → 成功停用；全程不需要邮箱验证码（emailService 为 nil，走到邮箱分支会 panic）。
	err = svc.Disable(context.Background(), admin.ID, "", "correct-password")
	require.NoError(t, err)
	require.True(t, userRepo.disableCalled)
}

func TestTotpDisableRegularUserStillRequiresEmailCode(t *testing.T) {
	user := &identity.User{ID: 2, Email: "user@example.com", Role: identity.RoleUser, TotpEnabled: true}
	require.NoError(t, user.SetPassword("whatever"))
	svc, _ := newTotpVMService(t, user, true)

	err := svc.Disable(context.Background(), user.ID, "", "whatever")
	require.ErrorIs(t, err, identity.ErrVerifyCodeRequired)
}

// 验证方式只读取邮箱开关；其它设置端口保持未安装，防止扩大测试依赖。
type totpVerificationSettings struct {
	identity.TotpSettings
	runtime *identity.RuntimeSettings
}

func (s totpVerificationSettings) IsEmailVerifyEnabled(ctx context.Context) bool {
	return s.runtime.IsEmailVerifyEnabled(ctx)
}
