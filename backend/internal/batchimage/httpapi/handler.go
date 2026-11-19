package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// handler.go handles authenticated batch-image requests, model discovery, and
// streamed download responses.
// Batch image requests identify the job owner from the authenticated API key
// and carry a separate billing-user choice into the use cases. JSON parsing
// precedes owner lookup on submission, while model discovery expands available
// composite groups and prefixes each returned model with its group binding.
// Exact aliases retain every target model's platform entry.
//
// Downloads keep admitted request activity alive through response writing.
// Item responses close their returned content stream and record completion
// after a successful copy. ZIP responses may fail after output has begun, when
// an error response would compete with archive bytes. Download-recording errors are
// logged after delivery, leaving response completion separate from metadata
// persistence.

// BatchImageHandler translates authenticated gateway requests into batch image
// use cases and streamed download responses.
type BatchImageHandler struct {
	// enter is optional and configured by BindActivity before requests begin.
	// When present, every route releases its successful admission after response
	// handling, including stream consumption and closure.
	enter func() (func(), error) // request activity admission callback
	// service, download, and cleanup are retained dependencies for the same
	// public job identity. Configure them before serving and keep their backing
	// stores and provider bindings available while handlers run.
	service  UseCases         // batch job management use cases
	download DownloadUseCases // streamed content download use cases
	cleanup  CleanupUseCases  // owned output cleanup use cases
	access   AccessPorts      // authenticated request value callbacks
}

// AccessPorts reads authenticated key, billing subscription, and session
// values supplied by gateway middleware.
type AccessPorts struct {
	// Key is called by owner and again during model discovery.
	// It must be callable and return a stable authenticated key view for this
	// request's user, key, group, and billing selections.
	Key func(*gin.Context) (*apikey.APIKey, bool) // authenticated API key lookup
	// PreferredSubscription is required when a composite key uses subscription
	// billing. Its false or nil result makes Models return an empty list before
	// querying any binding.
	PreferredSubscription func(*gin.Context) (*billing.UserSubscription, bool) // selected billing subscription lookup
	// SessionID is called by Submit after owner lookup succeeds.
	// A nonempty returned value replaces the request's session field before the
	// use case receives its input.
	SessionID func(*gin.Context) string // request client session identifier
}

// UseCases supplies owner-scoped job creation, discovery, and management to
// the HTTP handlers.
type UseCases interface {
	// Submit creates or reuses a batch for the supplied owner and idempotency key.
	// The implementation validates the input and carries the selected billing
	// mode into the job's funding snapshot.
	Submit(context.Context, batchimage.BatchImageOwner, batchimage.BatchImageSubmitRequest, string) (*batchimage.BatchImagePublicBatch, error)
	// Get returns the public job belonging to the supplied user and API key.
	// Missing, hidden, or differently owned records return an error that the
	// handler serializes as a batch failure.
	Get(context.Context, batchimage.BatchImageOwner, string) (*batchimage.BatchImagePublicBatch, error)
	// List returns the owner's public jobs using the supplied filters and cursor.
	// The implementation normalizes query values and pagination limits. The handler
	// forwards Atoi's value after discarding its error: syntax failures yield zero,
	// while range failures yield a saturated int.
	List(context.Context, batchimage.BatchImageOwner, batchimage.BatchImageJobsQuery) (*batchimage.BatchImagePublicListResponse, error)
	// ListModels returns batch-capable model entries for the owner's selected group.
	// The implementation applies group, provider, and pricing eligibility and returns
	// a nonnil response on success for handler alias expansion.
	ListModels(context.Context, batchimage.BatchImageOwner) (*batchimage.BatchImagePublicModelsResponse, error)
	// ListItems returns public indexed items after checking the job's user and
	// API key ownership. The implementation interprets status and cursor values
	// and applies its item pagination limits.
	ListItems(context.Context, batchimage.BatchImageOwner, string, batchimage.BatchImageItemsQuery) (*batchimage.BatchImagePublicItemsResponse, error)
	// Cancel requests cancellation of the owner's job and returns its public state.
	// Cancellation and funding effects follow the use case's lifecycle rules;
	// errors are returned to the handler for serialization.
	Cancel(context.Context, batchimage.BatchImageOwner, string) (*batchimage.BatchImagePublicBatch, error)
	// MarkDownloaded records successful delivery for the owner's visible job.
	// Handlers call it after response writing succeeds, and a persistence error
	// is logged because the content response has already been delivered.
	MarkDownloaded(context.Context, batchimage.BatchImageOwner, string) error
	// DeleteRecord hides an owner's record when the job's lifecycle permits deletion.
	// The public use case checks processor completion, and persistence applies its
	// terminal-state guard while retaining metadata for billing and cleanup.
	DeleteRecord(context.Context, batchimage.BatchImageOwner, string) error
}

