// 本地 TLS 夹具验证 Batch 与图片调用的实际报文、资源关闭及取消。
package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

func TestGenerateImagesLocalWireAndLastOutput(t *testing.T) {
	first, last := []byte("\x89PNG\r\n\x1a\nfirst"), []byte("\x89PNG\r\n\x1a\nlast")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models/gemini-image:generateContent", r.URL.Path)
		require.Equal(t, "overridden-fixture", r.Header.Get("x-goog-api-key"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		config, ok := body["generationConfig"].(map[string]any)
		require.True(t, ok)
		imageConfig, ok := config["imageConfig"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "2K", imageConfig["imageSize"])
		thinkingConfig, ok := config["thinkingConfig"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, false, thinkingConfig["includeThoughts"])
		_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}},{"inline_data":{"mime_type":"image/png","data":%q}}]}}]}`, base64.StdEncoding.EncodeToString(first), base64.StdEncoding.EncodeToString(last))
	}))
	defer server.Close()
	var closes, releases atomic.Int32
	options := ImageOptions{Mode: APIKeyCredential, Model: "gemini-image", APIKey: func() string { return "fixture" }, BaseURL: func() string { return server.URL }, ValidateGeminiBaseURL: func(v string) (string, error) { return v, nil }, ApplyHeaders: func(h http.Header) { h.Set("x-goog-api-key", "overridden-fixture") }, Do: func(req *http.Request) (*http.Response, error) {
		res, err := server.Client().Do(req)
		if res != nil {
			res.Body = &executeBody{ReadCloser: res.Body, closes: &closes}
		}
		return res, err
	}, HTTPError: func(status int, message string) error { return fmt.Errorf("http %d: %s", status, message) }, Invalid: func(format string, args ...any) error { return fmt.Errorf(format, args...) }, ErrorMessage: func(b []byte) string { return string(b) }, Enter: func() (func(), error) { return func() { releases.Add(1) }, nil }}
	outputs, err := GenerateImages(context.Background(), BuildImageRequest(ImageRequestInput{Prompt: "fixture", ImageSize: "2K", ThinkingLevel: "low"}), options)
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Equal(t, last, outputs[0].Bytes)
	require.Equal(t, 0, outputs[0].Index)
	require.EqualValues(t, 1, closes.Load())
	require.EqualValues(t, 1, releases.Load())
}

func TestBatchClientLocalTLSAndStreamCancellation(t *testing.T) {
	streamStarted := make(chan struct{})
	streamStopped := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "fixture-key", r.Header.Get("x-goog-api-key"))
		switch {
		case r.URL.Path == "/upload/v1beta/files":
			require.Equal(t, "multipart", r.URL.Query().Get("uploadType"))
			require.NoError(t, r.ParseMultipartForm(1<<20))
			file, _, err := r.FormFile("file")
			require.NoError(t, err)
			data, err := io.ReadAll(file)
			_ = file.Close()
			require.NoError(t, err)
			require.Contains(t, string(data), `"key":"request-one"`)
			_, _ = io.WriteString(w, `{"file":{"name":"files/f1"}}`)
		case r.URL.Path == "/v1beta/models/gemini-image:batchGenerateContent":
			_, _ = io.WriteString(w, `{"name":"batches/b1","state":"JOB_STATE_PENDING"}`)
		case r.URL.Path == "/v1beta/batches/b1":
			_, _ = io.WriteString(w, `{"name":"batches/b1","state":"JOB_STATE_SUCCEEDED","dest":{"fileName":"files/f1"}}`)
		case r.URL.Path == "/v1beta/batches/b1:cancel":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1beta/files/f1" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1beta/files/f1":
			_, _ = fmt.Fprintf(w, `{"downloadUri":"https://%s/bytes","mimeType":"application/jsonl"}`, r.Host)
		case r.URL.Path == "/bytes":
			w.Header().Set("Content-Type", "application/jsonl")
			// 明确仍有未发送内容，避免取消与正常 chunked EOF 竞争而误判为下载完成。
			w.Header().Set("Content-Length", fmt.Sprint(len("first-line\n")+1))
			_, _ = io.WriteString(w, "first-line\n")
			_ = http.NewResponseController(w).Flush()
			close(streamStarted)
			<-r.Context().Done()
			close(streamStopped)
		default:
			t.Errorf("未知调用: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewGeminiBatchHTTPClient(server.URL, server.Client(), errors.New("missing fixture key"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	uploaded, err := client.UploadJSONL(ctx, "fixture-key", "fixture", strings.NewReader("{\"key\":\"request-one\"}\n"))
	require.NoError(t, err)
	require.Equal(t, "files/f1", uploaded.Name)
	created, err := client.CreateBatch(ctx, "fixture-key", "gemini-image", uploaded.Name, "fixture")
	require.NoError(t, err)
	require.Equal(t, "batches/b1", created.Name)
	batch, err := client.GetBatch(ctx, "fixture-key", created.Name)
	require.NoError(t, err)
	require.Equal(t, "JOB_STATE_SUCCEEDED", batch.State)
	require.NoError(t, client.CancelBatch(ctx, "fixture-key", created.Name))
	require.NoError(t, client.DeleteFile(ctx, "fixture-key", uploaded.Name))
	body, mime, err := client.DownloadFile(ctx, "fixture-key", uploaded.Name)
	require.NoError(t, err)
	defer func() { _ = body.Close() }()
	require.Equal(t, "application/jsonl", mime)
	select {
	case <-streamStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("下载没有开始")
	}
	data := make([]byte, len("first-line\n"))
	_, err = io.ReadFull(body, data)
	require.NoError(t, err)
	cancel()
	_, err = io.ReadAll(body)
	require.Error(t, err)
	_ = body.Close()
	select {
	case <-streamStopped:
	case <-time.After(3 * time.Second):
		t.Fatal("取消未释放上游下载")
	}
}

func TestImageByteDefaultsRemainCompatible(t *testing.T) {
	data := []byte("\x89PNG\r\n\x1a\nfixture")
	decoded, err := upstream.DecodeBase64Image(base64.RawStdEncoding.EncodeToString(data))
	require.NoError(t, err)
	require.Equal(t, data, decoded.Bytes)
	require.Equal(t, "image/png", decoded.Mime)
}
