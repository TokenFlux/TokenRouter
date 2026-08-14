package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
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

func TestS3BackupStoreUploadSizedUsesSinglePut(t *testing.T) {
	payload := bytes.Repeat([]byte("part"), 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "PutObject", r.URL.Query().Get("x-id"))
		require.Empty(t, r.URL.Query().Get("uploadId"))
		require.Empty(t, r.URL.Query().Get("partNumber"))
		_, hasUploads := r.URL.Query()["uploads"]
		require.False(t, hasUploads)
		require.Equal(t, int64(len(payload)), r.ContentLength)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		w.Header().Set("ETag", `"single"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3BackupStoreFactory()(context.Background(), &service.BackupS3Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		ForcePathStyle:  true,
	})
	require.NoError(t, err)
	uploader, ok := store.(service.BackupObjectStoreSizedUploader)
	require.True(t, ok)

	size, err := uploader.UploadSized(
		context.Background(),
		"backups/part-1",
		bytes.NewReader(payload),
		"application/octet-stream",
		int64(len(payload)),
	)
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), size)
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

func TestS3BackupStoreUploadMultipartDoesNotReadWholeBackup(t *testing.T) {
	var createRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && hasRawQueryKey(r.URL.Query(), "uploads") {
			createRequests.Add(1)
			http.Error(w, "multipart unavailable", http.StatusInternalServerError)
			return
		}
		t.Errorf("unexpected S3 request: method=%s path=%s query=%s", r.Method, r.URL.Path, r.URL.RawQuery)
		http.Error(w, "unexpected request", http.StatusBadRequest)
	}))
	defer server.Close()

	store, err := NewS3BackupStoreFactory()(context.Background(), &service.BackupS3Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		ForcePathStyle:  true,
		UploadMode:      service.BackupS3UploadModeMultipart,
	})
	require.NoError(t, err)

	reader := &generatedBackupReader{remaining: 256 * 1024 * 1024}
	_, err = store.Upload(context.Background(), "backups/large.sql.gz", reader, "application/gzip")
	require.Error(t, err)
	require.Greater(t, createRequests.Load(), int64(0))
	require.LessOrEqual(t, reader.readBytes.Load(), int64(backupStreamPartSizeBytes))
}

func TestS3BackupStoreUploadSpooledPutRemovesTemporaryFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	payload := bytes.Repeat([]byte("backup"), 1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, int64(len(payload)), r.ContentLength)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, payload, body)
		w.Header().Set("ETag", `"single"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store, err := NewS3BackupStoreFactory()(context.Background(), &service.BackupS3Config{
		Endpoint:        server.URL,
		Region:          "us-east-1",
		Bucket:          "bucket-a",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		ForcePathStyle:  true,
		UploadMode:      service.BackupS3UploadModeSpooledPut,
	})
	require.NoError(t, err)

	size, err := store.Upload(context.Background(), "backups/small.sql.gz", bytes.NewReader(payload), "application/gzip")
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), size)
	tempFiles, err := filepath.Glob(filepath.Join(tempDir, "tokenrouter-backup-upload-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, tempFiles)
}

func TestS3BackupStoreUploadSpooledPutRemovesTemporaryFileOnCancel(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := &S3BackupStore{uploadMode: service.BackupS3UploadModeSpooledPut}
	_, err := store.Upload(ctx, "backups/canceled.sql.gz", bytes.NewReader([]byte("backup")), "application/gzip")
	require.ErrorIs(t, err, context.Canceled)
	tempFiles, globErr := filepath.Glob(filepath.Join(tempDir, "tokenrouter-backup-upload-*.tmp"))
	require.NoError(t, globErr)
	require.Empty(t, tempFiles)
}

func TestS3BackupStoreUploadSpooledPutRemovesTemporaryFileOnReadError(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)

	store := &S3BackupStore{uploadMode: service.BackupS3UploadModeSpooledPut}
	_, err := store.Upload(context.Background(), "backups/failed.sql.gz", failingBackupReader{}, "application/gzip")
	require.ErrorContains(t, err, "spool backup upload")
	tempFiles, globErr := filepath.Glob(filepath.Join(tempDir, "tokenrouter-backup-upload-*.tmp"))
	require.NoError(t, globErr)
	require.Empty(t, tempFiles)
}

type generatedBackupReader struct {
	remaining int64
	readBytes atomic.Int64
}

type failingBackupReader struct{}

// Read 模拟备份流在落盘过程中读取失败。
func (failingBackupReader) Read([]byte) (int, error) {
	return 0, errors.New("read backup failed")
}

// Read 按需生成备份字节，测试大输入时不在内存中创建完整 payload。
func (r *generatedBackupReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= int64(n)
	r.readBytes.Add(int64(n))
	return n, nil
}
