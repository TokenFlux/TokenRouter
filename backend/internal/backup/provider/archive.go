// Package provider 拥有备份归档的文件、压缩与外部进程资源。
package provider

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/backup"
)

type BackupRecord = backup.BackupRecord
type BackupS3Config = backup.BackupS3Config
type BackupDumpOptions = backup.BackupDumpOptions
type BackupObjectStore = backup.BackupObjectStore
type BackupObjectStoreSizedUploader = backup.BackupObjectStoreSizedUploader
type BackupObjectStoreProgressUploader = backup.BackupObjectStoreProgressUploader

const BackupStorageTypeS3 = backup.BackupStorageTypeS3
const BackupS3UploadModeSpooledPut = backup.BackupS3UploadModeSpooledPut
const backupObjectCleanupTimeout = 2 * time.Minute

// Archive 每次执行使用独立回调副本，不共享请求状态。
type Archive struct {
	dumper        backup.DBDumper
	partSizeBytes int64
	save          func(context.Context, *BackupRecord) error
}

func NewArchive(dumper backup.DBDumper, partSize int64) *Archive {
	if partSize <= 0 {
		partSize = defaultBackupPartSizeBytes
	}
	return &Archive{dumper: dumper, partSizeBytes: partSize}
}
func (s *Archive) Write(ctx context.Context, record *BackupRecord, objectStore BackupObjectStore, s3Cfg *BackupS3Config, dumpOptions BackupDumpOptions, save func(context.Context, *BackupRecord) error, cleanup func(time.Duration) (context.Context, context.CancelFunc)) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	local := *s
	local.save = save
	s = &local
	dumpReader, err := s.dumper.Dump(ctx, dumpOptions)
	if err != nil {
		return 0, fmt.Errorf("pg_dump failed: %w", err)
	}

	pr, pw := io.Pipe()
	gzipDone := make(chan error, 1)
	go func() {
		var gzipErr error
		defer func() {
			if recovered := recover(); recovered != nil {
				gzipErr = fmt.Errorf("gzip goroutine panic: %v", recovered)
			}
			if closeErr := dumpReader.Close(); gzipErr == nil && closeErr != nil {
				gzipErr = closeErr
			}
			if gzipErr != nil {
				_ = pw.CloseWithError(gzipErr)
			} else {
				_ = pw.Close()
			}
			gzipDone <- gzipErr
		}()

		gzWriter := gzip.NewWriter(pw)
		_, gzipErr = io.Copy(gzWriter, dumpReader)
		if closeErr := gzWriter.Close(); gzipErr == nil && closeErr != nil {
			gzipErr = closeErr
		}
	}()

	record.Progress = "uploading"
	_ = s.save(ctx, record)
	var sizeBytes int64
	if shouldUseBackupParts(record, s3Cfg) {
		sizeBytes, err = s.uploadBackupParts(ctx, record, objectStore, pr)
	} else {
		sizeBytes, err = objectStore.Upload(ctx, backup.RecordEffectiveStorageKey(record), pr, "application/gzip")
	}
	uploadErr := err
	if uploadErr != nil {
		cancel()
		_ = pr.CloseWithError(uploadErr)
	}
	gzipErr := <-gzipDone
	switch {
	case uploadErr != nil && gzipErr != nil:
		err = errors.Join(
			fmt.Errorf("backup write failed: %w", uploadErr),
			fmt.Errorf("gzip/dump failed after writer stopped: %w", gzipErr),
		)
	case uploadErr != nil:
		err = fmt.Errorf("backup write failed: %w", uploadErr)
	case gzipErr != nil:
		err = fmt.Errorf("gzip/dump failed: %w", gzipErr)
	}
	if err == nil {
		return sizeBytes, nil
	}

	cleanupCtx, cleanupCancel := cleanup(backupObjectCleanupTimeout)
	cleanupErr := backup.DeleteBackupObjectKeys(cleanupCtx, objectStore, record)
	cleanupCancel()
	return sizeBytes, errors.Join(err, cleanupErr)
}

