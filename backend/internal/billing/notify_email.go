// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	json "encoding/json"
	strings "strings"
)

// parseNotifyEmails parses a JSON string into []NotifyEmailSummary.
// It auto-detects the format:
//   - Old format ["email1","email2"] → converted to [{email, disabled:false, verified:true}, ...]
//   - New format [{email,disabled,verified}, ...] → parsed directly
//
// Returns nil on empty/invalid input.
func ParseNotifyEmails(raw string) []NotifyEmailSummary {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}

	// Try parsing as new format first (array of objects)
	var entries []NotifyEmailSummary
	if err := json.Unmarshal([]byte(raw), &entries); err == nil && len(entries) > 0 {
		// Verify it's actually the new format by checking the first element
		// json.Unmarshal into []NotifyEmailSummary succeeds even for ["string"]
		// because it tries to fit "string" into NotifyEmailSummary and gets zero values.
		// We need to detect old format explicitly.
		if !isOldStringArrayFormat(raw) {
			return entries
		}
	}

	// Try parsing as old format (array of strings)
	var emails []string
	if err := json.Unmarshal([]byte(raw), &emails); err == nil {
		result := make([]NotifyEmailSummary, 0, len(emails))
		for _, e := range emails {
			e = strings.TrimSpace(e)
			if e != "" {
				result = append(result, NotifyEmailSummary{
					Email:    e,
					Disabled: false,
					Verified: false, // Old format emails default to unverified
				})
			}
		}
		return result
	}

	return nil
}

// isOldStringArrayFormat checks if the JSON is a string array like ["email1","email2"].
func isOldStringArrayFormat(raw string) bool {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return false
	}
	// Check if first element starts with a quote (string) vs { (object)
	first := strings.TrimSpace(string(arr[0]))
	return len(first) > 0 && first[0] == '"'
}

// marshalNotifyEmails serializes []NotifyEmailSummary to JSON string.
func MarshalNotifyEmails(entries []NotifyEmailSummary) string {
	if len(entries) == 0 {
		return "[]"
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(data)
}
