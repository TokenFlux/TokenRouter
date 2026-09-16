// 本地 TLS 验证 Vertex Batch/GCS 的实际请求、对象边界和取消。
package vertex

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVertexBatchAndGCSLocalTransport(t *testing.T) {
	var pages, removed atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
		switch {
		case strings.HasSuffix(r.URL.Path, "/batchPredictionJobs"):
			var request VertexCreateBatchPredictionJobRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			require.Equal(t, "publishers/google/models/image", request.Model)
			require.Equal(t, []string{"gs://bucket/in.jsonl"}, request.InputConfig.GCSSource.URIs)
			_, _ = io.WriteString(w, `{"name":"projects/p/locations/global/batchPredictionJobs/j","state":"JOB_STATE_PENDING"}`)
		case strings.HasSuffix(r.URL.Path, "/batchPredictionJobs/j:cancel"):
			require.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/batchPredictionJobs/j"):
			_, _ = io.WriteString(w, `{"name":"projects/p/locations/global/batchPredictionJobs/j","state":"JOB_STATE_SUCCEEDED","outputConfig":{"gcsDestination":{"outputUriPrefix":"gs://bucket/out/"}}}`)
		case strings.HasPrefix(r.URL.Path, "/upload/"):
			require.Equal(t, "in.jsonl", r.URL.Query().Get("name"))
			require.Equal(t, "application/jsonl", r.Header.Get("Content-Type"))
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, "input\n", string(body))
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/storage/v1/b/bucket/o":
			pages.Add(1)
			if r.URL.Query().Get("pageToken") == "" {
				_, _ = io.WriteString(w, `{"items":[{"name":"out/a.jsonl"},{"name":"out/other.txt"}],"nextPageToken":"page-two"}`)
			} else {
				require.Equal(t, "page-two", r.URL.Query().Get("pageToken"))
				_, _ = io.WriteString(w, `{"items":[{"name":"out/b.jsonl"}]}`)
			}
		case r.Method == http.MethodDelete:
			removed.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/out/a.jsonl"):
			_, _ = io.WriteString(w, `{"key":"a"}`)
		case strings.HasSuffix(r.URL.Path, "/out/b.jsonl"):
			_, _ = io.WriteString(w, `{"key":"b"}`)
		default:
			t.Errorf("未知请求 %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	client := NewVertexBatchHTTPClient(server.URL, server.Client())
	store := NewVertexGCSObjectStore(server.URL, server.Client())
	require.NoError(t, store.UploadJSONL(ctx, "fixture-token", "gs://bucket/in.jsonl", strings.NewReader("input\n")))
	job, err := client.CreateBatchPredictionJob(ctx, "fixture-token", VertexCreateBatchPredictionJobRequest{ProjectID: "p", Location: "global", Model: "publishers/google/models/image", InputConfig: VertexBatchInputConfig{InstancesFormat: "jsonl", GCSSource: VertexBatchGCSSource{URIs: []string{"gs://bucket/in.jsonl"}}}})
	require.NoError(t, err)
	got, err := client.GetBatchPredictionJob(ctx, "fixture-token", job.Name)
	require.NoError(t, err)
	require.Equal(t, "JOB_STATE_SUCCEEDED", got.State)
	require.NoError(t, client.CancelBatchPredictionJob(ctx, "fixture-token", job.Name))
	objects, err := store.ListJSONLObjects(ctx, "fixture-token", got.OutputConfig.GCSDestination.OutputURIPrefix)
	require.NoError(t, err)
	require.Equal(t, []string{"gs://bucket/out/a.jsonl", "gs://bucket/out/b.jsonl"}, objects)
	body := NewCombinedJSONLReadCloser(ctx, "fixture-token", objects, store)
	content, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "{\"key\":\"a\"}\n{\"key\":\"b\"}", string(content))
	require.NoError(t, body.Close())
	_, err = body.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.NoError(t, store.DeletePrefix(ctx, "fixture-token", "gs://bucket/out/"))
	require.EqualValues(t, 3, removed.Load())
	require.EqualValues(t, 4, pages.Load())
}

func TestVertexGCSStreamCancellation(t *testing.T) {
	exited := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		close(exited)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := NewVertexGCSObjectStore(server.URL, server.Client())
	body, _, err := store.OpenObject(ctx, "fixture", "gs://bucket/out.jsonl")
	require.NoError(t, err)
	buf := make([]byte, 6)
	_, err = io.ReadFull(body, buf)
	require.NoError(t, err)
	cancel()
	_, err = io.ReadAll(body)
	require.Error(t, err)
	require.NoError(t, body.Close())
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("对象连接未在取消后退出")
	}
}