// DownloadUseCases supplies owner-checked content streams and ZIP output for
// HTTP response writers.
type DownloadUseCases interface {
	// OpenItemContent opens one selected image from an owner's completed job.
	// It returns content metadata with a stream that the handler closes after
	// copying; invalid image indexes and unavailable output return errors.
	OpenItemContent(context.Context, batchimage.BatchImageOwner, string, string, int) (*batchimage.BatchImageContentStream, error)
	// StreamZip writes the owner's result archive to the supplied writer and
	// returns progress with any error. Once archive output begins, implementations
	// return a nonnil result so the handler can distinguish streaming failure
	// from failure before output.
	StreamZip(context.Context, batchimage.BatchImageOwner, string, batchimage.BatchImageZipOptions, io.Writer) (*batchimage.BatchImageZipResult, error)
}

// CleanupUseCases supplies owner-checked output deletion for completed batch
// image jobs.
type CleanupUseCases interface {
	// DeleteOutputsForOwner removes the owner's completed output and returns its
	// public deletion state. Already deleted output can return the existing state;
	// unfinished jobs return the use case's deletion-not-ready error.
	DeleteOutputsForOwner(context.Context, batchimage.BatchImageOwner, string) (*batchimage.BatchImagePublicBatch, error)
}

// NewBatchImageHandler retains the use cases and request-access callbacks.
// Callers provide the dependencies used by their registered routes, including
// Key and submission's SessionID, and bind activity before serving requests.
func NewBatchImageHandler(service UseCases, download DownloadUseCases, cleanup CleanupUseCases, access AccessPorts) *BatchImageHandler {
	return &BatchImageHandler{service: service, download: download, cleanup: cleanup, access: access}
}

