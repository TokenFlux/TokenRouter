// Vertex Batch/GCS 只负责技术请求和对象流，任务状态及安全删除编排留调用方。
package vertex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

type VertexBatchClient interface {
	CreateBatchPredictionJob(ctx context.Context, accessToken string, req VertexCreateBatchPredictionJobRequest) (*VertexBatchPredictionJob, error)
	GetBatchPredictionJob(ctx context.Context, accessToken string, name string) (*VertexBatchPredictionJob, error)
	CancelBatchPredictionJob(ctx context.Context, accessToken string, name string) error
}
type VertexBatchObjectStore interface {
	UploadJSONL(ctx context.Context, accessToken string, uri string, r io.Reader) error
	ListJSONLObjects(ctx context.Context, accessToken string, prefixURI string) ([]string, error)
	OpenObject(ctx context.Context, accessToken string, uri string) (io.ReadCloser, string, error)
	DeleteObject(ctx context.Context, accessToken string, uri string) error
	DeletePrefix(ctx context.Context, accessToken string, prefixURI string) error
}
type VertexCreateBatchPredictionJobRequest struct {
	ProjectID      string                     `json:"-"`
	Location       string                     `json:"-"`
	DisplayName    string                     `json:"displayName"`
	Model          string                     `json:"model"`
	InputConfig    VertexBatchInputConfig     `json:"inputConfig"`
	OutputConfig   VertexBatchOutputConfig    `json:"outputConfig"`
	InstanceConfig *VertexBatchInstanceConfig `json:"instanceConfig,omitempty"`
}
type VertexBatchInputConfig struct {
	InstancesFormat string               `json:"instancesFormat"`
	GCSSource       VertexBatchGCSSource `json:"gcsSource"`
}
type VertexBatchGCSSource struct {
	URIs []string `json:"uris"`
}
type VertexBatchOutputConfig struct {
	PredictionsFormat string                    `json:"predictionsFormat"`
	GCSDestination    VertexBatchGCSDestination `json:"gcsDestination"`
}
type VertexBatchGCSDestination struct {
	OutputURIPrefix string `json:"outputUriPrefix"`
}
type VertexBatchInstanceConfig struct {
	KeyField string `json:"keyField"`
}
type VertexBatchPredictionJob struct {
	Name         string                  `json:"name"`
	DisplayName  string                  `json:"displayName"`
	State        string                  `json:"state"`
	OutputConfig VertexBatchOutputConfig `json:"outputConfig"`
	Error        *VertexBatchJobError    `json:"error"`
}
type VertexBatchJobError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func NormalizeVertexBatchModelPath(model string) string {
	model = strings.Trim(strings.TrimSpace(model), "/")
	if strings.HasPrefix(model, "publishers/") || strings.HasPrefix(model, "projects/") {
		return model
	}
	return "publishers/google/models/" + model
}
func BuildVertexBatchPredictionJobsEndpoint(baseURL, projectID, location string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	location = strings.TrimSpace(location)
	if projectID == "" {
		return "", errors.New("vertex project_id is required")
	}
	if location == "" {
		location = "global"
	}
	if !vertexLocationPattern.MatchString(location) {
		return "", fmt.Errorf("invalid vertex location: %s", location)
	}
	if strings.TrimSpace(baseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/v1/projects/" + url.PathEscape(projectID) + "/locations/" + url.PathEscape(location) + "/batchPredictionJobs", nil
	}
	host := fmt.Sprintf("%s-aiplatform.googleapis.com", location)
	if location == "global" {
		host = "aiplatform.googleapis.com"
	}
	return fmt.Sprintf("https://%s/v1/projects/%s/locations/%s/batchPredictionJobs", host, url.PathEscape(projectID), url.PathEscape(location)), nil
}

type vertexCombinedJSONLReadCloser struct {
	ctx          context.Context
	accessToken  string
	objects      []string
	store        VertexBatchObjectStore
	index        int
	current      io.ReadCloser
	needBoundary bool
	closed       bool
}

