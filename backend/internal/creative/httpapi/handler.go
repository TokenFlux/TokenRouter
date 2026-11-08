package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// handler.go parses creative submissions and serves task metadata, image
// content, and acknowledgments.
// Creative HTTP requests use the authenticated user and browser workspace as
// separate task-access inputs. Model and upload-capability reads need the
// user, while task creation, history, content, and acknowledgment also require
// a normalized workspace UUID. An optional activity callback keeps accepted
// requests registered until their response finishes.
//
// Multipart parsing bounds the request body and cumulative file bytes before
// submission validation. Uploaded sources retain arrival order and MIME
// labels, text operations are normalized, and output format plus optional GIF
// controls are passed to domain validation. History responses place task arrays directly in the common
// envelope; active pagination encodes a decimal offset in base64 rather than
// capturing a stable task snapshot. Output content uses private no-store
// headers instead of the JSON envelope.

const (
	// creativeMaxUploadPartSize supplies the parser file-part default when no
	// positive service limit is available; the additional read byte detects overflow.
	creativeMaxUploadPartSize = 40 << 20

	// creativeMultipartOverheadBytes adds room for text fields and boundaries to
	// the request-body cap while file bytes retain their separate cumulative limit.
	creativeMultipartOverheadBytes = 1 << 20
)

// CreativeHandler serves authenticated creative model, task, and output
// operations through HTTP.
type CreativeHandler struct {
	// enter optionally registers this request before authentication and returns
	// a nonnil completion callback, which the handler defers until response exit.
	enter   func() (func(), error) // optional request activity admission
	service CreativeUseCases       // bound creative use case service
}

// creativeCreateRunRequest retains parsed creative fields and uploaded image
// buffers for submission.
type creativeCreateRunRequest struct {
	OutputFormat    string // requested output format name
	FrameCount      *int   // requested animation frame count
	FrameDurationMS *int   // requested frame delay milliseconds
	GroupID         int64  // requested target group identifier
	Model           string // requested public model name
	Operation       string // normalized creative operation name
	Prompt          string // trimmed multipart prompt text
	// source uploads in arrival order
	SourceImages []creative.CreativeInputImage
	// Mask keeps the last nonempty mask upload, while later empty parts leave
	// that value present. Submission validation checks its operation and size.
	Mask          *creative.CreativeInputImage // last nonempty mask upload
	ImageSize     string                       // requested image size tier
	AspectRatio   string                       // requested image aspect ratio
	Quality       string                       // requested image quality option
	Background    string                       // normalized background option name
	ThinkingLevel string                       // normalized thinking level name
}

// CreativeUseCases supplies run operations to the HTTP handler.
type CreativeUseCases interface {
	// ListModels returns user-bindable creative model entries for the HTTP
	// directory response. A disabled feature returns an empty list through the
	// service, while permission and storage errors remain errors.
	ListModels(context.Context, int64) (*creative.CreativeModelsResponse, error)
	// GetCapabilities returns creative upload limits and accepted image MIME
	// types for clients.
	GetCapabilities(context.Context) *creative.CreativeCapabilitiesResponse
	// MaxAssetBytes returns the upload limit in bytes for one input file.
	MaxAssetBytes() int64
	// MaxTotalInputBytes returns the combined source and mask input limit in
	// bytes.
	MaxTotalInputBytes() int64
	// CreateRun validates and provisions a task within the supplied
	// user/workspace scope. Its idempotency key reuses an existing matching
	// request or returns a fingerprint conflict for a changed body.
	CreateRun(context.Context, creative.CreativeRunScope, creative.CreateCreativeRunParamsPublic, string) (*creative.CreativeRunPublic, error)
	// ListRuns returns task and output metadata within the supplied
	// user/workspace scope. The filter selects history or active states and
	// pagination, while the response reports possible further rows.
	ListRuns(context.Context, creative.CreativeRunScope, creative.CreativeRunFilter) (*creative.CreativeListRunsResponse, error)
	// GetRun returns task details after the supplied scope passes ownership
	// checks. A task outside that user or workspace is treated as missing by the
	// service.
	GetRun(context.Context, creative.CreativeRunScope, string) (*creative.CreativeRunPublic, error)
	// GetOutputContent returns temporary image bytes and MIME after scope and
	// state checks. Missing or expired delivery remains a typed service error for
	// the HTTP layer to map.
	GetOutputContent(context.Context, creative.CreativeRunScope, string, int) (*creative.CreativeOutputContent, error)
	// AckOutput records client receipt within the supplied user/workspace scope
	// before transient deletion. Repeat receipt succeeds under service rules, and
	// successful acknowledgment returns no image body.
	AckOutput(context.Context, creative.CreativeRunScope, string, int) error
}