// Submit parses a strict JSON request before reading its authenticated owner.
// It carries a nonempty request session ID and the Idempotency-Key header into
// submission, writes the public result, and releases any admitted activity
// when response handling returns.
func (h *BatchImageHandler) Submit(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	if c.Request == nil || c.Request.Body == nil {
		BatchImageError(c, batchimage.ErrBatchImageInvalidItems)
		return
	}
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil || wirejson.ValidateRequestObject(rawBody) != nil {
		BatchImageError(c, batchimage.ErrBatchImageInvalidItems)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))
	var req batchimage.BatchImageSubmitRequest
	if err := infraerrors.BindJSONStrict(c, &req); err != nil {
		BatchImageError(c, batchimage.ErrBatchImageInvalidItems)
		return
	}
	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if sessionID := h.access.SessionID(c); sessionID != "" {
		req.SessionID = &sessionID
	}
	got, err := h.service.Submit(c.Request.Context(), owner, req, c.GetHeader("Idempotency-Key"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// Get returns a public job for the authenticated user and API key.
// It passes the path ID to the owner-scoped use case and serializes lookup
// or admission errors before writing a successful JSON response.
func (h *BatchImageHandler) Get(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Get(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// List passes owner-scoped job filters and pagination to the listing use case.
// Limit syntax errors produce zero, while range errors produce a saturated int
// for downstream normalization. Status, downloaded, time, and cursor values
// retain their supplied spellings.
func (h *BatchImageHandler) List(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	// Discard parse errors because the listing use case owns pagination bounds.
	// Syntax failures supply zero and range failures supply a saturated int; those
	// values then receive the same bounds check as other requested limits.
	limit, _ := strconv.Atoi(c.Query("limit"))
	got, err := h.service.List(c.Request.Context(), owner, batchimage.BatchImageJobsQuery{
		Status:     c.Query("status"),
		TaskName:   c.Query("task_name"),
		Downloaded: c.Query("downloaded"),
		From:       c.Query("from"),
		To:         c.Query("to"),
		Limit:      limit,
		Cursor:     c.Query("cursor"),
	})
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// Models returns batch model entries available to the authenticated key.
// Ordinary keys use their selected group; composite keys enumerate bindings
// available to their user and selected subscription before querying each group.
//
// A missing required subscription produces an empty list. Eligible composite
// groups also need batch-image permission; each group's aliases are expanded
// before its prefix is added, keeping model and platform entries tied to the
// binding the client later requests. Any group lookup error aborts the combined
// response.
func (h *BatchImageHandler) Models(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	apiKey, _ := h.access.Key(c)
	if apiKey != nil && apiKey.IsComposite {
		preferredSubscription, ready := h.preferred(c, apiKey)
		if !ready {
			// Subscription billing needs its selected subscription to decide group
			// availability. Missing middleware data yields an empty model list before any
			// group is queried, preventing unscoped enumeration.
			c.JSON(http.StatusOK, &batchimage.BatchImagePublicModelsResponse{Object: "list", Data: make([]batchimage.BatchImagePublicModel, 0)})
			return
		}
		out := &batchimage.BatchImagePublicModelsResponse{Object: "list", Data: make([]batchimage.BatchImagePublicModel, 0)}
		for _, binding := range apiKey.CompositeGroups {
			if !gatewayhttp.CompositeGroupAvailableToUser(apiKey, preferredSubscription, binding.Group) || !binding.Group.AllowBatchImageGeneration {
				continue
			}
			groupOwner := owner
			groupID := binding.GroupID
			groupOwner.GroupID = &groupID
			models, err := h.service.ListModels(c.Request.Context(), groupOwner)
			if err != nil {
				BatchImageError(c, err)
				return
			}
			for _, model := range AppendBatchImageAPIKeyModelAliases(models.Data, apiKey.ModelMapping) {
				// Attach the binding prefix after alias expansion so each advertised alias
				// selects the same composite group as its target when a client submits it.
				model.ID = binding.Prefix + "/" + model.ID
				out.Data = append(out.Data, model)
			}
		}
		c.JSON(http.StatusOK, out)
		return
	}
	got, err := h.service.ListModels(c.Request.Context(), owner)
	if err != nil {
		BatchImageError(c, err)
		return
	}
	if apiKey != nil {
		got.Data = AppendBatchImageAPIKeyModelAliases(got.Data, apiKey.ModelMapping)
	}
	c.JSON(http.StatusOK, got)
}

// AppendBatchImageAPIKeyModelAliases returns model rows followed by eligible
// exact aliases, retaining every platform row for each target ID. It copies
// the input slice's values and leaves the supplied mapping unchanged. Supply
// trimmed model IDs and normalized mapping targets so availability checks and
// template lookup address the same spelling.
//
// Alias availability is computed from the original model IDs, so appended
// aliases cannot admit another mapping hop. Templates are indexed by each
// supplied ID and selected by the mapping's target spelling; every matching
// template gets the alias ID, preserving multiple platforms for one model.
func AppendBatchImageAPIKeyModelAliases(models []batchimage.BatchImagePublicModel, mapping map[string]string) []batchimage.BatchImagePublicModel {
	modelIDs := make([]string, 0, len(models))
	templates := make(map[string][]batchimage.BatchImagePublicModel)
	for _, model := range models {
		modelIDs = append(modelIDs, model.ID)
		templates[model.ID] = append(templates[model.ID], model)
	}
	// Keep templates grouped by their original model ID before adding aliases.
	// One alias then receives every platform row for that target, preventing
	// duplicate-ID models from losing provider metadata.
	result := append([]batchimage.BatchImagePublicModel(nil), models...)
	for _, alias := range apikey.AvailableAPIKeyModelAliases(modelIDs, mapping) {
		for _, template := range templates[mapping[alias]] {
			template.ID = alias
			result = append(result, template)
		}
	}
	return result
}

// Items returns the authenticated owner's item page for the path batch ID.
// It passes status and cursor through to the use case. Limit syntax errors
// supply zero and range errors supply a saturated int; the use case then applies
// its pagination bounds.
func (h *BatchImageHandler) Items(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	// Discard parse errors because the owner-scoped item use case applies its own
	// pagination bounds. A syntax failure supplies zero, while an overflow supplies
	// the int limit for its sign; status and cursor validation stay with that lookup.
	limit, _ := strconv.Atoi(c.Query("limit"))
	got, err := h.service.ListItems(c.Request.Context(), owner, c.Param("id"), batchimage.BatchImageItemsQuery{
		Status: c.Query("status"),
		Limit:  limit,
		Cursor: c.Query("cursor"),
	})
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// Cancel asks the use case to cancel the authenticated owner's path job.
// It serializes the resulting public state or lifecycle error, and any activity
// admission remains held until that response handling returns.
func (h *BatchImageHandler) Cancel(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.service.Cancel(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// ItemContent streams one image from the authenticated owner's path item.
// An absent image_index selects zero; malformed numeric input returns the index
// error. The returned stream is closed when response handling ends.
//
// Content headers are set before copying, and download metadata is recorded
// only after the copy succeeds. Bytes written before a copy error remain in the
// response, and handling returns without appending a JSON error. The content
// reader still closes during unwinding, releasing its attached download permit.
func (h *BatchImageHandler) ItemContent(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	imageIndex := 0
	if raw := c.Query("image_index"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			BatchImageError(c, batchimage.ErrBatchImageItemImageIndexOutOfRange)
			return
		}
		imageIndex = parsed
	}
	stream, err := h.download.OpenItemContent(c.Request.Context(), owner, c.Param("id"), c.Param("custom_id"), imageIndex)
	if err != nil {
		BatchImageError(c, err)
		return
	}
	// Close the returned stream when copying finishes or fails so the download
	// use case can release its attached permit. Close errors are discarded after
	// response handling has chosen its delivery result.
	defer func() { _ = stream.Reader.Close() }()

	c.Header("Content-Type", stream.ContentType)
	c.Header("Content-Disposition", batchimage.BatchImageContentDispositionAttachment(stream.Filename))
	c.Header("Cache-Control", "private, max-age=300")
	c.Header("X-Content-Type-Options", "nosniff")
	if stream.ContentLength != nil && *stream.ContentLength >= 0 {
		c.Header("Content-Length", strconv.FormatInt(*stream.ContentLength, 10))
	}
	c.Status(http.StatusOK)
	// Copy errors can follow committed headers or image bytes. Stop delivery
	// handling at that point and skip download recording, since successful image
	// completion has not been observed.
	if _, err := io.Copy(c.Writer, stream.Reader); err != nil {
		return
	}
	h.markDownloadedBestEffort(c, owner)
}

// markDownloadedBestEffort records delivery after the response body succeeds.
// It uses the request context, so cancellation can still fail persistence;
// such failures are logged with the batch ID after response delivery.
func (h *BatchImageHandler) markDownloadedBestEffort(c *gin.Context, owner batchimage.BatchImageOwner) {
	// The body has already reached the response writer. Log metadata failures
	// with the batch ID because a second response cannot repair that completed
	// delivery or its download marker.
	if err := h.service.MarkDownloaded(c.Request.Context(), owner, c.Param("id")); err != nil {
		logging.L().Warn("batch_image.mark_downloaded_failed",
			zap.String("batch_id", c.Param("id")),
			zap.Error(err),
		)
	}
}

// Download streams a ZIP archive for the authenticated owner's path batch.
// It passes the requested item limit and includes a manifest while setting
// private archive headers before invoking the download use case.
//
// Errors before output, or errors with a nil result, are serialized as batch
// errors. When a nonnil result accompanies an already written response, handling
// returns with the partial archive; successful completion records download
// metadata after the archive writer has finished.
func (h *BatchImageHandler) Download(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	// Forward Atoi's value even on an error: syntax supplies zero and overflow
	// supplies a saturated int. StreamZip applies its configured cap or default
	// before opening provider output for archive writing.
	maxItems, _ := strconv.Atoi(c.Query("max_items"))

	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", batchimage.BatchImageContentDispositionAttachment(c.Param("id")+".zip"))
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	result, err := h.download.StreamZip(c.Request.Context(), owner, c.Param("id"), batchimage.BatchImageZipOptions{
		Status:          c.Query("status"),
		MaxItems:        maxItems,
		IncludeManifest: true,
	}, c.Writer)
	if err != nil {
		// A progress result plus written bytes identifies an archive already in
		// flight. Return its streaming error without appending JSON to those ZIP bytes;
		// earlier failures can still use the batch error response.
		if result == nil || !c.Writer.Written() {
			BatchImageError(c, err)
		}
		return
	}
	h.markDownloadedBestEffort(c, owner)
}

// DeleteRecord hides an owner's job once the use case permits record deletion.
// A successful use case returns HTTP 204; ownership, lifecycle, persistence,
// and activity-admission failures use the batch error response.
func (h *BatchImageHandler) DeleteRecord(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	if err := h.service.DeleteRecord(c.Request.Context(), owner, c.Param("id")); err != nil {
		BatchImageError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DeleteOutputs removes output for the authenticated owner's path job.
// The cleanup use case checks its lifecycle and returns the public deletion
// state; cleanup or admission failures use the batch error response.
func (h *BatchImageHandler) DeleteOutputs(c *gin.Context) {
	if h.enter != nil {
		done, err := h.enter()
		if err != nil {
			BatchImageError(c, apperror.New(apperror.CategoryServiceUnavailable, "TASKS_STOPPED", "task service is stopping"))
			return
		}
		defer done()
	}

	owner, ok := h.owner(c)
	if !ok {
		BatchImageError(c, apperror.New(apperror.CategoryUnauthorized, "API_KEY_REQUIRED", "API key is required"))
		return
	}
	got, err := h.cleanup.DeleteOutputsForOwner(c.Request.Context(), owner, c.Param("id"))
	if err != nil {
		BatchImageError(c, err)
		return
	}
	c.JSON(http.StatusOK, got)
}

// owner returns user, key, group, and billing selections from the authenticated
// key. Missing or nonpositive user/key identities fail lookup; a positive loaded
// User.ID overrides UserID for the payer, while optional identity pointers
// remain borrowed from that key.
func (h *BatchImageHandler) owner(c *gin.Context) (batchimage.BatchImageOwner, bool) {
	apiKey, ok := h.access.Key(c)
	if !ok || apiKey == nil || apiKey.ID <= 0 || apiKey.UserID <= 0 {
		return batchimage.BatchImageOwner{}, false
	}
	billingUserID := apiKey.UserID
	if apiKey.User != nil && apiKey.User.ID > 0 {
		billingUserID = apiKey.User.ID
	}
	return batchimage.BatchImageOwner{
		UserID:                  apiKey.UserID,
		BillingUserID:           billingUserID,
		TeamID:                  apiKey.TeamID,
		APIKeyID:                apiKey.ID,
		GroupID:                 apiKey.GroupID,
		BillingMode:             apikey.APIKeyEffectiveBillingMode(apiKey),
		PreferredSubscriptionID: apiKey.PreferredSubscriptionID,
	}, true
}

// BatchImageError writes the nested batch error shape with an HTTP status.
// Nil or unclassified internal errors receive fixed internal text, and missing
// jobs receive fixed not-found text; other classified errors retain their
// application reason and message.
func BatchImageError(c *gin.Context, err error) {
	status := infraerrors.ErrorCode(err)
	code := apperror.Reason(err)
	message := apperror.Message(err)
	if err == nil {
		status = http.StatusInternalServerError
		code = "INTERNAL_ERROR"
		message = "internal error"
	}
	// An unclassified internal failure has no stable client reason. Replace its
	// status and text together so arbitrary diagnostic content does not become the
	// public batch error message.
	if status == 0 || (status == http.StatusInternalServerError && strings.TrimSpace(code) == "") {
		status = http.StatusInternalServerError
		code = "INTERNAL_ERROR"
		message = "internal error"
	}
	if errors.Is(err, batchimage.ErrBatchImageJobNotFound) {
		status = http.StatusNotFound
		code = "BATCH_IMAGE_NOT_FOUND"
		message = "batch image job not found"
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    code,
			"message": message,
		},
	})
}

// preferred returns subscription readiness for composite model discovery.
// Modes other than subscription need no selected value; subscription mode requires
// a present nonnil context subscription before bindings can be enumerated.
func (h *BatchImageHandler) preferred(c *gin.Context, k *apikey.APIKey) (*billing.UserSubscription, bool) {
	if apikey.APIKeyEffectiveBillingMode(k) != apikey.APIKeyBillingModeSubscription {
		return nil, true
	}
	value, ok := h.access.PreferredSubscription(c)
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

// BindActivity installs request admission and completion callbacks before the
// handler serves requests. A successful enter returns a callable completion
// function; the handler defers it through response writing, including streamed
// output, and treats enter errors as stopped-task failures.
func (h *BatchImageHandler) BindActivity(enter func() (func(), error)) { h.enter = enter }
