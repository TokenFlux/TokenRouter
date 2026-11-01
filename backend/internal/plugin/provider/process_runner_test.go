package provider

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/require"
)

// TestProcessRunnerCancellationClosesInheritedPipes keeps a descendant alive outside an owned plugin process group.
// Direct-child cancellation must close both host readers and allow waiting to finish, independently of Unix group termination or Windows descendant support.
func TestProcessRunnerCancellationClosesInheritedPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture launcher requires a Unix shell; runner cleanup is shared across platforms")
	}
	directory := t.TempDir()
	pidPath := filepath.Join(directory, "descendant.pid")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Deliberately omit platform process-group setup so cancellation can kill only
	// this direct shell, matching the descendant limitation of the Windows runner.
	command := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 30 & echo $! > "$1"; wait`, "fixture", pidPath)
	runner, err := newProcessRunner(command)
	require.NoError(t, err)
	require.NoError(t, runner.Start(t.Context()))
	var readers sync.WaitGroup
	readers.Add(2)
	for _, reader := range []io.Reader{runner.Stdout(), runner.Stderr()} {
		// Reproduce go-plugin's ordering: output readers must finish before its
		// process waiter runs, so natural direct-child exit alone cannot unblock cleanup.
		go func() {
			defer readers.Done()
			_, _ = io.Copy(io.Discard, reader)
		}()
	}
	finished := make(chan error, 1)
	go func() {
		readers.Wait()
		finished <- runner.Wait(context.Background())
	}()
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidPath)
		return err == nil && strings.TrimSpace(string(data)) != ""
	}, time.Second, 5*time.Millisecond)
	data, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	descendant, err := os.FindProcess(pid)
	require.NoError(t, err)
	// The deliberately ungrouped helper remains test-owned; cleanup always kills
	// it after verifying that host-side output closure does not depend on its exit.
	t.Cleanup(func() {
		_ = descendant.Kill()
		_ = descendant.Release()
	})
	cancel()
	select {
	case waitErr := <-finished:
		require.Error(t, waitErr)
		require.Equal(t, waitErr, runner.Wait(t.Context()))
	case <-time.After(time.Second):
		t.Fatal("inherited output kept the direct-child waiter blocked")
	}
	alive, err := process.PidExists(int32(pid))
	require.NoError(t, err)
	require.True(t, alive, "probe descendant exited before output closure was verified")
	for _, reader := range []io.Reader{runner.Stdout(), runner.Stderr()} {
		_, err := reader.Read(make([]byte, 1))
		require.ErrorIs(t, err, os.ErrClosed)
	}
}

// TestProcessRunnerFailedStartClosesOutput attempts to execute a nonexistent fixture binary after acquiring its output pipes.
// Failed startup closes both readers, publishes no process identity, and remains safe when go-plugin requests repeated cleanup.
func TestProcessRunnerFailedStartClosesOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	command := exec.CommandContext(ctx, filepath.Join(t.TempDir(), "missing-plugin"))
	runner, err := newProcessRunner(command)
	require.NoError(t, err)
	require.Error(t, runner.Start(t.Context()))
	require.Empty(t, runner.ID())
	require.NoError(t, runner.Kill(t.Context()))
	for _, reader := range []io.Reader{runner.Stdout(), runner.Stderr()} {
		_, err := reader.Read(make([]byte, 1))
		require.ErrorIs(t, err, os.ErrClosed)
	}
}
