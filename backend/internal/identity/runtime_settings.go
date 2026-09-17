package identity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// 身份设置键和管理员凭据前缀沿用已有存储与认证协议。
const (
	AdminAPIKeyPrefix                             = "admin-"
	SettingKeyAdminAPIKey                         = "admin_api_key"
	SettingKeyAliyunCaptchaAccessKeyID            = "aliyun_captcha_access_key_id"
	SettingKeyAliyunCaptchaAccessKeySecret        = "aliyun_captcha_access_key_secret"
	SettingKeyAliyunCaptchaEnabled                = "aliyun_captcha_enabled"
	SettingKeyAliyunCaptchaRegion                 = "aliyun_captcha_region"
	SettingKeyAliyunCaptchaSceneID                = "aliyun_captcha_scene_id"
	SettingKeyDefaultUserAPIKeyLimit              = "default_user_api_key_limit"
	SettingKeyDefaultUserRPMLimit                 = "default_user_rpm_limit"
	SettingKeyEmailVerifyEnabled                  = "email_verify_enabled"
	SettingKeyPasswordResetEnabled                = "password_reset_enabled"
	SettingKeyRegistrationEmailDomainQuotaEnabled = "registration_email_domain_quota_enabled"
	SettingKeyRegistrationEmailNormalization      = "registration_email_normalization"
	SettingKeyRegistrationEmailSuffixWhitelist    = "registration_email_suffix_whitelist"
	SettingKeyRegistrationEnabled                 = "registration_enabled"
	SettingKeySessionBindingEnabled               = "session_binding_enabled"
	SettingKeyStepUpEnabled                       = "step_up_enabled"
	SettingKeyTencentCaptchaAppID                 = "tencent_captcha_app_id"
	SettingKeyTencentCaptchaAppSecretKey          = "tencent_captcha_app_secret_key"
	SettingKeyTencentCaptchaCloudSecretID         = "tencent_captcha_cloud_secret_id"
	SettingKeyTencentCaptchaCloudSecretKey        = "tencent_captcha_cloud_secret_key"
	SettingKeyTencentCaptchaEnabled               = "tencent_captcha_enabled"
	SettingKeyTencentCaptchaRegion                = "tencent_captcha_region"
	SettingKeyTotpEnabled                         = "totp_enabled"
	SettingKeyTurnstileEnabled                    = "turnstile_enabled"
	SettingKeyTurnstileSecretKey                  = "turnstile_secret_key"
	SettingKeyUserEmailChangeEnabled              = "user_email_change_enabled"
)

// RuntimeSettingsStore 只暴露身份设置所需的存取，不接收完整配置或旧实体。
type RuntimeSettingsStore interface {
	GetValue(context.Context, string) (string, error)
	GetMultiple(context.Context, []string) (map[string]string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}

// RuntimeSettings 解释注册、安全和 captcha 的动态设置，不建立第二份缓存。
type RuntimeSettings struct {
	settingRepo RuntimeSettingsStore
	notFound    error
}

// NewRuntimeSettings 构造不回源，动态值继续在原调用时点读取。
func NewRuntimeSettings(repo RuntimeSettingsStore, notFound error) *RuntimeSettings {
	return &RuntimeSettings{settingRepo: repo, notFound: notFound}
}

// IsRegistrationEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsRegistrationEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEnabled)
	if err != nil {
		// 安全默认：如果设置不存在或查询出错，默认关闭注册
		return false
	}
	return value == "true"
}

// IsEmailVerifyEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsEmailVerifyEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyEmailVerifyEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// IsRegistrationEmailDomainQuotaEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsRegistrationEmailDomainQuotaEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailDomainQuotaEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// IsUserEmailChangeEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsUserEmailChangeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyUserEmailChangeEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// GetRegistrationEmailSuffixWhitelist 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetRegistrationEmailSuffixWhitelist(ctx context.Context) []string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailSuffixWhitelist)
	if err != nil {
		return []string{}
	}
	return ParseRegistrationEmailSuffixWhitelist(value)
}

// IsPasswordResetEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsPasswordResetEnabled(ctx context.Context) bool {
	// Password reset requires email verification to be enabled
	if !s.IsEmailVerifyEnabled(ctx) {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPasswordResetEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsTotpEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsTotpEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTotpEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsSessionBindingEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsSessionBindingEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeySessionBindingEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsStepUpEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsStepUpEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyStepUpEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// GetDefaultUserRPMLimit 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetDefaultUserRPMLimit(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultUserRPMLimit)
	if err != nil || value == "" {
		return 0
	}
	if v, err := strconv.Atoi(value); err == nil && v >= 0 {
		return v
	}
	return 0
}

