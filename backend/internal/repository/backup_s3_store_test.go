package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestS3BackupStoreUploadFileWithProgressUsesConcurrentMultipart(t *testing.T) {
	var activeParts atomic.Int64
	var maxActiveParts atomic.Int64
	var uploadedBytes atomic.Int64
	var uploadPartCount atomic.Int64
	var progressCount atomic.Int64
	var lastProgress atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.Method == http.MethodPost && hasRawQueryKey(query, "uploads"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<CreateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>bucket-a</Bucket><Key>exports/test.bin</Key><UploadId>upload-1</UploadId></CreateMultipartUploadResult>`)
		case r.Method == http.MethodPut && query.Get("partNumber") != "" && query.Get("uploadId") == "upload-1":
			current := activeParts.Add(1)
			for {
				old := maxActiveParts.Load()
				if current <= old || maxActiveParts.CompareAndSwap(old, current) {
					break
				}
			}
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			uploadedBytes.Add(n)
			uploadPartCount.Add(1)
			// 保持请求短暂重叠，测试能稳定观察到 transfermanager 的并发分片上传。
			time.Sleep(100 * time.Millisecond)
			activeParts.Add(-1)
			w.Header().Set("ETag", `"`+query.Get("partNumber")+`"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && query.Get("uploadId") == "upload-1":
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Location>http://example.test/bucket-a/exports/test.bin</Location><Bucket>bucket-a</Bucket><Key>exports/test.bin</Key><ETag>"complete"</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected S3 request: method=%s path=%s query=%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	store, err := NewS3BackupStoreFactory()(context.Background(), &service.BackupS3Config{
		Endpoint:          server.URL,
		Region:            "us-east-1",
		Bucket:            "bucket-a",
		AccessKeyID:       "ak",
		SecretAccessKey:   "sk",
		ForcePathStyle:    true,
		UploadConcurrency: 3,
		UploadPartSizeMB:  5,
	})
	require.NoError(t, err)
	uploader, ok := store.(service.BackupObjectStoreProgressUploader)
	require.True(t, ok)

	payload := bytes.Repeat([]byte("a"), 11*1024*1024)
	size, err := uploader.UploadFileWithProgress(context.Background(), "exports/test.bin", bytes.NewReader(payload), "application/octet-stream", func(uploadedBytes int64) {
		progressCount.Add(1)
		lastProgress.Store(uploadedBytes)
	})
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), size)
	require.Equal(t, int64(len(payload)), uploadedBytes.Load())
	require.GreaterOrEqual(t, uploadPartCount.Load(), int64(3))
	require.GreaterOrEqual(t, maxActiveParts.Load(), int64(2))
	require.Greater(t, progressCount.Load(), int64(0))
	require.Equal(t, int64(len(payload)), lastProgress.Load())
}

func hasRawQueryKey(values map[string][]string, key string) bool {
	_, ok := values[key]
	return ok
}

func TestS3UploadConfigNormalizers(t *testing.T) {
	require.Equal(t, s3UploadConcurrency, normalizeS3UploadConcurrency(0))
	require.Equal(t, s3UploadConcurrency, normalizeS3UploadConcurrency(-1))
	require.Equal(t, s3MaxUploadConcurrency, normalizeS3UploadConcurrency(100))
	require.Equal(t, 7, normalizeS3UploadConcurrency(7))

	require.Equal(t, s3UploadPartSizeMB, normalizeS3UploadPartSizeMB(0))
	require.Equal(t, s3MinUploadPartSizeMB, normalizeS3UploadPartSizeMB(1))
	require.Equal(t, s3MaxUploadPartSizeMB, normalizeS3UploadPartSizeMB(1000))
	require.Equal(t, 128, normalizeS3UploadPartSizeMB(128))
}

func TestS3BackupStoreUploadFileWithProgressReturnsPartialProgressOnCancel(t *testing.T) {
	var started atomic.Int64
	var abortSeen atomic.Bool
	var mu sync.Mutex
	var unexpected []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		switch {
		case r.Method == http.MethodPost && hasRawQueryKey(query, "uploads"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = fmt.Fprint(w, `<CreateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>bucket-a</Bucket><Key>exports/cancel.bin</Key><UploadId>upload-1</UploadId></CreateMultipartUploadResult>`)
		case r.Method == http.MethodPut && query.Get("partNumber") != "":
			started.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			time.Sleep(200 * time.Millisecond)
			w.Header().Set("ETag", `"`+query.Get("partNumber")+`"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && query.Get("uploadId") == "upload-1":
			abortSeen.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			partNumber, _ := strconv.Atoi(query.Get("partNumber"))
			mu.Lock()
			unexpected = append(unexpected, fmt.Sprintf("method=%s part=%d path=%s query=%s", r.Method, partNumber, r.URL.Path, r.URL.RawQuery))
			mu.Unlock()
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	store, err := NewS3BackupStoreFactory()(context.Background(), &service.BackupS3Config{
		Endpoint:          server.URL,
		Region:            "us-east-1",
		Bucket:            "bucket-a",
		AccessKeyID:       "ak",
		SecretAccessKey:   "sk",
		ForcePathStyle:    true,
		UploadConcurrency: 1,
		UploadPartSizeMB:  5,
	})
	require.NoError(t, err)
	uploader, ok := store.(service.BackupObjectStoreProgressUploader)
	require.True(t, ok)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for started.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	payload := bytes.Repeat([]byte("b"), 11*1024*1024)
	size, err := uploader.UploadFileWithProgress(ctx, "exports/cancel.bin", bytes.NewReader(payload), "application/octet-stream", nil)
	require.Error(t, err)
	require.LessOrEqual(t, size, int64(len(payload)))
	require.True(t, abortSeen.Load())
	mu.Lock()
	defer mu.Unlock()
	require.Empty(t, unexpected)
}
