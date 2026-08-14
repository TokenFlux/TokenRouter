package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"sort"
	"strings"
)

const defaultBackupPartSizeBytes int64 = 4 * 1024 * 1024 * 1024

// BackupPart 描述一个 gzip 字节分卷；StorageKey 为当前字段，S3Key 兼容上游和旧客户端。
type BackupPart struct {
	Index      int    `json:"index"`
	StorageKey string `json:"storage_key,omitempty"`
	S3Key      string `json:"s3_key,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256,omitempty"`
}

type localBackupPart struct {
	Index     int
	Path      string
	SizeBytes int64
	SHA256    string
}

// spoolNextBackupPart 只落盘一个有界分卷，并通过 bufio.Peek 判断后续是否还有数据。
// 调用方必须在使用完返回路径后删除临时文件。
func spoolNextBackupPart(ctx context.Context, src *bufio.Reader, index int, partSize int64) (part localBackupPart, hasPart bool, hasMore bool, err error) {
	if partSize <= 0 {
		return part, false, false, errors.New("backup part size must be positive")
	}
	if index <= 0 {
		return part, false, false, errors.New("backup part index must be positive")
	}

	tmp, err := os.CreateTemp("", "tokenrouter-backup-part-*.tmp")
	if err != nil {
		return part, false, false, fmt.Errorf("create backup part: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(tmp, hash), &contextReader{ctx: ctx, reader: src}, partSize)
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		cleanup()
		return part, false, false, fmt.Errorf("write backup part %d: %w", index, copyErr)
	}
	if written == 0 {
		cleanup()
		return part, false, false, nil
	}
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return part, false, false, fmt.Errorf("close backup part %d: %w", index, closeErr)
	}

	if _, peekErr := src.Peek(1); peekErr == nil {
		hasMore = true
	} else if !errors.Is(peekErr, io.EOF) {
		_ = os.Remove(tmpPath)
		return part, false, false, fmt.Errorf("inspect backup part %d boundary: %w", index, peekErr)
	}

	return localBackupPart{
		Index:     index,
		Path:      tmpPath,
		SizeBytes: written,
		SHA256:    hex.EncodeToString(hash.Sum(nil)),
	}, true, hasMore, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每次读取前检查取消信号，避免临时卷写入阶段忽略任务超时。
func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}

func backupPartStorageKey(part BackupPart) string {
	if strings.TrimSpace(part.StorageKey) != "" {
		return strings.TrimSpace(part.StorageKey)
	}
	return strings.TrimSpace(part.S3Key)
}

func buildBackupPartKey(baseKey, backupID string, index int) string {
	root := pathpkg.Dir(strings.TrimSpace(baseKey))
	if root == "." {
		root = ""
	}
	return pathpkg.Join(root, strings.TrimSpace(backupID), fmt.Sprintf("payload.part-%06d", index))
}

func orderedBackupParts(parts []BackupPart) ([]BackupPart, error) {
	if len(parts) == 0 {
		return nil, errors.New("backup parts are empty")
	}
	ordered := append([]BackupPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	for i, part := range ordered {
		if part.Index != i+1 || backupPartStorageKey(part) == "" || part.SizeBytes <= 0 {
			return nil, fmt.Errorf("invalid backup part metadata at index %d", i+1)
		}
	}
	return ordered, nil
}

// downloadBackupParts 先下载并校验全部分卷，再把完整 gzip 归档交给恢复流程。
// 这样任一后续分卷损坏都不会让数据库进入部分恢复状态。
func downloadBackupParts(ctx context.Context, objectStore BackupObjectStore, parts []BackupPart) (archivePath string, err error) {
	ordered, err := orderedBackupParts(parts)
	if err != nil {
		return "", err
	}

	archive, err := os.CreateTemp("", "tokenrouter-backup-restore-*.sql.gz")
	if err != nil {
		return "", fmt.Errorf("create restore archive: %w", err)
	}
	tempPath := archive.Name()
	archivePath = tempPath
	defer func() {
		if closeErr := archive.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close restore archive: %w", closeErr)
		}
		if err != nil {
			_ = cleanupBackupFiles(tempPath)
		}
	}()

	for _, part := range ordered {
		body, downloadErr := objectStore.Download(ctx, backupPartStorageKey(part))
		if downloadErr != nil {
			return "", fmt.Errorf("download backup part %d: %w", part.Index, downloadErr)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(archive, hash), &contextReader{ctx: ctx, reader: body})
		closeErr := body.Close()
		if copyErr != nil {
			return "", fmt.Errorf("read backup part %d: %w", part.Index, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close backup part %d: %w", part.Index, closeErr)
		}
		if written != part.SizeBytes {
			return "", fmt.Errorf("backup part %d size mismatch: got %d, want %d", part.Index, written, part.SizeBytes)
		}
		actualSHA256 := hex.EncodeToString(hash.Sum(nil))
		if part.SHA256 != "" && !strings.EqualFold(part.SHA256, actualSHA256) {
			return "", fmt.Errorf("backup part %d checksum mismatch", part.Index)
		}
	}
	return archivePath, nil
}

func cleanupBackupFiles(paths ...string) error {
	var errs []error
	for _, filePath := range paths {
		if strings.TrimSpace(filePath) == "" {
			continue
		}
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", filePath, err))
		}
	}
	return errors.Join(errs...)
}
