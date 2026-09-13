// 本文件维护 transfer 的所属能力；兼容入口复用唯一实现。
package transfer

// OpenAIOAuthImportAccountDefaults 是 OpenAI OAuth 导入模板允许填充的账号字段。
type OpenAIOAuthImportAccountDefaults struct {
	Notes              *string  `json:"notes,omitempty"`
	Concurrency        *int     `json:"concurrency,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	RateMultiplier     *float64 `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64   `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool    `json:"auto_pause_on_expired,omitempty"`
}

// OpenAIOAuthImportDefaults 是 OpenAI OAuth 账号导入时的缺省模板。
type OpenAIOAuthImportDefaults struct {
	Account     OpenAIOAuthImportAccountDefaults `json:"account,omitempty"`
	Credentials map[string]any                   `json:"credentials,omitempty"`
	Extra       map[string]any                   `json:"extra,omitempty"`
}
