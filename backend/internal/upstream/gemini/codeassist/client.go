package codeassist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// client.go sends Code Assist account discovery, onboarding, and long-running
// operation polling requests using Gemini CLI authentication headers.
// Code Assist discovery and onboarding target v1internal account endpoints with
// the official Gemini CLI client metadata. Missing request inputs select
// IDE_UNSPECIFIED and legacy-tier defaults, while GetOperation polls pending
// onboarding operations via GET /v1internal/{operationName}.

const (
	// DefaultTestModel supplies the preselected Gemini model used by provider
	// connection tests when their caller has not selected another test model.
	DefaultTestModel = "gemini-2.0-flash"

	// DefaultIDEType, DefaultPlatform, DefaultPluginType, and DefaultOnboardTierID
	// match the official google-gemini/gemini-cli Code Assist setup metadata.
	DefaultIDEType       = "IDE_UNSPECIFIED"
	DefaultPlatform      = "PLATFORM_UNSPECIFIED"
	DefaultPluginType    = "GEMINI"
	DefaultOnboardTierID = "legacy-tier"
)

// Client exchanges Code Assist account requests against its configured
// endpoint prefix.
type Client struct{ BaseURL string } // BaseURL is the account API endpoint prefix.

type (
	// LoadCodeAssistRequest carries project hints and client metadata for account
	// discovery.
	LoadCodeAssistRequest = wire.LoadCodeAssistRequest
	// LoadCodeAssistMetadata describes the IDE, platform, plugin, and project
	// hint used for account discovery.
	LoadCodeAssistMetadata = wire.LoadCodeAssistMetadata
	// TierInfo represents a tier identifier and metadata returned by Code Assist.
	TierInfo = wire.TierInfo
	// AllowedTier represents an onboarding tier advertised by Code Assist.
	AllowedTier = wire.AllowedTier
	// IneligibleTier represents an unavailable tier and its rejection details.
	IneligibleTier = wire.IneligibleTier
	// LoadCodeAssistResponse describes account tiers, ineligible tiers, and the
	// discovered companion project.
	LoadCodeAssistResponse = wire.LoadCodeAssistResponse
)

type (
	// OnboardUserRequest carries the selected tier, optional project ID, and
	// client metadata for enrollment.
	OnboardUserRequest = wire.OnboardUserRequest
	// OnboardUserResponse represents onboarding completion and its optional
	// companion project result.
	OnboardUserResponse = wire.OnboardUserResponse
)

// NewClient creates a Code Assist client using the Gemini CLI API endpoint.
func NewClient() *Client { return &Client{BaseURL: GeminiCliBaseURL} }

// LoadCodeAssist returns decoded tier and project observations for a bearer
// token. A nil reqBody selects default Gemini CLI metadata; disabled-service
// failures return activation guidance, while other failed HTTP statuses
// include bounded log-body text.
func (c *Client) LoadCodeAssist(ctx context.Context, accessToken, proxyURL string, reqBody *LoadCodeAssistRequest) (*LoadCodeAssistResponse, error) {
	if reqBody == nil {
		reqBody = defaultLoadCodeAssistRequest()
	}

	statusCode, bodyBytes, err := c.doCodeAssistJSON(ctx, http.MethodPost, accessToken, proxyURL, "/v1internal:loadCodeAssist", reqBody)
	if err != nil {
		logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] LoadCodeAssist request error: %v", err)
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, formatCodeAssistHTTPError("loadCodeAssist", statusCode, bodyBytes)
	}

	var out LoadCodeAssistResponse
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("decode loadCodeAssist response: %w", err)
	}
	logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] LoadCodeAssist success: status %d, tier=%s, hasProject=%t, ineligible=%d", statusCode, out.GetTier(), strings.TrimSpace(out.CloudAICompanionProject) != "", len(out.IneligibleTiers))
	return &out, nil
}

// OnboardUser posts one enrollment request using the bearer token and CLI
// identity. A nil reqBody selects default legacy-tier metadata; callers inspect
// Done and Name and poll GetOperation until completion.
func (c *Client) OnboardUser(ctx context.Context, accessToken, proxyURL string, reqBody *OnboardUserRequest) (*OnboardUserResponse, error) {
	if reqBody == nil {
		reqBody = defaultOnboardUserRequest()
	}

	statusCode, bodyBytes, err := c.doCodeAssistJSON(ctx, http.MethodPost, accessToken, proxyURL, "/v1internal:onboardUser", reqBody)
	if err != nil {
		logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] OnboardUser request error: %v", err)
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, formatCodeAssistHTTPError("onboardUser", statusCode, bodyBytes)
	}

	var out OnboardUserResponse
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("decode onboardUser response: %w", err)
	}
	logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] OnboardUser success: status %d, done=%t, name=%s", statusCode, out.Done, out.Name)
	return &out, nil
}

