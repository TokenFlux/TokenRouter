package google

import (
	"encoding/json"
	"fmt"
)

// ErrorResponse represents a Google API error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains the error details from Google API
type ErrorDetail struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Status  string            `json:"status"`
	Details []json.RawMessage `json:"details,omitempty"`
}

// ErrorDetailInfo contains additional error information
type ErrorDetailInfo struct {
	Type     string            `json:"@type"`
	Reason   string            `json:"reason,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ErrorHelp contains help links
type ErrorHelp struct {
	Type  string     `json:"@type"`
	Links []HelpLink `json:"links,omitempty"`
}

// HelpLink represents a help link
type HelpLink struct {
	Description string `json:"description"`
	URL         string `json:"url"`
}

// ParseError parses a Google API error response and extracts key information
func ParseError(body string) (*ErrorResponse, error) {
	var errResp ErrorResponse
	if err := json.Unmarshal([]byte(body), &errResp); err != nil {
		return nil, fmt.Errorf("failed to parse error response: %w", err)
	}
	return &errResp, nil
}
