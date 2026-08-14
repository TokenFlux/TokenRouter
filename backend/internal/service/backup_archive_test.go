//go:build unit

package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpoolNextBackupPart_ReassemblesExactBytes(t *testing.T) {
	reader := bufio.NewReader(bytes.NewReader([]byte("0123456789abcdefg")))
	var got bytes.Buffer
	for index := 1; ; index++ {
		part, hasPart, hasMore, err := spoolNextBackupPart(context.Background(), reader, index, 5)
		require.NoError(t, err)
		require.True(t, hasPart)
		data, err := os.ReadFile(part.Path)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), part.SHA256)
		require.Equal(t, int64(len(data)), part.SizeBytes)
		require.NoError(t, cleanupBackupFiles(part.Path))
		got.Write(data)
		if !hasMore {
			break
		}
	}
	require.Equal(t, []byte("0123456789abcdefg"), got.Bytes())
}

func TestSpoolNextBackupPart_RejectsInvalidInput(t *testing.T) {
	reader := bufio.NewReader(bytes.NewReader([]byte("data")))
	_, _, _, err := spoolNextBackupPart(context.Background(), reader, 1, 0)
	require.Error(t, err)
	_, _, _, err = spoolNextBackupPart(context.Background(), reader, 0, 5)
	require.Error(t, err)
}

func TestSpoolNextBackupPart_EmptyInputCreatesNoFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	part, hasPart, hasMore, err := spoolNextBackupPart(
		context.Background(),
		bufio.NewReader(bytes.NewReader(nil)),
		1,
		5,
	)
	require.NoError(t, err)
	require.False(t, hasPart)
	require.False(t, hasMore)
	require.Empty(t, part.Path)
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestSpoolNextBackupPart_CancelledContextRemovesTemporaryFile(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := spoolNextBackupPart(
		ctx,
		bufio.NewReader(bytes.NewReader([]byte("backup"))),
		1,
		5,
	)
	require.ErrorIs(t, err, context.Canceled)
	entries, readErr := os.ReadDir(tempDir)
	require.NoError(t, readErr)
	require.Empty(t, entries)
}
