package repository

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/BrandonVee/TokenRouter/internal/pkg/servertiming"
	"github.com/BrandonVee/TokenRouter/internal/service"
)

const s3UploadPartSizeMB = 64
const s3UploadConcurrency = 4
const s3MinUploadPartSizeMB = 5
const s3MaxUploadPartSizeMB = 128
const s3MinUploadConcurrency = 1
const s3MaxUploadConcurrency = 8
const s3MaxUploadParts = 10000
const s3UploadFailTimeout = 30 * time.Second

const backupStreamPartSizeBytes = 16 * 1024 * 1024
const backupStreamConcurrency = 1

// S3BackupStore implements service.BackupObjectStore using AWS S3 compatible storage
type S3BackupStore struct {
	client            *s3.Client
	bucket            string
	uploadConcurrency int
	uploadPartSizeMB  int
	uploadMode        string
}

// NewS3BackupStoreFactory returns a BackupObjectStoreFactory that creates S3-backed stores
func NewS3BackupStoreFactory() service.BackupObjectStoreFactory {
	return func(ctx context.Context, cfg *service.BackupS3Config) (service.BackupObjectStore, error) {
		region := cfg.Region
		if region == "" {
			region = "auto" // Cloudflare R2 默认 region
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(region),
			awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			),
		)
		if err != nil {
			return nil, fmt.Errorf("load aws config: %w", err)
		}

		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.Endpoint != "" {
				o.BaseEndpoint = &cfg.Endpoint
			}
			if cfg.ForcePathStyle {
				o.UsePathStyle = true
			}
			o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		})

		return &S3BackupStore{
			client:            client,
			bucket:            cfg.Bucket,
			uploadConcurrency: normalizeS3UploadConcurrency(cfg.UploadConcurrency),
			uploadPartSizeMB:  normalizeS3UploadPartSizeMB(cfg.UploadPartSizeMB),
			uploadMode:        service.NormalizeBackupS3UploadMode(cfg.UploadMode),
		}, nil
	}
}

func (s *S3BackupStore) Upload(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	if s.uploadMode == service.BackupS3UploadModeMultipart {
		return s.uploadBackupMultipart(ctx, key, body, contentType)
	}
	return s.uploadBackupSpooledPut(ctx, key, body, contentType)
}

// uploadBackupMultipart 使用单并发小分片上传未知长度的备份流，限制进程内缓冲区上限。
func (s *S3BackupStore) uploadBackupMultipart(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	progress := &s3UploadProgressListener{}
	uploader := transfermanager.New(s.client, func(o *transfermanager.Options) {
		o.PartSizeBytes = backupStreamPartSizeBytes
		o.MultipartUploadThreshold = backupStreamPartSizeBytes
		o.Concurrency = backupStreamConcurrency
		o.MaxUploadParts = s3MaxUploadParts
		o.FailTimeout = s3UploadFailTimeout
		o.ObjectProgressListeners.Register(progress)
	})
	finish := servertiming.ObserveDependency(ctx, "s3")
	out, err := uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
	finish()
	if err != nil {
		return progress.UploadedBytes(), fmt.Errorf("S3 backup multipart upload: %w", err)
	}
	uploaded := progress.UploadedBytes()
	if out.ContentLength != nil && *out.ContentLength > uploaded {
		uploaded = *out.ContentLength
	}
	return uploaded, nil
}