// GetDefaultUserAPIKeyLimit 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetDefaultUserAPIKeyLimit(ctx context.Context) int {
	if s == nil || s.settingRepo == nil {
		return DefaultUserAPIKeyLimit
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultUserAPIKeyLimit)
	if err != nil || value == "" {
		return DefaultUserAPIKeyLimit
	}
	if limit, err := strconv.Atoi(value); err == nil && IsValidUserAPIKeyLimit(limit) {
		return limit
	}
	return DefaultUserAPIKeyLimit
}

// IsTurnstileEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsTurnstileEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTurnstileEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// GetTurnstileSecretKey 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetTurnstileSecretKey(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTurnstileSecretKey)
	if err != nil {
		return ""
	}
	return value
}

// GetCaptchaProviderConfig 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetCaptchaProviderConfig(ctx context.Context) (CaptchaProviderConfig, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyTurnstileEnabled,
		SettingKeyTurnstileSecretKey,
		SettingKeyTencentCaptchaEnabled,
		SettingKeyTencentCaptchaAppID,
		SettingKeyTencentCaptchaAppSecretKey,
		SettingKeyTencentCaptchaCloudSecretID,
		SettingKeyTencentCaptchaCloudSecretKey,
		SettingKeyTencentCaptchaRegion,
		SettingKeyAliyunCaptchaEnabled,
		SettingKeyAliyunCaptchaAccessKeyID,
		SettingKeyAliyunCaptchaAccessKeySecret,
		SettingKeyAliyunCaptchaSceneID,
		SettingKeyAliyunCaptchaRegion,
	})
	if err != nil {
		return CaptchaProviderConfig{}, fmt.Errorf("read captcha provider settings: %w", err)
	}
	return CaptchaProviderConfig{
		TurnstileEnabled:   values[SettingKeyTurnstileEnabled] == "true",
		TurnstileSecretKey: values[SettingKeyTurnstileSecretKey],
		Tencent: TencentCaptchaConfig{
			Enabled:        values[SettingKeyTencentCaptchaEnabled] == "true",
			AppID:          values[SettingKeyTencentCaptchaAppID],
			AppSecretKey:   values[SettingKeyTencentCaptchaAppSecretKey],
			CloudSecretID:  values[SettingKeyTencentCaptchaCloudSecretID],
			CloudSecretKey: values[SettingKeyTencentCaptchaCloudSecretKey],
			Region:         NormalizeTencentCaptchaRegion(values[SettingKeyTencentCaptchaRegion]),
		},
		Aliyun: AliyunCaptchaConfig{
			Enabled:         values[SettingKeyAliyunCaptchaEnabled] == "true",
			AccessKeyID:     values[SettingKeyAliyunCaptchaAccessKeyID],
			AccessKeySecret: values[SettingKeyAliyunCaptchaAccessKeySecret],
			SceneID:         values[SettingKeyAliyunCaptchaSceneID],
			Region:          NormalizeAliyunCaptchaRegion(values[SettingKeyAliyunCaptchaRegion]),
		},
	}, nil
}

// IsTencentCaptchaEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsTencentCaptchaEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTencentCaptchaEnabled)
	return err == nil && value == "true"
}

// GetTencentCaptchaConfig 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetTencentCaptchaConfig(ctx context.Context) TencentCaptchaConfig {
	config, err := s.GetCaptchaProviderConfig(ctx)
	if err != nil {
		return TencentCaptchaConfig{}
	}
	return config.Tencent
}

// GenerateAdminAPIKey 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GenerateAdminAPIKey(ctx context.Context) (string, error) {
	// 生成 32 字节随机数 = 64 位十六进制字符
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	key := AdminAPIKeyPrefix + hex.EncodeToString(bytes)

	// 存储到 settings 表
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKey, key); err != nil {
		return "", fmt.Errorf("save admin api key: %w", err)
	}

	return key, nil
}

// GetAdminAPIKeyStatus 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetAdminAPIKeyStatus(ctx context.Context) (maskedKey string, exists bool, err error) {
	key, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if key == "" {
		return "", false, nil
	}

	// 脱敏：显示前 10 位和后 4 位
	if len(key) > 14 {
		maskedKey = key[:10] + "..." + key[len(key)-4:]
	} else {
		maskedKey = key
	}

	return maskedKey, true, nil
}

// GetAdminAPIKey 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) GetAdminAPIKey(ctx context.Context) (string, error) {
	key, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return "", nil // 未配置，返回空字符串
		}
		return "", err // 数据库错误
	}
	return key, nil
}

// DeleteAdminAPIKey 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) DeleteAdminAPIKey(ctx context.Context) error {
	return s.settingRepo.Delete(ctx, SettingKeyAdminAPIKey)
}

// IsRegistrationEmailNormalizationEnabled 保留身份设置的原读取时点、缺省和失败语义。
func (s *RuntimeSettings) IsRegistrationEmailNormalizationEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailNormalization)
	if err != nil {
		return false
	}
	return value == "true"
}
