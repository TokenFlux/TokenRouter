// 激活诊断的原断言随唯一平台实现迁移。
package codeassist

import "testing"

func TestExtractActivationURL(t *testing.T) {
	// Test case from the user's error message
	errorBody := `{
		"error": {
			"code": 403,
			"message": "Gemini for Google Cloud API has not been used in project project-6eca5881-ab73-4736-843 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/cloudaicompanion.googleapis.com/overview?project=project-6eca5881-ab73-4736-843 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry.",
			"status": "PERMISSION_DENIED",
			"details": [
				{
					"@type": "type.googleapis.com/google.rpc.ErrorInfo",
					"reason": "SERVICE_DISABLED",
					"domain": "googleapis.com",
					"metadata": {
						"service": "cloudaicompanion.googleapis.com",
						"activationUrl": "https://console.developers.google.com/apis/api/cloudaicompanion.googleapis.com/overview?project=project-6eca5881-ab73-4736-843",
						"consumer": "projects/project-6eca5881-ab73-4736-843",
						"serviceTitle": "Gemini for Google Cloud API",
						"containerInfo": "project-6eca5881-ab73-4736-843"
					}
				},
				{
					"@type": "type.googleapis.com/google.rpc.LocalizedMessage",
					"locale": "en-US",
					"message": "Gemini for Google Cloud API has not been used in project project-6eca5881-ab73-4736-843 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/cloudaicompanion.googleapis.com/overview?project=project-6eca5881-ab73-4736-843 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry."
				},
				{
					"@type": "type.googleapis.com/google.rpc.Help",
					"links": [
						{
							"description": "Google developers console API activation",
							"url": "https://console.developers.google.com/apis/api/cloudaicompanion.googleapis.com/overview?project=project-6eca5881-ab73-4736-843"
						}
					]
				}
			]
		}
	}`

	activationURL := ExtractActivationURL(errorBody)
	expectedURL := "https://console.developers.google.com/apis/api/cloudaicompanion.googleapis.com/overview?project=project-6eca5881-ab73-4736-843"

	if activationURL != expectedURL {
		t.Errorf("Expected activation URL %s, got %s", expectedURL, activationURL)
	}
}
func TestIsServiceDisabledError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name: "SERVICE_DISABLED error",
			body: `{
				"error": {
					"code": 403,
					"status": "PERMISSION_DENIED",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.ErrorInfo",
							"reason": "SERVICE_DISABLED"
						}
					]
				}
			}`,
			expected: true,
		},
		{
			name: "Other 403 error",
			body: `{
				"error": {
					"code": 403,
					"status": "PERMISSION_DENIED",
					"details": [
						{
							"@type": "type.googleapis.com/google.rpc.ErrorInfo",
							"reason": "OTHER_REASON"
						}
					]
				}
			}`,
			expected: false,
		},
		{
			name: "404 error",
			body: `{
				"error": {
					"code": 404,
					"status": "NOT_FOUND"
				}
			}`,
			expected: false,
		},
		{
			name:     "Invalid JSON",
			body:     `invalid json`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsServiceDisabledError(tt.body)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
