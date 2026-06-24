package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

const s3UploadPartSizeMB = 64
const s3UploadConcurrency = 4
const s3MinUploadPartSizeMB = 5
const s3MaxUploadPartSizeMB = 128
const s3MinUploadConcurrency = 1
const s3MaxUploadConcurrency = 8
const s3MaxUploadParts = 10000
const s3UploadFailTimeout = 30 * time.Second

// S3BackupStore implements service.BackupObjectStore using AWS S3 compatible storage
type S3BackupStore struct {
	client            *s3.Client
	bucket            string
	uploadConcurrency int
	uploadPartSizeMB  int
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
		}, nil
	}
}

func (s *S3BackupStore) Upload(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	// 读取全部内容以获取大小（S3 PutObject 需要知道内容长度）
	// 注意：阿里云 OSS 不兼容 s3manager 分片上传的签名方式，因此使用 PutObject
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}

	if err := s.putObject(ctx, key, bytes.NewReader(data), contentType); err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

// UploadFile 面向几十 GB 级别的本地文件，超过单个分片后按固定缓冲区流式上传。
func (s *S3BackupStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	return s.UploadFileWithProgress(ctx, key, body, contentType, nil)
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
	out, err := uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
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

func (s *S3BackupStore) putObject(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &s.bucket,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
	if err != nil {
		return fmt.Errorf("S3 PutObject: %w", err)
	}
	return nil
}

func (s *S3BackupStore) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("S3 GetObject: %w", err)
	}
	return result.Body, nil
}

func (s *S3BackupStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	})
	return err
}

func (s *S3BackupStore) PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s.client)
	result, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", fmt.Errorf("presign url: %w", err)
	}
	return result.URL, nil
}

func (s *S3BackupStore) HeadBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &s.bucket,
	})
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
