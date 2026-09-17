package provider

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// 根内外链接均用临时目录，不读取宿主真实备份。
func TestS14B03LocalRoots(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	s := NewLocalBackupStore(root)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "fixture"), []byte("outside"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "outside")))
	_, err := s.Download(context.Background(), "outside/fixture")
	require.Error(t, err)
	_, err = s.Upload(context.Background(), "outside/new", bytes.NewBufferString("escaped"), "")
	require.Error(t, err)
	require.Error(t, s.Delete(context.Background(), "outside/fixture"))
	data, err := os.ReadFile(filepath.Join(outside, "fixture"))
	require.NoError(t, err)
	require.Equal(t, "outside", string(data))
	require.NoError(t, os.Mkdir(filepath.Join(root, "inside"), 0700))
	require.NoError(t, os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "alias")))
	_, err = s.Upload(context.Background(), "alias/new", bytes.NewBufferString("safe"), "")
	require.NoError(t, err)
	body, err := s.Download(context.Background(), "alias/new")
	require.NoError(t, err)
	data, err = io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, "safe", string(data))
	require.NoError(t, s.Delete(context.Background(), "alias/new"))
}

// 在校验后替换路径，目录句柄仍必须拒绝越界目标。
func TestS14B03PathReplacement(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	s := NewLocalBackupStore(base)
	require.NoError(t, os.Mkdir(filepath.Join(base, "dir"), 0700))
	root, relative, err := s.rooted("dir/new")
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	require.NoError(t, os.Rename(filepath.Join(base, "dir"), filepath.Join(base, "original")))
	require.NoError(t, os.Symlink(outside, filepath.Join(base, "dir")))
	file, err := root.Create(relative)
	if file != nil {
		_ = file.Close()
	}
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(outside, "new"))
	require.True(t, os.IsNotExist(err))
}

// 根内最终符号链接删除只删除链接，保持原对象存储的文件删除语义。
func TestS14B03DeleteInnerLinkPreservesTarget(t *testing.T) {
	base := t.TempDir()
	s := NewLocalBackupStore(base)
	require.NoError(t, os.WriteFile(filepath.Join(base, "target"), []byte("kept"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(base, "target"), filepath.Join(base, "link")))
	require.NoError(t, s.Delete(context.Background(), "link"))
	data, err := os.ReadFile(filepath.Join(base, "target"))
	require.NoError(t, err)
	require.Equal(t, "kept", string(data))
	_, err = os.Lstat(filepath.Join(base, "link"))
	require.True(t, os.IsNotExist(err))
}

func TestS14LocalMissingReadDoesNotCreateRoot(t *testing.T) {
	base := filepath.Join(t.TempDir(), "missing")
	s := NewLocalBackupStore(base)
	_, err := s.Download(context.Background(), "absent")
	require.Error(t, err)
	require.NoError(t, s.Delete(context.Background(), "absent"))
	_, err = os.Stat(base)
	require.True(t, os.IsNotExist(err))
}
