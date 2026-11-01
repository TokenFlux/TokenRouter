package provider

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// parentDeathReady carries the isolated host's initialized child identity or startup failure.
type parentDeathReady struct {
	pid int   // initialized child process identifier
	err error // readiness read or parse failure
}

// startParentDeathHost starts the isolated host and waits for a successfully initialized child.
// Its startup budget includes host executable initialization, the full production
// handshake and Init budgets, and fixture failure cleanup. Parent-death assertions
// start only after this helper returns and keep their separate three-second limit.
//
// Stderr is retained in a test-owned file so failure output identifies whether
// the host entered main, connected, initialized, or exited. The helper kills and
// waits for the host before reading final diagnostics on failure; cleanup owns
// the same host after successful readiness, including assertion failures afterward.
func startParentDeathHost(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	directory := t.TempDir()
	logPath := filepath.Join(directory, "host.stderr")
	output, err := os.Create(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = output.Close() })
	host := exec.Command(fixtureBinaries["hostdeath"], fixtureBinaries["crashy"], directory)
	host.Stderr = output
	stdout, err := host.StdoutPipe()
	require.NoError(t, err)
	started := time.Now()
	require.NoError(t, host.Start())
	t.Cleanup(func() {
		_ = host.Process.Kill()
		_ = host.Wait()
	})
	ready := make(chan parentDeathReady, 1)
	// Readiness comes only from the initialized child's PID; diagnostic stderr
	// stays separate so startup messages cannot be mistaken for a successful handshake.
	go func() {
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			ready <- parentDeathReady{err: err}
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		ready <- parentDeathReady{pid: pid, err: err}
	}()
	// The outer allowance includes cold host startup before main plus both plugin
	// initialization phases; it does not extend the subsequent parent-death assertion.
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	var result parentDeathReady
	select {
	case result = <-ready:
	case <-timer.C:
		result.err = os.ErrDeadlineExceeded
	}
	if result.err != nil || result.pid <= 0 {
		_ = host.Process.Kill()
		waitErr := host.Wait()
		diagnostics, readErr := os.ReadFile(logPath)
		t.Fatalf("parent-death host readiness failed after %s: %v; host exit: %v; log read: %v\n%s", time.Since(started), result.err, waitErr, readErr, diagnostics)
	}
	t.Logf("parent-death host initialized child %d after %s", result.pid, time.Since(started))
	return host, result.pid
}

// packageArchive constructs a complete static archive without depending on local build artifacts.
// The fixture includes a valid contribution and original manifest bytes, allowing
// upload and recovery tests to compare real compressed and extracted identities.
func packageArchive(t *testing.T, version string) []byte {
	t.Helper()
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"` + version + `","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","frontend":{"entry":"frontend/index.js","pages":[]}}`)
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string // archive-relative file path
		data []byte // exact fixture file bytes
	}{{"plugin.json", raw}, {"frontend/index.js", []byte("globalThis.fixture = true;")}} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o644, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}))
		_, err := archive.Write(entry.data)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	return buffer.Bytes()
}

// uploadReference converts accepted preview metadata into the storage contract under test.
func uploadReference(upload *plugin.Upload) plugin.PackageRef {
	return plugin.PackageRef{
		PluginID:       upload.Snapshot.Manifest.ID,
		Version:        upload.Snapshot.Version,
		Source:         plugin.SourceUpload,
		Platform:       upload.Platform,
		ManifestSHA256: upload.Snapshot.ManifestSHA256,
		Artifact:       upload.Snapshot.Artifacts[upload.Platform],
		UploadToken:    upload.Token,
	}
}
