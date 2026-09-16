// Batch 技术客户端只接收 URL、HTTP 客户端与错误契约，不持有任务或业务账号。
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"

	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

type GeminiUploadedFile = wire.GeminiUploadedFile
type GeminiBatchJob = wire.GeminiBatchJob
type GeminiBatchDest = wire.GeminiBatchDest
type GeminiBatchResponse = wire.GeminiBatchResponse
type GeminiBatchError = wire.GeminiBatchError
type GeminiBatchHTTPClient struct {
	missingAPIKey error
	baseURL       string
	client        *http.Client
}

func NewGeminiBatchHTTPClient(baseURL string, client *http.Client, missingAPIKey error) *GeminiBatchHTTPClient {
	if missingAPIKey == nil {
		missingAPIKey = apperror.BadRequest("BATCH_IMAGE_PROVIDER_MISSING_API_KEY", "batch image provider account is missing api key")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = codeassist.AIStudioBaseURL
	}
	if client == nil {
		client = httpclient.DefaultBatchHTTPClient()
	}
	return &GeminiBatchHTTPClient{baseURL: baseURL, client: client, missingAPIKey: missingAPIKey}
}
func (c *GeminiBatchHTTPClient) UploadJSONL(ctx context.Context, apiKey string, displayName string, r io.Reader) (*GeminiUploadedFile, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeader := textproto.MIMEHeader{}
	metadataHeader.Set("Content-Disposition", `form-data; name="metadata"`)
	metadataHeader.Set("Content-Type", "application/json; charset=utf-8")
	metadataPart, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{"file": map[string]any{"displayName": displayName, "mimeType": "application/jsonl"}}
	if err := json.NewEncoder(metadataPart).Encode(metadata); err != nil {
		return nil, err
	}
	fileHeader := textproto.MIMEHeader{}
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="batch.jsonl"`)
	fileHeader.Set("Content-Type", "application/jsonl")
	filePart, err := writer.CreatePart(fileHeader)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(filePart, r); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/upload/v1beta/files?uploadType=multipart", apiKey, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	var resp struct {
		File *GeminiUploadedFile `json:"file"`
		*GeminiUploadedFile
	}
	if err := c.doJSON(req, &resp); err != nil {
		return nil, err
	}
	if resp.File != nil {
		return resp.File, nil
	}
	return resp.GeminiUploadedFile, nil
}
func (c *GeminiBatchHTTPClient) CreateBatch(ctx context.Context, apiKey string, model string, fileName string, displayName string) (*GeminiBatchJob, error) {
	body := map[string]any{
		"batch": map[string]any{
			"displayName": displayName,
			"inputConfig": map[string]any{
				"fileName": fileName,
			},
		},
	}
	payload, _ := json.Marshal(body)
	path := fmt.Sprintf("/v1beta/models/%s:batchGenerateContent", url.PathEscape(strings.TrimSpace(model)))
	req, err := c.newRequest(ctx, http.MethodPost, path, apiKey, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doBatchJob(req)
}
func (c *GeminiBatchHTTPClient) GetBatch(ctx context.Context, apiKey string, batchName string) (*GeminiBatchJob, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1beta/"+strings.TrimLeft(batchName, "/"), apiKey, nil)
	if err != nil {
		return nil, err
	}
	return c.doBatchJob(req)
}
func (c *GeminiBatchHTTPClient) CancelBatch(ctx context.Context, apiKey string, batchName string) error {
	req, err := c.newRequest(ctx, http.MethodPost, "/v1beta/"+strings.TrimLeft(batchName, "/")+":cancel", apiKey, nil)
	if err != nil {
		return err
	}
	return c.doNoBody(req)
}
func (c *GeminiBatchHTTPClient) DownloadFile(ctx context.Context, apiKey string, fileName string) (io.ReadCloser, string, error) {
	metaReq, err := c.newRequest(ctx, http.MethodGet, "/v1beta/"+strings.TrimLeft(fileName, "/"), apiKey, nil)
	if err != nil {
		return nil, "", err
	}
	var metadata struct {
		DownloadURI string `json:"downloadUri"`
		DownloadURL string `json:"download_url"`
		MimeType    string `json:"mimeType"`
	}
	if err := c.doJSON(metaReq, &metadata); err != nil {
		return nil, "", err
	}
	downloadURL := strings.TrimSpace(metadata.DownloadURI)
	if downloadURL == "" {
		downloadURL = strings.TrimSpace(metadata.DownloadURL)
	}
	if downloadURL == "" {
		downloadURL = c.baseURL + "/v1beta/" + strings.TrimLeft(fileName, "/") + ":download"
	}
	// 纵深加固：downloadUri 来自上游响应，跟随前校验目标 host，
	// 防止异常/被劫持的响应把带 api key 的请求带到任意主机。
	if err := validateGeminiDownloadHost(downloadURL, c.baseURL); err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, "", readGeminiAPIError(resp)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = metadata.MimeType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return resp.Body, contentType, nil
}
func (c *GeminiBatchHTTPClient) DeleteFile(ctx context.Context, apiKey string, fileName string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, "/v1beta/"+strings.TrimLeft(fileName, "/"), apiKey, nil)
	if err != nil {
		return err
	}
	return c.doNoBody(req)
}
func (c *GeminiBatchHTTPClient) doBatchJob(req *http.Request) (*GeminiBatchJob, error) {
	var job GeminiBatchJob
	if err := c.doJSON(req, &job); err != nil {
		return nil, err
	}
	job.Raw = map[string]any{}
	return &job, nil
}
func (c *GeminiBatchHTTPClient) doNoBody(req *http.Request) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readGeminiAPIError(resp)
	}
	return nil
}
func (c *GeminiBatchHTTPClient) doJSON(req *http.Request, out any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readGeminiAPIError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
func (c *GeminiBatchHTTPClient) newRequest(ctx context.Context, method, path, apiKey string, body io.Reader) (*http.Request, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, c.missingAPIKey
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-goog-api-key", apiKey)
	return req, nil
}

// validateGeminiDownloadHost 只允许跟随到 googleapis.com（含子域）
// 或与配置的 baseURL 同 host 的下载地址。
func validateGeminiDownloadHost(downloadURL, baseURL string) error {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return ProviderError("GEMINI_INVALID_RESPONSE", "Gemini download uri is invalid", err)
	}
	if parsed.Scheme != "https" {
		return ProviderError("GEMINI_INVALID_RESPONSE", "Gemini download uri must use https", nil)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "googleapis.com" || strings.HasSuffix(host, ".googleapis.com") {
		return nil
	}
	if base, err := url.Parse(baseURL); err == nil && strings.EqualFold(base.Hostname(), host) {
		return nil
	}
	return ProviderError("GEMINI_INVALID_RESPONSE", "Gemini download uri host is not allowed", nil)
}

type GeminiAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *GeminiAPIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" {
		return fmt.Sprintf("gemini api error: status=%d code=%s message=%s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("gemini api error: status=%d message=%s", e.StatusCode, e.Message)
}
func readGeminiAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	message := string(body)
	var parsed struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		message = parsed.Error.Message
		return &GeminiAPIError{StatusCode: resp.StatusCode, Code: parsed.Error.Status, Message: message}
	}
	return &GeminiAPIError{StatusCode: resp.StatusCode, Message: message}
}
func ProviderError(reason, message string, cause error) error {
	err := apperror.New(apperror.CategoryBadGateway, reason, message)
	if cause != nil {
		return err.WithCause(cause)
	}
	return err
}
func MapClientError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *GeminiAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return ProviderError("GEMINI_AUTH_FAILED", "Gemini authentication failed", nil)
		case http.StatusTooManyRequests:
			return ProviderError("GEMINI_RATE_LIMITED", "Gemini rate limit exceeded", nil)
		case http.StatusNotFound:
			return ProviderError("GEMINI_BATCH_NOT_FOUND", "Gemini batch resource was not found", nil)
		default:
			return ProviderError("GEMINI_INVALID_RESPONSE", "Gemini API request failed", nil)
		}
	}
	return ProviderError("GEMINI_INVALID_RESPONSE", "Gemini API request failed", nil)
}
