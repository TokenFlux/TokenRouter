package dto

// OverloadCooldownSettings 保留现有 HTTP JSON 字段与省略语义。
type OverloadCooldownSettings struct {
	Enabled         bool `json:"enabled"`
	CooldownMinutes int  `json:"cooldown_minutes"`
}

// OpenAI403CooldownSettings 保留现有 HTTP JSON 字段与省略语义。
type OpenAI403CooldownSettings struct {
	Enabled                 bool `json:"enabled"`
	CooldownMinutes         int  `json:"cooldown_minutes"`
	ErrorOnThresholdEnabled bool `json:"error_on_threshold_enabled"`
	ThresholdCount          int  `json:"threshold_count"`
	ThresholdWindowMinutes  int  `json:"threshold_window_minutes"`
}

// RateLimit429CooldownSettings 保留现有 HTTP JSON 字段与省略语义。
type RateLimit429CooldownSettings struct {
	Enabled         bool `json:"enabled"`
	CooldownSeconds int  `json:"cooldown_seconds"`
}

// OpenAIImagesOAuthUnavailableCooldownSettings 保留现有 HTTP JSON 字段与省略语义。
type OpenAIImagesOAuthUnavailableCooldownSettings struct {
	CooldownMinutes int `json:"cooldown_minutes"`
}

// OpenAIOAuthImportAccountDefaults 保留现有 HTTP JSON 字段与省略语义。
type OpenAIOAuthImportAccountDefaults struct {
	Notes              *string  `json:"notes,omitempty"`
	Concurrency        *int     `json:"concurrency,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	RateMultiplier     *float64 `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64   `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool    `json:"auto_pause_on_expired,omitempty"`
}

// OpenAIOAuthImportDefaults 保留现有 HTTP JSON 字段与省略语义。
type OpenAIOAuthImportDefaults struct {
	Account     OpenAIOAuthImportAccountDefaults `json:"account,omitempty"`
	Credentials map[string]any                   `json:"credentials,omitempty"`
	Extra       map[string]any                   `json:"extra,omitempty"`
}

// StreamTimeoutSettings 保留现有 HTTP JSON 字段与省略语义。
type StreamTimeoutSettings struct {
	Enabled                bool   `json:"enabled"`
	Action                 string `json:"action"`
	TempUnschedMinutes     int    `json:"temp_unsched_minutes"`
	ThresholdCount         int    `json:"threshold_count"`
	ThresholdWindowMinutes int    `json:"threshold_window_minutes"`
}
