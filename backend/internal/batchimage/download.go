// Download 保留结果索引、ZIP 与限额规则，返回的流拥有释放下载许可的责任。
package batchimage

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type DownloadOptions struct {
	MaxItems int
	MaxBytes int64
	Duration time.Duration
}
type Download struct {
	Repo            BatchImageRepository
	ResolveProvider func(context.Context, *BatchImageJob) (BoundProvider, error)
	Limiter         BatchImageDownloadLimiter
	Options         DownloadOptions
}

const (
	DefaultBatchImageZipMaxItems          = 200
	DefaultBatchImageZipMaxBytes          = 512 * 1024 * 1024
	DefaultBatchImageDownloadDuration     = 10 * time.Minute
	DefaultBatchImageDownloadConcurrency  = 1
	BatchImageDownloadScannerMaxLineBytes = 16 * 1024 * 1024
)

var ErrBatchImageDownloadSizeExceeded = errors.New("batch image download size limit exceeded")

type BatchImageDownloadLimiter interface {
	Acquire(ctx context.Context, userID string, kind string) (BatchImageDownloadPermit, error)
}
type BatchImageDownloadPermit interface {
	Release(ctx context.Context) error
}
type BatchImageContentStream struct {
	Reader        io.ReadCloser
	ContentType   string
	Filename      string
	ContentLength *int64
}
type BatchImageZipOptions struct {
	Status          string
	MaxItems        int
	IncludeManifest bool
}
type BatchImageZipResult struct {
	FileCount  int
	ErrorCount int
}
type BatchImageLineImages struct {
	CustomID     string
	Images       []BatchImageInlineImage
	ErrorCode    string
	ErrorMessage string
}
type BatchImageInlineImage struct {
	MimeType   string
	Extension  string
	Base64Data string
}
type BatchImageDownloadLimitWriter struct {
	w       io.Writer
	limit   int64
	written int64
}

