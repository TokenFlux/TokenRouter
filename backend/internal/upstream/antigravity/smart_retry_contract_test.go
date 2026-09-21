//go:build unit

package antigravity

import (
	"testing"
	"time"
)

func TestParseAntigravitySmartRetryInfo(t *testing.T) {
	tests := []struct {
		name                             string
		body                             string
		expectedDelay                    time.Duration
		expectedModel                    string
		expectedNil                      bool
		expectedIsModelCapacityExhausted bool
	}{
		{
			name: "valid complete response with RATE_LIMIT_EXCEEDED",
			body: `{
				"error": {
					"code": 429,
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.ErrorInfo",
							"domain": "cloudcode-pa.googleapis.com",
							"metadata": {
								"model": "claude-sonnet-4-5",
								"quotaResetDelay": "201.506475ms"
							},
							"reason": "RATE_LIMIT_EXCEEDED"
						},
						{
							"@type": "type.googleapis.com/google.rpc.RetryInfo",
							"retryDelay": "0.201506475s"
						}
					],
					"message": "You have exhausted your capacity on this model.",
					"status": "RESOURCE_EXHAUSTED"
				}
			}`,
			expectedDelay: 201506475 * time.Nanosecond,
			expectedModel: "claude-sonnet-4-5",
		},
		{
			name: "429 RESOURCE_EXHAUSTED without RATE_LIMIT_EXCEEDED - should return nil",
			body: `{
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.ErrorInfo",
							"metadata": {"model": "claude-sonnet-4-5"},
							"reason": "QUOTA_EXCEEDED"
						},
						{
							"@type": "type.googleapis.com/google.rpc.RetryInfo",
							"retryDelay": "3s"
						}
					]
				}
			}`,
			expectedNil: true,
		},
		{
			name: "503 UNAVAILABLE with MODEL_CAPACITY_EXHAUSTED - long delay",
			body: `{
				"error": {
					"code": 503,
					"status": "UNAVAILABLE",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
					],
					"message": "No capacity available for model gemini-3-pro-high on the server"
				}
			}`,
			expectedDelay:                    39 * time.Second,
			expectedModel:                    "gemini-3-pro-high",
			expectedIsModelCapacityExhausted: true,
		},
		{
			name: "503 UNAVAILABLE without MODEL_CAPACITY_EXHAUSTED - should return nil",
			body: `{
				"error": {
					"code": 503,
					"status": "UNAVAILABLE",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-pro"}, "reason": "SERVICE_UNAVAILABLE"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "5s"}
					]
				}
			}`,
			expectedNil: true,
		},
		{
			name: "wrong status - should return nil",
			body: `{
				"error": {
					"code": 429,
					"status": "INVALID_ARGUMENT",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3s"}
					]
				}
			}`,
			expectedNil: true,
		},
		{
			name: "missing status - should return nil",
			body: `{
				"error": {
					"code": 429,
					"details": [
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3s"}
					]
				}
			}`,
			expectedNil: true,
		},
		{
			name: "milliseconds format is now supported",
			body: `{
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "test-model"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "500ms"}
					]
				}
			}`,
			expectedDelay: 500 * time.Millisecond,
			expectedModel: "test-model",
		},
		{
			name: "minutes format is supported",
			body: `{
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "4m50s"}
					]
				}
			}`,
			expectedDelay: 4*time.Minute + 50*time.Second,
			expectedModel: "gemini-3-pro",
		},
		{
			name: "missing model name - should return nil",
			body: `{
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3s"}
					]
				}
			}`,
			expectedNil: true,
		},
		{
			name:        "invalid JSON",
			body:        `not json`,
			expectedNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAntigravitySmartRetryInfo([]byte(tt.body))
			if tt.expectedNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Errorf("expected non-nil result")
				return
			}
			if result.RetryDelay != tt.expectedDelay {
				t.Errorf("RetryDelay = %v, want %v", result.RetryDelay, tt.expectedDelay)
			}
			if result.ModelName != tt.expectedModel {
				t.Errorf("ModelName = %q, want %q", result.ModelName, tt.expectedModel)
			}
			if result.IsModelCapacityExhausted != tt.expectedIsModelCapacityExhausted {
				t.Errorf("IsModelCapacityExhausted = %v, want %v", result.IsModelCapacityExhausted, tt.expectedIsModelCapacityExhausted)
			}
		})
	}
}