// uploadBackupSpooledPut 先写入权限受限的临时文件，再以已知长度执行兼容性更好的 PutObject。
func (s *S3BackupStore) uploadBackupSpooledPut(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error) {
	tmp, err := os.CreateTemp("", "tokenrouter-backup-upload-*.tmp")
	if err != nil {
		return 0, fmt.Errorf("create backup upload temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	written, err := io.Copy(tmp, &contextAwareReader{ctx: ctx, reader: body})
	if err != nil {
		return 0, fmt.Errorf("spool backup upload: %w", err)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("rewind backup upload temp file: %w", err)
	}
	if err := s.putObject(ctx, key, tmp, contentType, written); err != nil {
		return 0, err
	}
	return written, nil
}

type contextAwareReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read 在每次读取前检查取消信号，避免磁盘暂存阶段忽略任务超时。
func (r *contextAwareReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}

// UploadFile 面向几十 GB 级别的本地文件，超过单个分片后按固定缓冲区流式上传。
func (s *S3BackupStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.UploadFileWithProgress(ctx, key, body, contentType, nil)
}

// UploadSized 对已知长度的备份分卷直接执行 PutObject，避免兼容模式重复写临时文件。
func (s *S3BackupStore) UploadSized(ctx context.Context, key string, body io.Reader, contentType string, sizeBytes int64) (int64, error) {
	if sizeBytes < 0 {
		return 0, fmt.Errorf("backup upload size must not be negative")
	}
	if err := s.putObject(ctx, key, body, contentType, sizeBytes); err != nil {
		return 0, err
	}
	return sizeBytes, nil
}

// UploadFileWithProgress 面向几十 GB 级别的本地文件，使用官方 transfermanager 并发上传分片。
func (s *S3BackupStore) UploadFileWithProgress(ctx context.Context, key string, body io.Reader, contentType string, onProgress func(uploadedBytes int64)) (int64, error) {
	partSizeBytes := int64(normalizeS3UploadPartSizeMB(s.uploadPartSizeMB)) * 1024 * 1024
	progress := &s3UploadProgressListener{onProgress: onProgress}
	uploader := transfermanager.New(s.client, func(o *transfermanager.Options) {
		o.PartSizeBytes = partSizeBytes
		o.MultipartUploadThreshold = partSizeBytes
		o.Concurrency = normalizeS3UploadConcurrency(s.uploadConcurrency)
		o.MaxUploadParts = s3MaxUploadParts
		o.FailTimeout = s3UploadFailTimeout
		o.ObjectProgressListeners.Register(progress)
	})
	finish := servertiming.ObserveDependency(ctx, "s3")
	out, err := uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
	finish()
	if err != nil {
		return progress.UploadedBytes(), fmt.Errorf("S3 transfer upload object: %w", err)
	}
	uploaded := progress.UploadedBytes()
	if out.ContentLength != nil && *out.ContentLength > uploaded {
		uploaded = *out.ContentLength
	}
	if onProgress != nil {
		onProgress(uploaded)
	}
	return uploaded, nil
}

func (s *S3BackupStore) putObject(ctx context.Context, key string, body io.Reader, contentType string, contentLength int64) error {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        &s.bucket,
		Key:           &key,
		Body:          body,
		ContentType:   &contentType,
		ContentLength: &contentLength,
	})
	finish()
	if err != nil {
		return fmt.Errorf("S3 PutObject: %w", err)
	}
	return nil
}

func (s *S3BackupStore) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	finish := servertiming.ObserveDependency(ctx, "s3")
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	finish()
	if err != nil {
		return nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	return result.Body, nil
}

func (s *S3BackupStore) Delete(ctx context.Context, key string) error {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	finish()
	return err
}

func (s *S3BackupStore) PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)
	// 强制 attachment disposition：浏览器同页导航该 URL 时直接触发下载而非渲染，
	// 前端无需依赖会被弹窗拦截的新标签页。
	disposition := fmt.Sprintf("attachment; filename=%q", path.Base(key))
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     &s.bucket,
		Key:                        &key,
		ResponseContentDisposition: &disposition,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign url: %w", err)
	}
	return result.URL, nil
}

func (s *S3BackupStore) HeadBucket(ctx context.Context) error {
	finish := servertiming.ObserveDependency(ctx, "s3")
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &s.bucket,
	})
	finish()
	if err != nil {
		return fmt.Errorf("S3 HeadBucket failed: %w", err)
	}
	return nil
}

type s3UploadProgressListener struct {
	uploaded   atomic.Int64
	onProgress func(uploadedBytes int64)
}

func (l *s3UploadProgressListener) OnObjectBytesTransferred(_ context.Context, event *transfermanager.ObjectBytesTransferredEvent) {
	if l == nil || event == nil {
		return
	}
	uploaded := event.BytesTransferred
	l.uploaded.Store(uploaded)
	if l.onProgress != nil {
		l.onProgress(uploaded)
	}
}

func (l *s3UploadProgressListener) UploadedBytes() int64 {
	if l == nil {
		return 0
	}
	return l.uploaded.Load()
}

func normalizeS3UploadConcurrency(count int) int {
	if count <= 0 {
		count = s3UploadConcurrency
	}
	if count < s3MinUploadConcurrency {
		return s3MinUploadConcurrency
	}
	if count > s3MaxUploadConcurrency {
		return s3MaxUploadConcurrency
	}
	return count
}

func normalizeS3UploadPartSizeMB(sizeMB int) int {
	if sizeMB <= 0 {
		sizeMB = s3UploadPartSizeMB
	}
	if sizeMB < s3MinUploadPartSizeMB {
		return s3MinUploadPartSizeMB
	}
	if sizeMB > s3MaxUploadPartSizeMB {
		return s3MaxUploadPartSizeMB
	}
	return sizeMB
}