func (w *BatchImageDownloadLimitWriter) Write(p []byte) (int, error) {
	if w == nil || w.w == nil {
		return 0, io.ErrClosedPipe
	}
	if w.limit > 0 && w.written+int64(len(p)) > w.limit {
		return 0, ErrBatchImageDownloadSizeExceeded
	}
	n, err := w.w.Write(p)
	w.written += int64(n)
	return n, err
}
func (s *Download) OpenItemContent(ctx context.Context, owner BatchImageOwner, batchID string, customID string, imageIndex int) (*BatchImageContentStream, error) {
	if imageIndex < 0 {
		return nil, ErrBatchImageItemImageIndexOutOfRange
	}
	job, err := s.GetCompletedJob(ctx, owner, batchID)
	if err != nil {
		return nil, err
	}
	item, err := s.Repo.GetBatchImageItemForDownload(ctx, job.BatchID, customID)
	if err != nil {
		return nil, err
	}
	if item.Status != BatchImageItemStatusSuccess {
		return nil, ErrBatchImageItemFailed
	}
	if imageIndex >= item.ImageCount {
		return nil, ErrBatchImageItemImageIndexOutOfRange
	}

	permit, err := s.AcquirePermit(ctx, owner.UserID, "item")
	if err != nil {
		return nil, err
	}
	releasePermit := true
	defer func() {
		if releasePermit && permit != nil {
			_ = permit.Release(ctx)
		}
	}()

	provider, err := s.ResolveProvider(ctx, job)
	if err != nil {
		return nil, err
	}
	r, _, err := provider.OpenResult(ctx, job)
	if err != nil {
		return nil, ErrBatchImageResultMissing.WithCause(err)
	}
	defer func() { _ = r.Close() }()

	line, err := FindBatchImageLineImages(r, item.CustomID)
	if err != nil {
		return nil, err
	}
	if imageIndex >= len(line.Images) {
		return nil, ErrBatchImageItemImageIndexOutOfRange
	}
	image := line.Images[imageIndex]
	if strings.TrimSpace(image.Base64Data) == "" {
		return nil, ErrBatchImageResultMissing
	}
	contentType := strings.TrimSpace(image.MimeType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	extension := strings.TrimSpace(image.Extension)
	if extension == "" {
		extension = BatchImageFileExtension(contentType)
	}
	if extension == "" {
		extension = "bin"
	}

	reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(image.Base64Data))
	releasePermit = false
	return &BatchImageContentStream{
		Reader:      &BatchImagePermitReadCloser{Reader: reader, permit: permit},
		ContentType: contentType,
		Filename:    BatchImageSafeDownloadFilename(item.CustomID, extension),
	}, nil
}
func (s *Download) StreamZip(ctx context.Context, owner BatchImageOwner, batchID string, opts BatchImageZipOptions, w io.Writer) (*BatchImageZipResult, error) {
	job, err := s.GetCompletedJob(ctx, owner, batchID)
	if err != nil {
		return nil, err
	}
	maxItems := opts.MaxItems
	if cap := s.MaxZipItems(); maxItems <= 0 || maxItems > cap {
		// 客户端传入的 max_items 不得放大管理员配置的 ZIP 上限。
		maxItems = cap
	}
	if job.SuccessCount > maxItems {
		return nil, ErrBatchImageZipTooManyItems
	}
	successItems, err := s.Repo.ListBatchImageItemsForDownload(ctx, job.BatchID, BatchImageItemStatusSuccess, maxItems+1)
	if err != nil {
		return nil, err
	}
	if len(successItems) > maxItems {
		return nil, ErrBatchImageZipTooManyItems
	}
	failedItems, err := s.Repo.ListBatchImageItemsForDownload(ctx, job.BatchID, BatchImageItemStatusFailed, maxItems)
	if err != nil {
		return nil, err
	}

	permit, err := s.AcquirePermit(ctx, owner.UserID, "zip")
	if err != nil {
		return nil, err
	}
	if permit != nil {
		defer func() { _ = permit.Release(ctx) }()
	}

	provider, err := s.ResolveProvider(ctx, job)
	if err != nil {
		return nil, err
	}
	r, _, err := provider.OpenResult(ctx, job)
	if err != nil {
		return nil, ErrBatchImageResultMissing.WithCause(err)
	}
	defer func() { _ = r.Close() }()

	streamCtx := ctx
	cancel := func() {}
	if d := s.MaxDownloadDuration(); d > 0 {
		streamCtx, cancel = context.WithTimeout(ctx, d)
	}
	defer cancel()

	limitedWriter := &BatchImageDownloadLimitWriter{w: w, limit: s.MaxDownloadBytes()}
	zipWriter := zip.NewWriter(limitedWriter)
	result, manifestFiles, zipErrors, err := s.WriteZipImages(streamCtx, zipWriter, r, successItems)
	if err != nil {
		_ = zipWriter.Close()
		if errors.Is(err, ErrBatchImageDownloadSizeExceeded) {
			return result, ErrBatchImageDownloadTooLarge.WithCause(err)
		}
		return result, ErrBatchImageDownloadFailed.WithCause(err)
	}
	zipErrors = append(zipErrors, BatchImageZipErrorsFromItems(failedItems)...)
	if err := WriteBatchImageZipJSON(zipWriter, "manifest.json", BatchImageZipManifest{
		BatchID:      job.BatchID,
		Model:        BatchImageRequestedModel(job),
		ItemCount:    job.ItemCount,
		SuccessCount: job.SuccessCount,
		FailCount:    job.FailCount,
		Files:        manifestFiles,
	}); err != nil {
		_ = zipWriter.Close()
		if errors.Is(err, ErrBatchImageDownloadSizeExceeded) {
			return result, ErrBatchImageDownloadTooLarge.WithCause(err)
		}
		return result, ErrBatchImageDownloadFailed.WithCause(err)
	}
	if err := WriteBatchImageZipJSON(zipWriter, "errors.json", zipErrors); err != nil {
		_ = zipWriter.Close()
		if errors.Is(err, ErrBatchImageDownloadSizeExceeded) {
			return result, ErrBatchImageDownloadTooLarge.WithCause(err)
		}
		return result, ErrBatchImageDownloadFailed.WithCause(err)
	}
	result.ErrorCount = len(zipErrors)
	if err := zipWriter.Close(); err != nil {
		if errors.Is(err, ErrBatchImageDownloadSizeExceeded) {
			return result, ErrBatchImageDownloadTooLarge.WithCause(err)
		}
		return result, ErrBatchImageDownloadFailed.WithCause(err)
	}
	return result, nil
}
func (s *Download) WriteZipImages(ctx context.Context, zipWriter *zip.Writer, resultReader io.Reader, successItems []*BatchImageItem) (*BatchImageZipResult, []BatchImageZipManifestFile, []BatchImageZipError, error) {
	successByID := make(map[string]*BatchImageItem, len(successItems))
	missing := make(map[string]struct{}, len(successItems))
	for _, item := range successItems {
		if item == nil {
			continue
		}
		successByID[item.CustomID] = item
		missing[item.CustomID] = struct{}{}
	}
	scanner := bufio.NewScanner(resultReader)
	scanner.Buffer(make([]byte, 0, 64*1024), BatchImageDownloadScannerMaxLineBytes)

	result := &BatchImageZipResult{}
	var manifestFiles []BatchImageZipManifestFile
	var zipErrors []BatchImageZipError
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return result, manifestFiles, zipErrors, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		images, err := ExtractBatchImagePartsFromResultLine([]byte(line))
		if err != nil {
			return result, manifestFiles, zipErrors, err
		}
		item := successByID[images.CustomID]
		if item == nil {
			continue
		}
		delete(missing, images.CustomID)
		if len(images.Images) == 0 {
			zipErrors = append(zipErrors, BatchImageZipError{CustomID: images.CustomID, Code: "EMPTY_IMAGE_OUTPUT", Message: "provider response contained no image output"})
			continue
		}
		for idx, image := range images.Images {
			extension := image.Extension
			if extension == "" {
				extension = "bin"
			}
			filename := BatchImageZipImageFilename(item.CustomID, idx, extension)
			entry, err := zipWriter.CreateHeader(&zip.FileHeader{Name: filename, Method: zip.Deflate})
			if err != nil {
				return result, manifestFiles, zipErrors, err
			}
			decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(image.Base64Data))
			if _, err := io.Copy(entry, decoder); err != nil {
				zipErrors = append(zipErrors, BatchImageZipError{CustomID: item.CustomID, Code: "IMAGE_DECODE_FAILED", Message: "image data could not be decoded"})
				continue
			}
			result.FileCount++
			manifestFiles = append(manifestFiles, BatchImageZipManifestFile{
				CustomID:   item.CustomID,
				Filename:   filename,
				MimeType:   image.MimeType,
				ImageIndex: idx,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return result, manifestFiles, zipErrors, err
	}
	missingIDs := make([]string, 0, len(missing))
	for customID := range missing {
		missingIDs = append(missingIDs, customID)
	}
	sort.Strings(missingIDs)
	for _, customID := range missingIDs {
		zipErrors = append(zipErrors, BatchImageZipError{CustomID: customID, Code: "RESULT_MISSING", Message: "provider result was not found for item"})
	}
	return result, manifestFiles, zipErrors, nil
}
func (s *Download) GetCompletedJob(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImageJob, error) {
	if s == nil || s.Repo == nil {
		return nil, ErrBatchImageDownloadFailed
	}
	job, err := s.Repo.GetBatchImageJobForDownload(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case BatchImageJobStatusCompleted:
		return job, nil
	case BatchImageJobStatusOutputDeleted:
		return nil, ErrBatchImageOutputDeleted
	default:
		return nil, ErrBatchImageNotReady
	}
}
func (s *Download) AcquirePermit(ctx context.Context, userID int64, kind string) (BatchImageDownloadPermit, error) {
	if s == nil || s.Limiter == nil {
		return nil, nil
	}
	permit, err := s.Limiter.Acquire(ctx, fmt.Sprintf("%d", userID), kind)
	if err != nil {
		if infraerrors.CategoryOf(err) == infraerrors.CategoryTooManyRequests {
			return nil, ErrBatchImageDownloadLimited
		}
		return nil, ErrBatchImageDownloadLimited.WithCause(err)
	}
	return permit, nil
}
func (s *Download) MaxZipItems() int {
	if s != nil && s.Options.MaxItems > 0 {
		return s.Options.MaxItems
	}
	return DefaultBatchImageZipMaxItems
}
func (s *Download) MaxDownloadBytes() int64 {
	if s != nil && s.Options.MaxBytes > 0 {
		return s.Options.MaxBytes
	}
	return DefaultBatchImageZipMaxBytes
}
func (s *Download) MaxDownloadDuration() time.Duration {
	if s != nil && s.Options.Duration > 0 {
		return s.Options.Duration
	}
	return DefaultBatchImageDownloadDuration
}
func ExtractBatchImagePartsFromResultLine(line []byte) (*BatchImageLineImages, error) {
	var obj map[string]any
	if err := json.Unmarshal(line, &obj); err != nil {
		return nil, ErrBatchImageIndexParseFailed.WithCause(err)
	}
	customID := BatchImageFirstNonEmptyString(
		BatchImageMapString(obj, "key"),
		BatchImageMapString(obj, "custom_id"),
		BatchImageMapString(obj, "customId"),
		BatchImageNestedString(obj, "request", "key"),
	)
	if customID == "" {
		return nil, ErrBatchImageIndexParseFailed.WithCause(fmt.Errorf("missing custom id"))
	}
	out := &BatchImageLineImages{CustomID: customID}
	out.Images = append(out.Images, ExtractBatchImageInlineImages(BatchImageNestedAny(obj, "response", "candidates"))...)
	out.Images = append(out.Images, ExtractBatchImageInlineImages(obj["candidates"])...)
	if len(out.Images) > 0 {
		return out, nil
	}
	if code, message, ok := BatchImageFailureFromProviderFields(obj); ok {
		out.ErrorCode = code
		out.ErrorMessage = TruncateBatchImageMessage(message, BatchImageMaxErrorMessageLength)
		return out, nil
	}
	if _, hasResponse := obj["response"]; hasResponse || BatchImageHasCandidates(obj) {
		out.ErrorCode = "EMPTY_IMAGE_OUTPUT"
		out.ErrorMessage = "provider response contained no image output"
		return out, nil
	}
	out.ErrorCode = "PROVIDER_ITEM_FAILED"
	out.ErrorMessage = "provider result line contained no image output"
	return out, nil
}
func ExtractBatchImageInlineImages(raw any) []BatchImageInlineImage {
	candidates, ok := raw.([]any)
	if !ok {
		return nil
	}
	var images []BatchImageInlineImage
	for _, candidateRaw := range candidates {
		candidate, ok := candidateRaw.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := BatchImageNestedAny(candidate, "content", "parts").([]any)
		if !ok {
			continue
		}
		for _, partRaw := range parts {
			part, ok := partRaw.(map[string]any)
			if !ok {
				continue
			}
			inline, ok := FirstMap(part["inlineData"], part["inline_data"])
			if !ok {
				continue
			}
			data := strings.TrimSpace(BatchImageMapString(inline, "data"))
			mime := strings.TrimSpace(BatchImageFirstNonEmptyString(BatchImageMapString(inline, "mimeType"), BatchImageMapString(inline, "mime_type")))
			if data == "" || !strings.HasPrefix(strings.ToLower(mime), "image/") {
				continue
			}
			images = append(images, BatchImageInlineImage{
				MimeType:   mime,
				Extension:  BatchImageFileExtension(mime),
				Base64Data: data,
			})
		}
	}
	return images
}
func FindBatchImageLineImages(r io.Reader, customID string) (*BatchImageLineImages, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), BatchImageDownloadScannerMaxLineBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parsed, err := ExtractBatchImagePartsFromResultLine([]byte(line))
		if err != nil {
			return nil, err
		}
		if parsed.CustomID != customID {
			continue
		}
		if len(parsed.Images) == 0 {
			if parsed.ErrorCode != "" {
				return nil, ErrBatchImageItemFailed
			}
			return nil, ErrBatchImageResultMissing
		}
		return parsed, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, ErrBatchImageDownloadFailed.WithCause(err)
	}
	return nil, ErrBatchImageResultMissing
}
func BatchImageSafeDownloadFilename(customID, extension string) string {
	base := SanitizeBatchImageFilenameBase(customID)
	extension = SanitizeBatchImageFilenameExtension(extension)
	if extension == "" {
		extension = "bin"
	}
	return base + "." + extension
}
func BatchImageContentDispositionAttachment(filename string) string {
	filename = strings.ReplaceAll(filename, "\\", "_")
	filename = strings.ReplaceAll(filename, `"`, "_")
	filename = SanitizeBatchImageFilenameBase(strings.TrimSuffix(filename, filepath.Ext(filename))) + filepath.Ext(filename)
	return `attachment; filename="` + filename + `"`
}
func SanitizeBatchImageFilenameBase(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "image"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0:
			_ = b.WriteByte('_')
		case unicode.IsControl(r):
			_ = b.WriteByte('_')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.':
			_, _ = b.WriteRune(r)
		default:
			_ = b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ". ")
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", "_")
	}
	out = strings.Trim(out, ". ")
	if out == "" {
		out = "image"
	}
	if len(out) > 120 {
		out = strings.TrimRight(out[:120], ". ")
	}
	if out == "" {
		out = "image"
	}
	return out
}
func SanitizeBatchImageFilenameExtension(extension string) string {
	extension = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(extension)), ".")
	var b strings.Builder
	for _, r := range extension {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			_, _ = b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}
