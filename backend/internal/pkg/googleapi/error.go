package googleapi

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// 错误 wire 类型由 protocol/google 唯一拥有，激活诊断留 S09 迁移。
type ErrorResponse = google.ErrorResponse
type ErrorDetail = google.ErrorDetail
type ErrorDetailInfo = google.ErrorDetailInfo
type ErrorHelp = google.ErrorHelp
type HelpLink = google.HelpLink

func ParseError(body string) (*ErrorResponse, error) { return google.ParseError(body) }

// ExtractActivationURL extracts the API activation URL from error details
func ExtractActivationURL(body string) string {
	var errResp ErrorResponse
	if err := json.Unmarshal([]byte(body), &errResp); err != nil {
		return ""
	}

	// Check error details for activation URL
	for _, detailRaw := range errResp.Error.Details {
		// Parse as ErrorDetailInfo
		var info ErrorDetailInfo
		if err := json.Unmarshal(detailRaw, &info); err == nil {
			if info.Metadata != nil {
				if activationURL, ok := info.Metadata["activationUrl"]; ok && activationURL != "" {
					return activationURL
				}
			}
		}

		// Parse as ErrorHelp
		var help ErrorHelp
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
	var errResp ErrorResponse
	if err := json.Unmarshal([]byte(body), &errResp); err != nil {
		return false
	}

	// Check if it's a 403 PERMISSION_DENIED with SERVICE_DISABLED reason
	if errResp.Error.Code != 403 || errResp.Error.Status != "PERMISSION_DENIED" {
		return false
	}

	for _, detailRaw := range errResp.Error.Details {
		var info ErrorDetailInfo
		if err := json.Unmarshal(detailRaw, &info); err == nil {
			if info.Reason == "SERVICE_DISABLED" {
				return true
			}
		}
	}

	return false
}
