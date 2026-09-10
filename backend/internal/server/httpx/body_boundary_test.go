package httpx

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// zeroReader 生成可压缩数据，避免测试额外分配一份大输入。
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// TestCompressedRequestRetainsReadBoundary 保留旧 LimitReader 的截断与请求元数据更新行为。
func TestCompressedRequestRetainsReadBoundary(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.CopyN(writer, zeroReader{}, maxDecompressedBodySize+1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", &compressed)
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Content-Length", "123")
	body, err := ReadRequestBodyWithPrealloc(request)
	if err != nil || len(body) != maxDecompressedBodySize {
		t.Fatalf("boundary changed: length=%d err=%v", len(body), err)
	}
	if request.ContentLength != maxDecompressedBodySize || request.Header.Get("Content-Encoding") != "" || request.Header.Get("Content-Length") != "" {
		t.Fatal("decompressed request metadata changed")
	}
}

// TestRequestBodyPreservesOuterLimit 保留路由层设置的原始请求体大小错误。
func TestRequestBodyPreservesOuterLimit(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("oversize"))
	request.Body = http.MaxBytesReader(httptest.NewRecorder(), request.Body, 3)
	_, err := ReadRequestBodyWithPrealloc(request)
	var limit *http.MaxBytesError
	if !errors.As(err, &limit) || limit.Limit != 3 {
		t.Fatalf("expected original MaxBytesError, got %v", err)
	}
}