func BatchImageZipImageFilename(customID string, imageIndex int, extension string) string {
	base := SanitizeBatchImageFilenameBase(customID)
	if imageIndex > 0 {
		base = fmt.Sprintf("%s_%d", base, imageIndex+1)
	}
	return "images/" + BatchImageSafeDownloadFilename(base, extension)
}
func WriteBatchImageZipJSON(zipWriter *zip.Writer, name string, value any) error {
	entry, err := zipWriter.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type BatchImageZipManifest struct {
	BatchID      string                      `json:"batch_id"`
	Model        string                      `json:"model"`
	ItemCount    int                         `json:"item_count"`
	SuccessCount int                         `json:"success_count"`
	FailCount    int                         `json:"fail_count"`
	Files        []BatchImageZipManifestFile `json:"files"`
}
type BatchImageZipManifestFile struct {
	CustomID   string `json:"custom_id"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mime_type"`
	ImageIndex int    `json:"image_index"`
}
type BatchImageZipError struct {
	CustomID string `json:"custom_id"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func BatchImageZipErrorsFromItems(items []*BatchImageItem) []BatchImageZipError {
	out := make([]BatchImageZipError, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		out = append(out, BatchImageZipError{
			CustomID: item.CustomID,
			Code:     BatchImageDerefString(item.ErrorCode),
			Message:  SanitizeBatchImagePublicMessage(BatchImageDerefString(item.ErrorMessage)),
		})
	}
	return out
}

type BatchImagePermitReadCloser struct {
	io.Reader
	permit BatchImageDownloadPermit
	once   sync.Once
	err    error
}

func (r *BatchImagePermitReadCloser) Close() error {
	r.once.Do(func() {
		if r.permit != nil {
			r.err = r.permit.Release(context.Background())
		}
	})
	return r.err
}