// GetOperation polls a pending Code Assist onboarding operation via
// GET /v1internal/{operationName}. Leading slashes and a redundant v1internal/
// prefix are normalized, while malformed or traversal operation names return an
// error before issuing a network request.
func (c *Client) GetOperation(ctx context.Context, accessToken, proxyURL, operationName string) (*OnboardUserResponse, error) {
	normalized, err := normalizeOperationName(operationName)
	if err != nil {
		return nil, err
	}

	statusCode, bodyBytes, err := c.doCodeAssistJSON(ctx, http.MethodGet, accessToken, proxyURL, "/v1internal/"+normalized, nil)
	if err != nil {
		logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] GetOperation request error: %v", err)
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, formatCodeAssistHTTPError("getOperation", statusCode, bodyBytes)
	}

	var out OnboardUserResponse
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("decode getOperation response: %w", err)
	}
	logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] GetOperation success: status %d, done=%t, name=%s", statusCode, out.Done, out.Name)
	return &out, nil
}

// doCodeAssistJSON executes an authenticated GET or JSON POST through the
// shared thirty-second HTTP client pool for proxyURL with a 4 MiB response cap.
func (c *Client) doCodeAssistJSON(ctx context.Context, method, accessToken, proxyURL, path string, reqBody any) (int, []byte, error) {
	client, err := createGeminiCliHTTPClient(proxyURL)
	if err != nil {
		return 0, nil, fmt.Errorf("create HTTP client: %w", err)
	}
	var bodyReader io.Reader
	if reqBody != nil {
		encoded, err := json.Marshal(reqBody)
		if err != nil {
			return 0, nil, fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(encoded)
	}

	baseURL := strings.TrimRight(c.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", GeminiCLIUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap Code Assist management replies at 4 MiB to bound buffered error and JSON reads.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	return resp.StatusCode, bodyBytes, nil
}

// formatCodeAssistHTTPError logs a sanitized error body and converts
// SERVICE_DISABLED responses into actionable Cloud Console activation guidance.
func formatCodeAssistHTTPError(operation string, statusCode int, bodyBytes []byte) error {
	body := string(bodyBytes)
	sanitizedBody := SanitizeBodyForLogs(body)
	logging.LegacyPrintf("upstream.gemini_codeassist", "[CodeAssist] %s failed: status %d, body: %s", operation, statusCode, sanitizedBody)

	// Translate a disabled-service envelope into activation guidance so account
	// authorization can show the required project setup step; other failures
	// retain bounded diagnostics.
	if IsServiceDisabledError(body) {
		if activationURL := ExtractActivationURL(body); activationURL != "" {
			return fmt.Errorf("gemini API not enabled for this project, please enable it by visiting: %s\n\nAfter enabling the API, wait a few minutes for the changes to propagate, then try again", activationURL)
		}
		return fmt.Errorf("gemini API not enabled for this project, please enable it in the Google Cloud Console at: https://console.cloud.google.com/apis/library/cloudaicompanion.googleapis.com")
	}

	return fmt.Errorf("%s failed: status %d, body: %s", operation, statusCode, sanitizedBody)
}

// normalizeOperationName strips optional leading slashes and v1internal/
// prefixes and verifies that operationName stays within operations/.
func normalizeOperationName(operationName string) (string, error) {
	trimmed := strings.TrimSpace(operationName)
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, "v1internal/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" || !strings.HasPrefix(trimmed, "operations/") {
		return "", fmt.Errorf("invalid operation name %q: must start with operations/", operationName)
	}
	if len(trimmed) <= len("operations/") || strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "?#\\ \t\r\n") {
		return "", fmt.Errorf("invalid operation name %q", operationName)
	}
	return trimmed, nil
}

// createGeminiCliHTTPClient selects the shared HTTP client for proxyURL with a
// thirty-second timeout. Construction errors return to the account operation.
func createGeminiCliHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  30 * time.Second,
	})
}

// defaultLoadCodeAssistRequest returns fresh Gemini CLI metadata for Code
// Assist discovery.
func defaultLoadCodeAssistRequest() *LoadCodeAssistRequest {
	return &LoadCodeAssistRequest{
		Metadata: LoadCodeAssistMetadata{
			IDEType:    DefaultIDEType,
			Platform:   DefaultPlatform,
			PluginType: DefaultPluginType,
		},
	}
}

// defaultOnboardUserRequest returns fresh legacy-tier enrollment metadata for
// the Gemini CLI plugin.
func defaultOnboardUserRequest() *OnboardUserRequest {
	return &OnboardUserRequest{
		TierID: DefaultOnboardTierID,
		Metadata: LoadCodeAssistMetadata{
			IDEType:    DefaultIDEType,
			Platform:   DefaultPlatform,
			PluginType: DefaultPluginType,
		},
	}
}
