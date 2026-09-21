// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	context "context"
	fmt "fmt"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	captcha "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/captcha/v20190722"
	common "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	profile "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
)

type tencentCaptchaAPI interface {
	DescribeCaptchaResultWithContext(context.Context, *captcha.DescribeCaptchaResultRequest) (*captcha.DescribeCaptchaResultResponse, error)
}

type tencentCaptchaClientFactory func(secretID, secretKey, endpoint string) (tencentCaptchaAPI, error)

type tencentCaptchaVerifier struct {
	newClient tencentCaptchaClientFactory
}

// NewTencentCaptchaVerifier 创建腾讯云验证码票据校验适配器。
func NewTencentCaptchaVerifier() identity.TencentCaptchaVerifier {
	return &tencentCaptchaVerifier{newClient: newTencentCaptchaSDKClient}
}

// newTencentCaptchaSDKClient 接入点由调用方按站点下发（中国站 / 国际站）。
// service 层的 tencentCaptchaEndpoint 已保证 endpoint 非空；即便为空，
// 腾讯 SDK 也会回落到服务默认域 captcha.tencentcloudapi.com（即中国站），不会失败。
func newTencentCaptchaSDKClient(secretID, secretKey, endpoint string) (tencentCaptchaAPI, error) {
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = endpoint
	clientProfile.HttpProfile.ReqMethod = "POST"
	clientProfile.HttpProfile.ReqTimeout = 5
	return captcha.NewClient(common.NewCredential(secretID, secretKey), "", clientProfile)
}

func (v *tencentCaptchaVerifier) VerifyTicket(ctx context.Context, credentials identity.TencentCaptchaCredentials, proof identity.TencentCaptchaProof, remoteIP string) (*identity.TencentCaptchaVerifyResponse, error) {
	client, err := v.newClient(credentials.CloudSecretID, credentials.CloudSecretKey, credentials.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("create tencent captcha client: %w", err)
	}
	request := captcha.NewDescribeCaptchaResultRequest()
	request.CaptchaType = common.Uint64Ptr(9)
	request.Ticket = common.StringPtr(proof.Ticket)
	request.UserIp = common.StringPtr(remoteIP)
	request.Randstr = common.StringPtr(proof.Randstr)
	request.CaptchaAppId = common.Uint64Ptr(credentials.AppID)
	request.AppSecretKey = common.StringPtr(credentials.AppSecretKey)

	response, err := client.DescribeCaptchaResultWithContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("describe captcha result: %w", err)
	}
	if response == nil || response.Response == nil {
		return nil, fmt.Errorf("describe captcha result: empty response")
	}
	return &identity.TencentCaptchaVerifyResponse{
		CaptchaCode: valueOrZero(response.Response.CaptchaCode),
		CaptchaMsg:  valueOrEmpty(response.Response.CaptchaMsg),
		RequestID:   valueOrEmpty(response.Response.RequestId),
	}, nil
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