// NewCreativeHandler binds required creative operations for user HTTP
// requests. The returned handler has no activity gate until BindActivity is
// called, and callers provide the service before serving requests.
func NewCreativeHandler(service CreativeUseCases) *CreativeHandler {
	return &CreativeHandler{service: service}
}

// creativeRunScopeFromRequest normalizes the browser workspace header and
// pairs it with the supplied authenticated user ID. Missing or invalid UUID
// input returns the corresponding workspace error before task operations run.
func creativeRunScopeFromRequest(c *gin.Context, userID int64) (creative.CreativeRunScope, error) {
	workspaceID, err := creative.NormalizeCreativeWorkspaceID(c.GetHeader(creative.CreativeWorkspaceHeader))
	if err != nil {
		return creative.CreativeRunScope{}, err
	}
	return creative.CreativeRunScope{UserID: userID, WorkspaceID: workspaceID}, nil
}

// ListModels returns the model array in the common success envelope for the
// authenticated user. An installed activity gate can reject stopping requests
// before authentication, and service errors retain their HTTP mapping.
func (h *CreativeHandler) ListModels(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	got, err := h.service.ListModels(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Place the array directly in the response envelope data field.
	response.Success(c, got.Data)
}

// ListCapabilities returns server input limits after requiring an
// authenticated user. The route needs no workspace header, and an installed
// activity gate covers the response until the deferred completion callback
// runs.
func (h *CreativeHandler) ListCapabilities(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	if _, ok := middleware2.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	response.Success(c, h.service.GetCapabilities(c.Request.Context()))
}

// CreateRun bounds and parses multipart uploads, then submits normalized
// fields with the request idempotency key. It requires an authenticated user
// and valid workspace before reading the upload body. Service limits override
// parser defaults, upload-size errors retain their specific HTTP mapping, and
// other parser failures become CREATIVE_INVALID_PARAMS. Successful creation
// returns the public run in the common envelope.
func (h *CreativeHandler) CreateRun(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	partLimit := int64(creativeMaxUploadPartSize)
	totalInputLimit := int64(64 << 20)
	if h.service != nil && h.service.MaxAssetBytes() > 0 {
		partLimit = h.service.MaxAssetBytes()
	}
	if h.service != nil && h.service.MaxTotalInputBytes() > 0 {
		totalInputLimit = h.service.MaxTotalInputBytes()
	}
	// Cap the entire body before multipart iteration so unread file and text
	// bytes also share a finite request budget, with separate room for form
	// boundaries beyond the cumulative image-byte limit.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, totalInputLimit+creativeMultipartOverheadBytes)
	req, err := parseCreativeCreateRunMultipart(c, partLimit, totalInputLimit)
	if err != nil {
		if errors.Is(err, creative.ErrCreativeAssetTooLarge) || errors.Is(err, creative.ErrCreativeInputTooLarge) {
			response.ErrorFrom(c, err)
			return
		}
		response.ErrorFrom(c, creative.ErrCreativeInvalidParams)
		return
	}
	got, err := h.service.CreateRun(c.Request.Context(), scope, creative.CreateCreativeRunParamsPublic{
		OutputFormat: req.OutputFormat, FrameCount: req.FrameCount, FrameDurationMS: req.FrameDurationMS,
		GroupID:       req.GroupID,
		Model:         req.Model,
		Operation:     req.Operation,
		Prompt:        req.Prompt,
		SourceImages:  req.SourceImages,
		Mask:          req.Mask,
		ImageSize:     req.ImageSize,
		AspectRatio:   req.AspectRatio,
		Quality:       req.Quality,
		Background:    req.Background,
		ThinkingLevel: req.ThinkingLevel,
	}, c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, got)
}

// parseCreativeCreateRunMultipart assembles creative input fields while
// bounding file reads against per-part and cumulative limits. It requires a
// parseable Content-Type with a nonblank boundary and defaults nonpositive
// total limits to sixty-four MiB.
//
// File bytes count toward the total even when their field is ignored. Source
// aliases preserve arrival order, later nonempty masks replace earlier masks,
// and other recognized text fields keep the last trimmed value. File reads
// include one extra byte to distinguish limit overflow; text reads retain at
// most one MiB without a separate overflow error. Group ID is required, while
// domain validation owns prompt, operation, MIME, and model rules.
func parseCreativeCreateRunMultipart(c *gin.Context, partLimit, totalInputLimit int64) (*creativeCreateRunRequest, error) {
	contentType := c.GetHeader("Content-Type")
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, http.ErrMissingBoundary
	}
	req := &creativeCreateRunRequest{}
	if totalInputLimit <= 0 {
		totalInputLimit = 64 << 20
	}
	var totalFileBytes int64
	reader := multipart.NewReader(c.Request.Body, boundary)
	// Every acquired part is closed after its read or before a guard returns.
	// Closing consumes any unread remainder; close errors are discarded so the
	// parser reports its read, limit, or field-validation result.
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		fileName := strings.TrimSpace(part.FileName())
		if fileName != "" {
			if partLimit <= 0 {
				partLimit = creativeMaxUploadPartSize
			}
			remaining := totalInputLimit - totalFileBytes
			if remaining <= 0 {
				_ = part.Close()
				return nil, creative.ErrCreativeInputTooLarge
			}
			readLimit := min(remaining, partLimit)
			// One extra byte distinguishes overflow from an exactly
			// full file part.
			// When cumulative remaining space is smaller, overflow
			// belongs to the
			// total-input error rather than the individual asset-size error.
			data, readErr := io.ReadAll(io.LimitReader(part, readLimit+1))
			_ = part.Close()
			if readErr != nil {
				if _, ok := errors.AsType[*http.MaxBytesError](readErr); ok {
					return nil, creative.ErrCreativeInputTooLarge
				}
				return nil, readErr
			}
			if int64(len(data)) > readLimit {
				if readLimit < partLimit {
					return nil, creative.ErrCreativeInputTooLarge
				}
				return nil, creative.ErrCreativeAssetTooLarge
			}
			totalFileBytes += int64(len(data))
			partMIME := normalizeCreativeUploadMime(strings.TrimSpace(part.Header.Get("Content-Type")), data)
			// Source aliases append to one ordered collection, so
			// plain and bracketed
			// field names preserve the same reference ordering.
			// Nonempty mask uploads
			// instead replace the prior mask used by submission validation.
			switch {
			case name == "mask":
				if len(data) > 0 {
					req.Mask = &creative.CreativeInputImage{Bytes: data, Mime: partMIME}
				}
			case name == "source_images" || name == "source_images[]" || strings.HasPrefix(name, "source_images["):
				if len(data) > 0 {
					req.SourceImages = append(req.SourceImages, creative.CreativeInputImage{Bytes: data, Mime: partMIME})
				}
			}
			continue
		}
		// Text fields retain this bounded prefix without an overflow
		// error; closing
		// the part consumes its remainder before the next field is interpreted.
		data, readErr := io.ReadAll(io.LimitReader(part, 1<<20))
		_ = part.Close()
		if readErr != nil {
			return nil, readErr
		}
		value := strings.TrimSpace(string(data))
		switch name {
		case "group_id":
			groupID, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || groupID <= 0 {
				return nil, creative.ErrCreativeInvalidParams
			}
			req.GroupID = groupID
		case "model":
			req.Model = value
		case "operation":
			req.Operation = strings.ToLower(value)
		case "prompt":
			req.Prompt = value
		case "image_size":
			req.ImageSize = value
		case "aspect_ratio":
			req.AspectRatio = value
		case "quality":
			req.Quality = value
		case "output_format":
			req.OutputFormat = strings.ToLower(value)
		case "frame_count", "frame_duration_ms":
			number, err := strconv.Atoi(value)
			if err != nil {
				return nil, creative.ErrCreativeInvalidParams
			}
			if name == "frame_count" {
				req.FrameCount = &number
			} else {
				req.FrameDurationMS = &number
			}
		case "output_compression", "response_mime_type":
			// Output MIME comes from verified provider bytes. Compression and
			// response MIME overrides are not exposed by the creative input contract.
			return nil, creative.ErrCreativeInvalidParams
		case "background":
			req.Background = strings.ToLower(value)
		case "thinking_level":
			req.ThinkingLevel = strings.ToLower(value)
		}
	}
	if req.GroupID <= 0 {
		return nil, creative.ErrCreativeInvalidParams
	}
	return req, nil
}

