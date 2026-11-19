package codeassist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// client.go sends Code Assist account discovery and onboarding requests using
// Gemini CLI authentication headers.
// Code Assist discovery and onboarding use v1internal account endpoints with
// the Gemini CLI client identity. Missing request inputs select the
// Antigravity metadata defaults, while provider authorization interprets
// returned tier/project information and handles unfinished onboarding
// operations.

// DefaultTestModel supplies the preselected Gemini model used by provider
// connection tests when their caller has not selected another test model.
const DefaultTestModel = "gemini-2.0-flash"

// Client exchanges Code Assist account requests against its configured
// endpoint prefix.
type Client struct{ BaseURL string } // BaseURL is the account API endpoint prefix.

type (
	// LoadCodeAssistRequest carries project hints and client metadata for account
	// discovery.
	LoadCodeAssistRequest = wire.LoadCodeAssistRequest
	// LoadCodeAssistMetadata describes the IDE, platform, and plugin used for
	// account discovery.
	LoadCodeAssistMetadata = wire.LoadCodeAssistMetadata
	// TierInfo represents a tier identifier and metadata returned by Code Assist.
	TierInfo = wire.TierInfo
	// LoadCodeAssistResponse describes account tiers and the discovered companion
	// project.
	LoadCodeAssistResponse = wire.LoadCodeAssistResponse
)

type (
	// OnboardUserRequest carries the selected tier and client metadata for
	// enrollment.
	OnboardUserRequest = wire.OnboardUserRequest
	// OnboardUserResponse represents onboarding completion and its optional
	// companion project result.
	OnboardUserResponse = wire.OnboardUserResponse
)

// NewClient creates a Code Assist client using the Gemini CLI API endpoint.
func NewClient() *Client { return &Client{BaseURL: GeminiCliBaseURL} }

// LoadCodeAssist returns decoded tier and project observations for a bearer
// token. A nil reqBody selects default client metadata; disabled-service
// failures return activation guidance, while other failed HTTP statuses
// include bounded log-body text.
func (c *Client) LoadCodeAssist(ctx context.Context, accessToken, proxyURL string, reqBody *LoadCodeAssistRequest) (*LoadCodeAssistResponse, error) {
	if reqBody == nil {
		reqBody = defaultLoadCodeAssistRequest()
	}

	var out LoadCodeAssistResponse
	statusCode, bodyBytes, err := c.postCodeAssistJSON(ctx, accessToken, proxyURL, "/v1internal:loadCodeAssist", reqBody)
	if err != nil {
		fmt.Printf("[CodeAssist] LoadCodeAssist request error: %v\n", err)
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		body := string(bodyBytes)
		sanitizedBody := SanitizeBodyForLogs(body)
		fmt.Printf("[CodeAssist] LoadCodeAssist failed: status %d, body: %s\n", statusCode, sanitizedBody)

		// Translate a disabled-service envelope into activation guidance so account
		// authorization can show the required project setup step; other failures
		// retain bounded diagnostics.
		if IsServiceDisabledError(body) {
			activationURL := ExtractActivationURL(body)
			if activationURL != "" {
				return nil, fmt.Errorf("gemini API not enabled for this project, please enable it by visiting: %s\n\nAfter enabling the API, wait a few minutes for the changes to propagate, then try again", activationURL)
			}
			return nil, fmt.Errorf("gemini API not enabled for this project, please enable it in the Google Cloud Console at: https://console.cloud.google.com/apis/library/cloudaicompanion.googleapis.com")
		}

		return nil, fmt.Errorf("loadCodeAssist failed: status %d, body: %s", statusCode, sanitizedBody)
	}
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("decode loadCodeAssist response: %w", err)
	}
	fmt.Printf("[CodeAssist] LoadCodeAssist success: status %d, response: %+v\n", statusCode, out)
	return &out, nil
}

// OnboardUser posts one enrollment observation using the bearer token and CLI
// identity. A nil reqBody selects LEGACY metadata; callers inspect Done and
// project output and perform any subsequent polling.
func (c *Client) OnboardUser(ctx context.Context, accessToken, proxyURL string, reqBody *OnboardUserRequest) (*OnboardUserResponse, error) {
	if reqBody == nil {
		reqBody = defaultOnboardUserRequest()
	}

	fmt.Printf("[CodeAssist] OnboardUser request body: %+v\n", reqBody)

	var out OnboardUserResponse
	statusCode, bodyBytes, err := c.postCodeAssistJSON(ctx, accessToken, proxyURL, "/v1internal:onboardUser", reqBody)
	if err != nil {
		fmt.Printf("[CodeAssist] OnboardUser request error: %v\n", err)
		return nil, err
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		body := string(bodyBytes)
		sanitizedBody := SanitizeBodyForLogs(body)
		fmt.Printf("[CodeAssist] OnboardUser failed: status %d, body: %s\n", statusCode, sanitizedBody)

		// Translate a disabled-service envelope into activation guidance so account
		// authorization can show the required project setup step; other failures
		// retain bounded diagnostics.
		if IsServiceDisabledError(body) {
			activationURL := ExtractActivationURL(body)
			if activationURL != "" {
				return nil, fmt.Errorf("gemini API not enabled for this project, please enable it by visiting: %s\n\nAfter enabling the API, wait a few minutes for the changes to propagate, then try again", activationURL)
			}
			return nil, fmt.Errorf("gemini API not enabled for this project, please enable it in the Google Cloud Console at: https://console.cloud.google.com/apis/library/cloudaicompanion.googleapis.com")
		}

		return nil, fmt.Errorf("onboardUser failed: status %d, body: %s", statusCode, sanitizedBody)
	}
	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return nil, fmt.Errorf("decode onboardUser response: %w", err)
	}
	fmt.Printf("[CodeAssist] OnboardUser success: status %d, response: %+v\n", statusCode, out)
	return &out, nil
}

// postCodeAssistJSON marshals reqBody and executes a POST through the shared
// thirty-second HTTP client pool for proxyURL with a 4 MiB response cap.
func (c *Client) postCodeAssistJSON(ctx context.Context, accessToken, proxyURL, path string, reqBody any) (int, []byte, error) {
	client, err := createGeminiCliHTTPClient(proxyURL)
	if err != nil {
		return 0, nil, fmt.Errorf("create HTTP client: %w", err)
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
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

// createGeminiCliHTTPClient selects the shared HTTP client for proxyURL with a
// thirty-second timeout. Construction errors return to the account operation.
func createGeminiCliHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  30 * time.Second,
	})
}

// defaultLoadCodeAssistRequest returns fresh Antigravity metadata for Code
// Assist discovery.
func defaultLoadCodeAssistRequest() *LoadCodeAssistRequest {
	return &LoadCodeAssistRequest{
		Metadata: LoadCodeAssistMetadata{
			IDEType:    "ANTIGRAVITY",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}
}

// defaultOnboardUserRequest returns fresh LEGACY enrollment metadata for the
// Gemini plugin.
func defaultOnboardUserRequest() *OnboardUserRequest {
	return &OnboardUserRequest{
		TierID: "LEGACY",
		Metadata: LoadCodeAssistMetadata{
			IDEType:    "ANTIGRAVITY",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}
}
