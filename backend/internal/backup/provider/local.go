package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type LocalBackupStore struct {
	basePath string
}

func NewLocalBackupStore(basePath string) *LocalBackupStore {
	return &LocalBackupStore{basePath: basePath}
}

func (s *LocalBackupStore) Upload(ctx context.Context, key string, body io.Reader, _ string) (int64, error) {
	if _, err := s.safePath(key); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return 0, err
	}
	root, path, err := s.rooted(key)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("create backup directory: %w", err)
	}
	out, err := root.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create backup file: %w", err)
	}
	defer func() { _ = out.Close() }()

	written, err := copyWithContext(ctx, out, body)
	if err != nil {
		_ = root.Remove(path)
		return 0, err
	}
	return written, nil
}

func (s *LocalBackupStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.Upload(ctx, key, body, contentType)
}

func (s *LocalBackupStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	root, path, err := s.rooted(key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open backup file: %w", err)
	}
	return file, nil
}

func (s *LocalBackupStore) Delete(_ context.Context, key string) error {
	root, _, err := s.rooted(key)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	// 删除保留原最终符号链接语义；只解析父目录，不能误删链接指向的根内对象。
	full, err := s.safePath(key)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	relative, err := filepath.Rel(root.Name(), filepath.Join(parent, filepath.Base(full)))
	if err != nil {
		return err
	}
	if err = root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete backup file: %w", err)
	}
	return nil
}

func (s *LocalBackupStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	_, err := s.safePath(key)
	if err != nil {
		return "", err
	}
	return "", apperror.BadRequest("BACKUP_LOCAL_DOWNLOAD_REQUIRES_API", "local backup must be downloaded through the application")
}

func (s *LocalBackupStore) HeadBucket(_ context.Context) error {
	if err := os.MkdirAll(s.basePath, 0755); err != nil {
		return fmt.Errorf("create local backup directory: %w", err)
	}
	testFile, err := os.CreateTemp(s.basePath, ".write-test-*")
	if err != nil {
		return fmt.Errorf("local backup directory is not writable: %w", err)
	}
	name := testFile.Name()
	_ = testFile.Close()
	_ = os.Remove(name)
	return nil
}

func (s *LocalBackupStore) safePath(key string) (string, error) {
	cleanKey := filepath.Clean(strings.TrimSpace(key))
	if cleanKey == "." || strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanKey) {
		return "", apperror.BadRequest("INVALID_BACKUP_PATH", "invalid backup path")
	}
	baseAbs, err := filepath.Abs(s.basePath)
	if err != nil {
		return "", err
	}
	fullPath := filepath.Join(baseAbs, cleanKey)
	rel, err := filepath.Rel(baseAbs, fullPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", apperror.BadRequest("INVALID_BACKUP_PATH", "invalid backup path")
	}
	return fullPath, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				break
			}
			return written, er
		}
	}
	return written, nil
}

// rooted 校验真实目标，再由同一个目录句柄完成操作，防止校验后替换路径越界。
func (s *LocalBackupStore) rooted(key string) (*os.Root, string, error) {
	full, err := s.safePath(key)
	if err != nil {
		return nil, "", err
	}
	base, err := filepath.EvalSymlinks(s.basePath)
	if err != nil {
		return nil, "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	// 尚不存在的上传路径逐级解析其最近的现有父目录。
	probe := full
	var suffix []string
	for {
		target, e := filepath.EvalSymlinks(probe)
		if e == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				target = filepath.Join(target, suffix[i])
			}
			rel, e := filepath.Rel(base, target)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				_ = root.Close()
				return nil, "", apperror.BadRequest("INVALID_BACKUP_PATH", "invalid backup path")
			}
			return root, rel, nil
		}
		if !errors.Is(e, os.ErrNotExist) || probe == filepath.Dir(probe) {
			_ = root.Close()
			return nil, "", e
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = filepath.Dir(probe)
	}
}