// normalizeCreativeUploadMime returns a normalized recognized header MIME or a
// sniffed label for known signatures. An unrecognized signature retains the
// lowercased header, leaving definitive MIME checks to submission validation.
func normalizeCreativeUploadMime(headerMIME string, data []byte) string {
	switch strings.ToLower(strings.TrimSpace(headerMIME)) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/webp":
		return "image/webp"
	}
	// Recognized header labels returned above take precedence over byte signatures.
	// Probe unrecognized headers here so uploads with missing or unusable labels
	// can still reach domain validation with a supported image MIME.
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return strings.ToLower(strings.TrimSpace(headerMIME))
}

// ListRuns returns the authenticated workspace history array in the response
// envelope. Limit parsing can yield zero for invalid text, which the service
// normalizes; history starts at offset zero.
func (h *CreativeHandler) ListRuns(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	got, err := h.service.ListRuns(c.Request.Context(), scope, creative.CreativeRunFilter{
		Status: strings.TrimSpace(c.Query("status")),
		Limit:  limit,
		Offset: 0,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// Place the run array directly in the response envelope data field.
	response.Success(c, got.Data)
}

// ListActiveRuns returns an active task page using a base64-encoded decimal
// offset cursor. Authenticated workspace scope is required, invalid cursors
// fail with CREATIVE_INVALID_PARAMS, and limits outside one to one hundred use
// one hundred.
//
// The service active filter determines included states. HasMore produces the
// next cursor from the current offset plus returned row count, so this
// pagination follows changing offset-based lists rather than a frozen result
// set. The response includes items, next_cursor, and has_more in one envelope.
func (h *CreativeHandler) ListActiveRuns(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset := 0
	// The cursor encodes the next offset as URL-safe unpadded base64. Decode
	// and validate it before listing so malformed or negative positions cannot
	// enter the repository pagination filter.
	if rawCursor := strings.TrimSpace(c.Query("cursor")); rawCursor != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(rawCursor)
		if decodeErr != nil {
			response.ErrorFrom(c, creative.ErrCreativeInvalidParams)
			return
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil || offset < 0 {
			response.ErrorFrom(c, creative.ErrCreativeInvalidParams)
			return
		}
	}
	got, err := h.service.ListRuns(c.Request.Context(), scope, creative.CreativeRunFilter{
		Status: "active",
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	nextCursor := ""
	if got.HasMore {
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(got.Data))))
	}
	response.Success(c, gin.H{
		"items":       got.Data,
		"next_cursor": nextCursor,
		"has_more":    got.HasMore,
	})
}

// GetRun returns task details for the authenticated browser workspace using
// the route identifier. The scope helper rejects invalid workspace headers
// before the service applies resource ownership checks.
func (h *CreativeHandler) GetRun(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	got, err := h.service.GetRun(c.Request.Context(), scope, c.Param("id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, got)
}

// GetOutputContent serves transient image bytes for the authenticated
// workspace and nonnegative output index. Service errors use common HTTP
// mapping; successful binary content carries private no-store and nosniff
// headers.
func (h *CreativeHandler) GetOutputContent(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	outputIndex, err := strconv.Atoi(c.Param("index"))
	if err != nil || outputIndex < 0 {
		response.ErrorFrom(c, creative.ErrCreativeOutputNotFound)
		return
	}
	content, err := h.service.GetOutputContent(c.Request.Context(), scope, c.Param("id"), outputIndex)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, content.ContentType, content.Content)
}

// AckOutput acknowledges the authenticated workspace output named by the route
// and nonnegative index. The service records receipt and requests transient
// deletion, and the handler returns an acked success object.
func (h *CreativeHandler) AckOutput(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			response.ErrorFrom(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	scope, err := creativeRunScopeFromRequest(c, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	outputIndex, err := strconv.Atoi(c.Param("index"))
	if err != nil || outputIndex < 0 {
		response.ErrorFrom(c, creative.ErrCreativeOutputNotFound)
		return
	}
	if err := h.service.AckOutput(c.Request.Context(), scope, c.Param("id"), outputIndex); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"acked": true})
}

// BindActivity installs the optional request activity admission callback
// before serving requests. A successful callback returns a nonnil completion
// function, and every handler defers that function until its response path
// exits.
func (h *CreativeHandler) BindActivity(enter func() (func(), error)) { h.enter = enter }
