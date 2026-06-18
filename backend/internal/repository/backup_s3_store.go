package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

const s3MultipartPartSize = 64 * 1024 * 1024

// S3BackupStore implements service.BackupObjectStore using AWS S3 compatible storage
type S3BackupStore struct {
	client *s3.Client
	bucket string
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

		return &S3BackupStore{client: client, bucket: cfg.Bucket}, nil
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

// UploadFileWithProgress 面向几十 GB 级别的本地文件，按分片完成情况上报累计上传字节数。
func (s *S3BackupStore) UploadFileWithProgress(ctx context.Context, key string, body io.Reader, contentType string, onProgress func(uploadedBytes int64)) (int64, error) {
	buf := make([]byte, s3MultipartPartSize)
	firstSize, readErr := io.ReadFull(body, buf)
	if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
		if err := s.putObject(ctx, key, bytes.NewReader(buf[:firstSize]), contentType); err != nil {
			return 0, err
		}
		if onProgress != nil {
			onProgress(int64(firstSize))
		}
		return int64(firstSize), nil
	}
	if readErr != nil {
		return 0, fmt.Errorf("read multipart body: %w", readErr)
	}

	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      &s.bucket,
		Key:         &key,
		ContentType: &contentType,
	})
	if err != nil {
		return 0, fmt.Errorf("S3 create multipart upload: %w", err)
	}
	if created.UploadId == nil || *created.UploadId == "" {
		return 0, fmt.Errorf("S3 create multipart upload: missing upload id")
	}
	uploadID := *created.UploadId
	completedParts := make([]types.CompletedPart, 0, 16)
	var total int64
	partNumber := int32(1)
	abort := true
	defer func() {
		if abort {
			_, _ = s.client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
				Bucket:   &s.bucket,
				Key:      &key,
				UploadId: &uploadID,
			})
		}
	}()

	etag, err := s.uploadPart(ctx, key, uploadID, partNumber, bytes.NewReader(buf[:firstSize]))
	if err != nil {
		return 0, err
	}
	completedPartNumber := partNumber
	completedParts = append(completedParts, types.CompletedPart{
		ETag:       etag,
		PartNumber: &completedPartNumber,
	})
	total += int64(firstSize)
	if onProgress != nil {
		onProgress(total)
	}
	partNumber++

	for {
		n, readErr := io.ReadFull(body, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return total, fmt.Errorf("read multipart body: %w", readErr)
		}
		if n == 0 && readErr == io.EOF {
			break
		}
		etag, err := s.uploadPart(ctx, key, uploadID, partNumber, bytes.NewReader(buf[:n]))
		if err != nil {
			return total, err
		}
		completedPartNumber := partNumber
		completedParts = append(completedParts, types.CompletedPart{
			ETag:       etag,
			PartNumber: &completedPartNumber,
		})
		total += int64(n)
		if onProgress != nil {
			onProgress(total)
		}
		partNumber++
		if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
			break
		}
	}
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   &s.bucket,
		Key:      &key,
		UploadId: &uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		return total, fmt.Errorf("S3 complete multipart upload: %w", err)
	}
	abort = false
	return total, nil
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

func (s *S3BackupStore) uploadPart(ctx context.Context, key string, uploadID string, partNumber int32, body io.Reader) (*string, error) {
	partOut, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     &s.bucket,
		Key:        &key,
		UploadId:   &uploadID,
		PartNumber: &partNumber,
		Body:       body,
	})
	if err != nil {
		return nil, fmt.Errorf("S3 upload part %d: %w", partNumber, err)
	}
	return partOut.ETag, nil
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
