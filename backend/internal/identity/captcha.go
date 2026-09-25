// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	"context"
)

// TencentCaptchaConfig 保存腾讯云票据校验接口所需凭据，禁止通过公开接口返回。
type TencentCaptchaConfig struct {
	Enabled        bool
	AppID          string
	AppSecretKey   string
	CloudSecretID  string
	CloudSecretKey string
	Region         string
}

// AliyunCaptchaConfig 保存阿里云验证码 2.0 服务端校验所需凭据，公开接口不得返回该结构。
type AliyunCaptchaConfig struct {
	Enabled         bool
	AccessKeyID     string
	AccessKeySecret string
	SceneID         string
	Region          string
}

type CaptchaProviderConfig struct {
	TurnstileEnabled   bool
	TurnstileSecretKey string
	Tencent            TencentCaptchaConfig
	Aliyun             AliyunCaptchaConfig
}

// CaptchaSettings 按动作读取同一份动态验证配置。
type CaptchaSettings interface {
	GetCaptchaProviderConfig(context.Context) (CaptchaProviderConfig, error)
	IsTurnstileEnabled(context.Context) bool
	GetTurnstileSecretKey(context.Context) string
}

// LogFunc 将身份诊断投递到装配层选择的唯一日志后端。
type LogFunc func(component, format string, args ...any)

// Observer 允许纯核心在未配置观察者时保持无输出。
type Observer struct{ Log LogFunc }

func (o Observer) Printf(component, format string, args ...any) {
	if o.Log != nil {
		o.Log(component, format, args...)
	}
}