func (r *vertexCombinedJSONLReadCloser) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if r.needBoundary {
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = '\n'
		r.needBoundary = false
		return 1, nil
	}
	for {
		if r.current == nil {
			if r.index >= len(r.objects) {
				return 0, io.EOF
			}
			obj := r.objects[r.index]
			r.index++
			rc, _, err := r.store.OpenObject(r.ctx, r.accessToken, obj)
			if err != nil {
				return 0, err
			}
			r.current = rc
		}
		n, err := r.current.Read(p)
		if err == io.EOF {
			_ = r.current.Close()
			r.current = nil
			if r.index < len(r.objects) {
				if n > 0 {
					r.needBoundary = true
					return n, nil
				}
				if len(p) == 0 {
					return 0, nil
				}
				p[0] = '\n'
				return 1, nil
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (r *vertexCombinedJSONLReadCloser) Close() error {
	r.closed = true
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}

type VertexBatchHTTPClient struct {
	baseURL string
	client  *http.Client
}

func NewVertexBatchHTTPClient(baseURL string, client *http.Client) *VertexBatchHTTPClient {
	if client == nil {
		client = httpclient.DefaultBatchHTTPClient()
	}
	return &VertexBatchHTTPClient{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), client: client}
}

func (c *VertexBatchHTTPClient) CreateBatchPredictionJob(ctx context.Context, accessToken string, req VertexCreateBatchPredictionJobRequest) (*VertexBatchPredictionJob, error) {
	endpoint, err := BuildVertexBatchPredictionJobsEndpoint(c.baseURL, req.ProjectID, req.Location)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	return doVertexJSON[VertexBatchPredictionJob](c.client, httpReq)
}

func (c *VertexBatchHTTPClient) GetBatchPredictionJob(ctx context.Context, accessToken string, name string) (*VertexBatchPredictionJob, error) {
	endpoint := c.vertexResourceURL(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return doVertexJSON[VertexBatchPredictionJob](c.client, req)
}

func (c *VertexBatchHTTPClient) CancelBatchPredictionJob(ctx context.Context, accessToken string, name string) error {
	endpoint := c.vertexResourceURL(name) + ":cancel"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return doVertexNoBody(c.client, req)
}

func (c *VertexBatchHTTPClient) vertexResourceURL(name string) string {
	name = strings.TrimLeft(strings.TrimSpace(name), "/")
	if c.baseURL != "" {
		return c.baseURL + "/v1/" + name
	}
	return "https://aiplatform.googleapis.com/v1/" + name
}

type VertexGCSObjectStore struct {
	baseURL string
	client  *http.Client
}

func NewVertexGCSObjectStore(baseURL string, client *http.Client) *VertexGCSObjectStore {
	if client == nil {
		client = httpclient.DefaultBatchHTTPClient()
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "https://storage.googleapis.com"
	}
	return &VertexGCSObjectStore{baseURL: baseURL, client: client}
}

func (s *VertexGCSObjectStore) UploadJSONL(ctx context.Context, accessToken string, uri string, r io.Reader) error {
	bucket, object, err := parseGCSURI(uri)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?uploadType=media&name=%s", s.baseURL, url.PathEscape(bucket), url.QueryEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/jsonl")
	return doVertexNoBody(s.client, req)
}

func (s *VertexGCSObjectStore) ListJSONLObjects(ctx context.Context, accessToken string, prefixURI string) ([]string, error) {
	return s.listObjects(ctx, accessToken, prefixURI, true)
}

func (s *VertexGCSObjectStore) listObjects(ctx context.Context, accessToken string, prefixURI string, jsonlOnly bool) ([]string, error) {
	bucket, prefix, err := parseGCSURI(prefixURI)
	if err != nil {
		return nil, err
	}
	var objects []string
	pageToken := ""
	for {
		endpoint := fmt.Sprintf("%s/storage/v1/b/%s/o?prefix=%s", s.baseURL, url.PathEscape(bucket), url.QueryEscape(prefix))
		if pageToken != "" {
			endpoint += "&pageToken=" + url.QueryEscape(pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		var page struct {
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := doVertexDecodeJSON(s.client, req, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			if !jsonlOnly || strings.HasSuffix(item.Name, ".jsonl") {
				objects = append(objects, "gs://"+bucket+"/"+item.Name)
			}
		}
		if page.NextPageToken == "" {
			return objects, nil
		}
		pageToken = page.NextPageToken
	}
}

func (s *VertexGCSObjectStore) OpenObject(ctx context.Context, accessToken string, uri string) (io.ReadCloser, string, error) {
	bucket, object, err := parseGCSURI(uri)
	if err != nil {
		return nil, "", err
	}
	endpoint := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", s.baseURL, url.PathEscape(bucket), url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, "", readVertexAPIError(resp)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/jsonl"
	}
	return resp.Body, contentType, nil
}

func (s *VertexGCSObjectStore) DeleteObject(ctx context.Context, accessToken string, uri string) error {
	bucket, object, err := parseGCSURI(uri)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/storage/v1/b/%s/o/%s", s.baseURL, url.PathEscape(bucket), url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return doVertexNoBody(s.client, req)
}

func (s *VertexGCSObjectStore) DeletePrefix(ctx context.Context, accessToken string, prefixURI string) error {
	objects, err := s.listObjects(ctx, accessToken, prefixURI, false)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := s.DeleteObject(ctx, accessToken, object); err != nil {
			return err
		}
	}
	return nil
}

func parseGCSURI(uri string) (bucket, object string, err error) {
	uri = strings.TrimSpace(uri)
	if !strings.HasPrefix(uri, "gs://") {
		return "", "", fmt.Errorf("invalid gcs uri")
	}
	rest := strings.TrimPrefix(uri, "gs://")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("invalid gcs uri")
	}
	return parts[0], parts[1], nil
}

type VertexAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *VertexAPIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" {
		return fmt.Sprintf("vertex api error: status=%d code=%s message=%s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("vertex api error: status=%d message=%s", e.StatusCode, e.Message)
}

func doVertexJSON[T any](client *http.Client, req *http.Request) (*T, error) {
	var out T
	if err := doVertexDecodeJSON(client, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func doVertexDecodeJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readVertexAPIError(resp)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func doVertexNoBody(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readVertexAPIError(resp)
	}
	return nil
}

func readVertexAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	message := string(body)
	code := ""
	var parsed struct {
		Error struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Error.Message != "" {
		message = parsed.Error.Message
		code = parsed.Error.Status
	}
	return &VertexAPIError{StatusCode: resp.StatusCode, Code: code, Message: message}
}

// NewCombinedJSONLReadCloser 按对象顺序懒读取，并在对象间保留原换行边界。
func NewCombinedJSONLReadCloser(ctx context.Context, token string, objects []string, store VertexBatchObjectStore) io.ReadCloser {
	return &vertexCombinedJSONLReadCloser{ctx: ctx, accessToken: token, objects: objects, store: store}
}
