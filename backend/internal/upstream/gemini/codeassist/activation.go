// Code Assist 激活诊断只读取 Google 错误报文。
package codeassist

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// ExtractActivationURL extracts the API activation URL from error details
func ExtractActivationURL(body string) string {
	var errResp google.ErrorResponse
	if err := json.Unmarshal([]byte(body), &errResp); err != nil {
		return ""
	}

	// Check error details for activation URL
	for _, detailRaw := range errResp.Error.Details {
		// Parse as google.ErrorDetailInfo
		var info google.ErrorDetailInfo
		if err := json.Unmarshal(detailRaw, &info); err == nil {
			if info.Metadata != nil {
				if activationURL, ok := info.Metadata["activationUrl"]; ok && activationURL != "" {
					return activationURL
				}
			}
		}

		// Parse as google.ErrorHelp
		var help google.ErrorHelp
		if err := json.Unmarshal(detailRaw, &help); err == nil {
			for _, link := range help.Links {
				if strings.Contains(link.Description, "activation") ||
					strings.Contains(link.Description, "API activation") ||
					strings.Contains(link.URL, "/apis/api/") {
					return link.URL
				}
			}
		}
	}

	return ""
}

// IsServiceDisabledError checks if the error is a SERVICE_DISABLED error
func IsServiceDisabledError(body string) bool {
	var errResp google.ErrorResponse
	if err := json.Unmarshal([]byte(body), &errResp); err != nil {
		return false
	}

	// Check if it's a 403 PERMISSION_DENIED with SERVICE_DISABLED reason
	if errResp.Error.Code != 403 || errResp.Error.Status != "PERMISSION_DENIED" {
		return false
	}

	for _, detailRaw := range errResp.Error.Details {
		var info google.ErrorDetailInfo
		if err := json.Unmarshal(detailRaw, &info); err == nil {
			if info.Reason == "SERVICE_DISABLED" {
				return true
			}
		}
	}

	return false
}
