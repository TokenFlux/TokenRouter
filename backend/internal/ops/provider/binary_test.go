package provider

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/stretchr/testify/require"
)

// binaryFixtureClient 仅把受信资产请求映射到本地夹具，不访问真实发布资源。
type binaryFixtureClient struct{ url string }

func (c binaryFixtureClient) DownloadFile(ctx context.Context, url, dest string, max int64) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url+"/archive", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, max))
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0600)
}
func (c binaryFixtureClient) FetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.url+"/checksum", nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	return io.ReadAll(res.Body)
}
func TestS14BinaryInstallAndRollback(t *testing.T) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "sub2api", Mode: 0755, Size: 3}))
	_, err := tw.Write([]byte("new"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	name := "sub2api_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	sum := fmt.Sprintf("%x  %s\n", sha256.Sum256(buffer.Bytes()), name)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksum" {
			_, _ = io.WriteString(w, sum)
		} else {
			_, _ = w.Write(buffer.Bytes())
		}
	}))
	defer server.Close()
	assets := []ops.Asset{{Name: name, DownloadURL: "https://github.com/fixture/" + name}, {Name: "checksums.txt", DownloadURL: "https://github.com/fixture/checksums.txt"}}
	exe := filepath.Join(t.TempDir(), "server")
	require.NoError(t, os.WriteFile(exe, []byte("old"), 0755))
	installer := NewBinaryInstaller(binaryFixtureClient{server.URL}, func() (string, error) { return exe, nil })
	require.NoError(t, installer.Apply(context.Background(), assets))
	data, err := os.ReadFile(exe)
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	require.NoError(t, installer.Rollback())
	data, err = os.ReadFile(exe)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))
	// 第二次 rename 失败时必须恢复原二进制。
	calls := 0
	installer.rename = func(a, b string) error {
		calls++
		if calls == 2 {
			return errors.New("planned replace failure")
		}
		return os.Rename(a, b)
	}
	require.ErrorContains(t, installer.Apply(context.Background(), assets), "restored backup")
	data, err = os.ReadFile(exe)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))
	installer.rename = os.Rename
	sum = "bad  " + name + "\n"
	require.ErrorContains(t, installer.Apply(context.Background(), assets), "checksum mismatch")
	data, err = os.ReadFile(exe)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))
}