func TestShouldTriggerAntigravitySmartRetry(t *testing.T) {
	oauthAccount := true
	setupTokenAccount := true
	upstreamAccount := true
	apiKeyAccount := false

	tests := []struct {
		name                             string
		account                          bool
		body                             string
		expectedShouldRetry              bool
		expectedShouldRateLimit          bool
		expectedIsModelCapacityExhausted bool
		minWait                          time.Duration
		modelName                        string
	}{
		{
			name:    "OAuth account with short delay (< 7s) - smart retry",
			account: oauthAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-opus-4"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
					]
				}
			}`,
			expectedShouldRetry:     true,
			expectedShouldRateLimit: false,
			minWait:                 1 * time.Second, // 0.5s < 1s, 使用最小等待时间 1s
			modelName:               "claude-opus-4",
		},
		{
			name:    "SetupToken account with short delay - smart retry",
			account: setupTokenAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-flash"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "3s"}
					]
				}
			}`,
			expectedShouldRetry:     true,
			expectedShouldRateLimit: false,
			minWait:                 3 * time.Second,
			modelName:               "gemini-3-flash",
		},
		{
			name:    "OAuth account with long delay (>= 7s) - direct rate limit",
			account: oauthAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
					]
				}
			}`,
			expectedShouldRetry:     false,
			expectedShouldRateLimit: true,
			modelName:               "claude-sonnet-4-5",
		},
		{
			name:    "Upstream account with short delay - smart retry",
			account: upstreamAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "2s"}
					]
				}
			}`,
			expectedShouldRetry:     true,
			expectedShouldRateLimit: false,
			minWait:                 2 * time.Second,
			modelName:               "claude-sonnet-4-5",
		},
		{
			name:    "API Key account - should not trigger",
			account: apiKeyAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "test"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "0.5s"}
					]
				}
			}`,
			expectedShouldRetry:     false,
			expectedShouldRateLimit: false,
		},
		{
			name:    "OAuth account with exactly 7s delay - direct rate limit",
			account: oauthAccount,
			body: `{
				"error": {
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-pro"}, "reason": "RATE_LIMIT_EXCEEDED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "7s"}
					]
				}
			}`,
			expectedShouldRetry:     false,
			expectedShouldRateLimit: true,
			minWait:                 7 * time.Second,
			modelName:               "gemini-pro",
		},
		{
			name:    "503 UNAVAILABLE with MODEL_CAPACITY_EXHAUSTED - long delay",
			account: oauthAccount,
			body: `{
				"error": {
					"code": 503,
					"status": "UNAVAILABLE",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
						{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "39s"}
					]
				}
			}`,
			expectedShouldRetry:              true,
			expectedShouldRateLimit:          false,
			expectedIsModelCapacityExhausted: true,
			minWait:                          1 * time.Second,
			modelName:                        "gemini-3-pro-high",
		},
		{
			name:    "503 UNAVAILABLE with MODEL_CAPACITY_EXHAUSTED - no retryDelay - use fixed wait",
			account: oauthAccount,
			body: `{
				"error": {
					"code": 503,
					"status": "UNAVAILABLE",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-2.5-flash"}, "reason": "MODEL_CAPACITY_EXHAUSTED"}
					],
					"message": "No capacity available for model gemini-2.5-flash on the server"
				}
			}`,
			expectedShouldRetry:              true,
			expectedShouldRateLimit:          false,
			expectedIsModelCapacityExhausted: true,
			minWait:                          1 * time.Second,
			modelName:                        "gemini-2.5-flash",
		},
		{
			name:    "429 RESOURCE_EXHAUSTED with RATE_LIMIT_EXCEEDED - no retryDelay - use default rate limit",
			account: oauthAccount,
			body: `{
				"error": {
					"code": 429,
					"status": "RESOURCE_EXHAUSTED",
					"details": [
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"}
					],
					"message": "You have exhausted your capacity on this model."
				}
			}`,
			expectedShouldRetry:     false,
			expectedShouldRateLimit: true,
			minWait:                 30 * time.Second,
			modelName:               "claude-sonnet-4-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldRetry, shouldRateLimit, wait, model, isModelCapacityExhausted := ShouldTriggerAntigravitySmartRetry(tt.account, []byte(tt.body))
			if shouldRetry != tt.expectedShouldRetry {
				t.Errorf("shouldRetry = %v, want %v", shouldRetry, tt.expectedShouldRetry)
			}
			if shouldRateLimit != tt.expectedShouldRateLimit {
				t.Errorf("shouldRateLimit = %v, want %v", shouldRateLimit, tt.expectedShouldRateLimit)
			}
			if isModelCapacityExhausted != tt.expectedIsModelCapacityExhausted {
				t.Errorf("isModelCapacityExhausted = %v, want %v", isModelCapacityExhausted, tt.expectedIsModelCapacityExhausted)
			}
			if shouldRetry {
				if wait < tt.minWait {
					t.Errorf("wait = %v, want >= %v", wait, tt.minWait)
				}
			}
			if shouldRateLimit && tt.minWait > 0 {
				if wait < tt.minWait {
					t.Errorf("rate limit wait = %v, want >= %v", wait, tt.minWait)
				}
			}
			if (shouldRetry || shouldRateLimit) && model != tt.modelName {
				t.Errorf("modelName = %q, want %q", model, tt.modelName)
			}
		})
	}
}
