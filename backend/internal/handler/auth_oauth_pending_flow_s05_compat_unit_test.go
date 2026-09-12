//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

type createPendingOAuthAccountRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	VerifyCode            string `json:"verify_code,omitempty"`
	Password              string `json:"password" binding:"required,min=6"`
	TurnstileToken        string `json:"turnstile_token,omitempty"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket,omitempty"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr,omitempty"`
	InvitationCode        string `json:"invitation_code,omitempty"`
	AffCode               string `json:"aff_code,omitempty"`
	AdoptDisplayName      *bool  `json:"adopt_display_name,omitempty"`
	AdoptAvatar           *bool  `json:"adopt_avatar,omitempty"`
}

type sendPendingOAuthVerifyCodeRequest struct {
	Email                 string `json:"email" binding:"required,email"`
	TurnstileToken        string `json:"turnstile_token,omitempty"`
	TencentCaptchaTicket  string `json:"tencent_captcha_ticket,omitempty"`
	TencentCaptchaRandstr string `json:"tencent_captcha_randstr,omitempty"`
	PendingAuthToken      string `json:"pending_auth_token,omitempty"`
	PendingOAuthToken     string `json:"pending_oauth_token,omitempty"`
}