func (s *Archive) uploadBackupParts(ctx context.Context, record *BackupRecord, objectStore BackupObjectStore, src io.Reader) (int64, error) {
	partSize := s.partSizeBytes
	if partSize <= 0 {
		partSize = defaultBackupPartSizeBytes
	}
	reader := bufio.NewReaderSize(&contextReader{ctx: ctx, reader: src}, 32*1024)
	firstPart, hasPart, hasMore, err := spoolNextBackupPart(ctx, reader, 1, partSize)
	if err != nil {
		return 0, err
	}
	if !hasPart {
		return 0, errors.New("backup archive is empty")
	}
	defer func() { _ = cleanupBackupFiles(firstPart.Path) }()

	if !hasMore {
		sizeBytes, uploadErr := uploadBackupPart(ctx, objectStore, backup.RecordEffectiveStorageKey(record), firstPart, "application/gzip")
		return sizeBytes, uploadErr
	}

	baseKey := backup.RecordEffectiveStorageKey(record)
	record.StorageKey = ""
	record.S3Key = ""
	record.Parts = nil
	totalBytes := int64(0)
	part := firstPart
	for {
		key := buildBackupPartKey(baseKey, record.ID, part.Index)
		record.Parts = append(record.Parts, BackupPart{
			Index:      part.Index,
			StorageKey: key,
			S3Key:      key,
			SizeBytes:  part.SizeBytes,
			SHA256:     part.SHA256,
		})
		if err := s.save(ctx, record); err != nil {
			_ = cleanupBackupFiles(part.Path)
			return totalBytes, fmt.Errorf("save backup part plan: %w", err)
		}
		if _, err := uploadBackupPart(ctx, objectStore, key, part, "application/octet-stream"); err != nil {
			_ = cleanupBackupFiles(part.Path)
			return totalBytes, fmt.Errorf("upload backup part %d: %w", part.Index, err)
		}
		totalBytes += part.SizeBytes
		_ = cleanupBackupFiles(part.Path)
		if !hasMore {
			return totalBytes, nil
		}

		part, hasPart, hasMore, err = spoolNextBackupPart(ctx, reader, part.Index+1, partSize)
		if err != nil {
			return totalBytes, err
		}
		if !hasPart {
			return totalBytes, errors.New("backup archive ended before the next planned part")
		}
	}
}

func uploadBackupPart(ctx context.Context, objectStore BackupObjectStore, key string, part localBackupPart, contentType string) (int64, error) {
	file, err := os.Open(part.Path)
	if err != nil {
		return 0, fmt.Errorf("open backup part %d: %w", part.Index, err)
	}
	defer func() { _ = file.Close() }()
	if uploader, ok := objectStore.(BackupObjectStoreSizedUploader); ok {
		return uploader.UploadSized(ctx, key, file, contentType, part.SizeBytes)
	}
	return objectStore.UploadFile(ctx, key, file, contentType)
}

// RestoreBackup 从记录对应存储后端下载备份并流式恢复到数据库

func shouldUseBackupParts(record *BackupRecord, s3Cfg *BackupS3Config) bool {
	return backup.RecordEffectiveStorageType(record) == BackupStorageTypeS3 &&
		s3Cfg != nil &&
		backup.NormalizeBackupS3UploadMode(s3Cfg.UploadMode) == BackupS3UploadModeSpooledPut
}

// uploadBackupParts 边读取 gzip 流边封卷，内存与临时磁盘都只保留当前一卷。

func (s *Archive) restoreBackupArchive(ctx context.Context, archivePath string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	defer func() { _ = archive.Close() }()
	gzReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}
	return nil
}

func (s *Archive) Restore(ctx context.Context, record *BackupRecord, objectStore BackupObjectStore) error {
	if len(record.Parts) > 0 {
		archivePath, err := downloadBackupParts(ctx, objectStore, record.Parts)
		if err != nil {
			return err
		}
		defer func() { _ = cleanupBackupFiles(archivePath) }()
		return s.restoreBackupArchive(ctx, archivePath)
	}

	// 从记录对应存储后端流式下载
	body, err := objectStore.Download(ctx, backup.RecordEffectiveStorageKey(record))
	if err != nil {
		return fmt.Errorf("backup download failed: %w", err)
	}
	defer func() { _ = body.Close() }()
	stopClose := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopClose()

	// 流式解压 gzip -> psql（不将全部数据加载到内存）
	gzReader, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// 流式恢复
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}

	return nil
}

// StartRestore 异步恢复备份，立即返回
