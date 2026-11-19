package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// api_key_composite.go selects a composite key's group and rewrites prefixed
// model names across HTTP representations. A composite credential selects its
// request group from a client model prefix, while upstream requests use the
// remaining model ID. JSON, multipart, and Gemini URL parameters carry that
// identity differently, so extraction and rewriting follow the endpoint's
// representation. Model-less reads retain the composite credential and mark
// group assignment as unnecessary; connection endpoints that can change models
// are rejected before group selection.
//
// The original prefixed name remains in request context and response output.
// Additional tool models can select the same binding, while a known prefix for
// another group rejects the request. Body reconstruction updates its length and,
// for multipart output, its boundary so downstream parsers read the rewritten data.

// CompositeKeyNoGroupContextKey marks model-less composite requests so group
// assignment middleware accepts their unselected credential while other admission checks continue.
const CompositeKeyNoGroupContextKey = "composite_key_no_group"

// CompositeModelResponseWriter restores the client composite model in protocol response metadata.
type CompositeModelResponseWriter struct {
	gin.ResponseWriter // downstream protocol output
	// clientModel and actualModel belong to the same selected request. Write
	// restores actualModel-shaped metadata to clientModel, which includes the client's group prefix.
	clientModel string // prefixed client model
	actualModel string // unprefixed request model
}

// ResolveCompositeAPIKeyRequest selects the group from the model prefix and strips that prefix for forwarding.
// Ordinary or nil keys pass through. Composite model-less reads retain the key
// and mark the request for group-assignment middleware, while unsupported
// connection endpoints return a local composite-key error.
//
// Model-bearing requests are rewritten before the service creates and authorizes
// a group-specific key view. Successful selection installs response restoration
// and client-model telemetry together, preserving the association between the
// chosen group, its internal model, and the client's prefixed name. Callers stop
// admission on error because parameter or body rewriting may already have occurred.
// @project-doc docs/domains/composite_api_keys.md#group_selection
func ResolveCompositeAPIKeyRequest(c *gin.Context, apiKeyService *apikey.APIKeyService, apiKey *apikey.APIKey) (*apikey.APIKey, error) {
	if apiKey == nil || !apiKey.IsComposite {
		return apiKey, nil
	}
	if IsCompositeKeyUnsupportedEndpoint(c.Request.Method, c.Request.URL.Path, c.FullPath()) {
		return nil, apikey.ErrCompositeKeyUnsupported
	}
	if IsCompositeKeyNoModelEndpoint(c.Request.Method, c.Request.URL.Path) {
		c.Set(CompositeKeyNoGroupContextKey, true)
		return apiKey, nil
	}

	var originalModel string
	var actualModel string
	var binding *apikey.APIKeyCompositeGroup
	var err error
	if IsGeminiNativeModelEndpoint(c.Request.URL.Path) {
		// Gemini actions encode the group prefix in route parameters, so replace
		// their model portion while retaining the action used by native dispatch.
		originalModel, err = CompositeGeminiModelFromParams(c)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			RewriteCompositeGeminiParams(c, actualModel)
		}
	} else {
		// Resolve tool models against the chosen binding before replacing the main
		// model; one HTTP request cannot carry separately authorized composite groups.
		originalModel, err = CompositeModelFromRequest(c.Request)
		if err == nil {
			binding, actualModel, err = apiKey.ResolveCompositeModel(originalModel)
		}
		if err == nil {
			err = RewriteCompositeAdditionalModels(c.Request, apiKey, binding)
		}
		if err == nil {
			err = RewriteCompositeRequestModel(c.Request, actualModel)
		}
	}
	if err != nil {
		return nil, err
	}

	selected, err := apiKeyService.SelectCompositeGroupForRequest(c.Request.Context(), apiKey, binding)
	if err != nil {
		return nil, err
	}
	SetCompositeModelContext(c, originalModel, actualModel)
	return selected, nil
}

// SetCompositeModelContext records client and internal model names for logging
// and response restoration. A nil context is ignored; otherwise the current
// writer is wrapped and any request receives the prefixed client model in telemetry.
//
// The two names describe one request after prefix selection. Keeping them beside
// the writer lets subsequent output restore model metadata even after upstream
// preparation changes the payload, while GetCompositeModelFromContext supplies
// the same names to later key redirects and observation code. Repeated calls add
// another writer wrapper, so callers install it when establishing the selected model.
func SetCompositeModelContext(c *gin.Context, clientModel, actualModel string) {
	if c == nil {
		return
	}
	c.Set("composite_client_model", clientModel)
	c.Set("composite_actual_model", actualModel)
	c.Writer = &CompositeModelResponseWriter{
		ResponseWriter: c.Writer,
		clientModel:    clientModel,
		actualModel:    actualModel,
	}
	if c.Request != nil {
		ctx := context.WithValue(c.Request.Context(), telemetry.ClientModel, clientModel)
		c.Request = c.Request.WithContext(ctx)
	}
}

// Write restores composite model metadata before forwarding response bytes.
// It removes Content-Length because rewriting can change output size and returns
// the input byte count together with the downstream error, even when writing fails.
func (w *CompositeModelResponseWriter) Write(data []byte) (int, error) {
	// The client's prefixed model can be longer than the internal model, so a
	// length computed before metadata restoration cannot describe the emitted response.
	w.Header().Del("Content-Length")
	rewritten := ReplaceCompositeResponseModel(data, w.actualModel, w.clientModel)
	_, err := w.ResponseWriter.Write(rewritten)
	return len(data), err
}

// WriteString sends string output through the same composite restoration as Write.
// Its byte count describes the supplied string, and any downstream write error
// is returned to the response producer.
func (w *CompositeModelResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

// ReplaceCompositeResponseModel restores known model-metadata byte patterns in data.
// It delegates to modeltrace, which recognizes specific JSON field spellings and
// spacing; callers supply a chunk containing each complete pattern they expect restored.
func ReplaceCompositeResponseModel(data []byte, actualModel, clientModel string) []byte {
	return modeltrace.ReplaceModelMetadata(data, actualModel, clientModel)
}

// GetCompositeModelFromContext returns the composite key's client and internal model names.
// The success flag requires both names to be nonempty. A nil context or absent
// names yields false, allowing later redirect and observation code to skip restoration.
func GetCompositeModelFromContext(c *gin.Context) (clientModel, actualModel string, ok bool) {
	if c == nil {
		return "", "", false
	}
	clientModel = c.GetString("composite_client_model")
	actualModel = c.GetString("composite_actual_model")
	return clientModel, actualModel, clientModel != "" && actualModel != ""
}

// IsCompositeKeyNoModelEndpoint reports whether an endpoint uses key identity or aggregates all group mappings.
func IsCompositeKeyNoModelEndpoint(method, path string) bool {
	if IsCompositeKeyModelListEndpoint(method, path) || IsAPIKeyUsageRequest(method, path) {
		return true
	}
	return IsBatchImageBillingBypassRequest(method, path) || IsGrokVideoTaskRead(method, path)
}

// IsCompositeKeyModelListEndpoint identifies model lists that aggregate composite key mappings.
// It accepts GET requests and removes one trailing slash before comparing the
// supported paths; a model list is independent of a single prefixed request model.
func IsCompositeKeyModelListEndpoint(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch strings.TrimSuffix(path, "/") {
	case "/v1/models", "/models", "/v1beta/models", "/antigravity/models", "/antigravity/v1/models", "/antigravity/v1beta/models", "/v1/images/batches/models":
		return true
	default:
		return false
	}
}

// IsCompositeKeyBillingBypassEndpoint identifies reads of existing data that bypass consumption checks.
// Usage, batch management, and video task reads can operate after consumption
// eligibility ends. Model lists still require key quota, balance, and subscription checks.
func IsCompositeKeyBillingBypassEndpoint(method, path string) bool {
	if IsAPIKeyUsageRequest(method, path) {
		return true
	}
	return IsBatchImageBillingBypassRequest(method, path) || IsGrokVideoTaskRead(method, path)
}

// IsGrokVideoTaskRead identifies video queries authorized by task ownership rather than a request model.
// It recognizes GET paths beneath the versioned or unversioned video prefix,
// including content reads; handlers separately validate the referenced task and its owner.
func IsGrokVideoTaskRead(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	cleanPath := strings.TrimSuffix(path, "/")
	return strings.HasPrefix(cleanPath, "/v1/videos/") || strings.HasPrefix(cleanPath, "/videos/")
}

// IsCompositeKeyUnsupportedEndpoint identifies realtime endpoints that can use multiple models per connection.
// Callers supply the matched route template as well as the request path so a
// Codex sideband GET can be distinguished from ordinary requests sharing its prefix.
func IsCompositeKeyUnsupportedEndpoint(method, path, routePath string) bool {
	cleanPath := strings.TrimSuffix(path, "/")
	if cleanPath == "/v1/live" || cleanPath == "/backend-api/codex/realtime/calls" {
		return true
	}
	// Codex Live sideband shares its prefix with ordinary HTTP endpoints. The
	// matched route template identifies that connection before any model-based group selection.
	if method == http.MethodGet && routePath == "/backend-api/codex/:call_id" {
		return true
	}
	if method == http.MethodGet && (strings.HasPrefix(cleanPath, "/v1/live/") || cleanPath == "/v1/responses" || cleanPath == "/responses" || cleanPath == "/backend-api/codex/responses") {
		return true
	}
	return false
}

// IsGeminiNativeModelEndpoint reports whether the path contains Gemini's native model route.
func IsGeminiNativeModelEndpoint(path string) bool {
	return strings.Contains(path, "/v1beta/models/")
}

// CompositeGeminiModelFromParams extracts the prefixed model from Gemini URL parameters.
// GET uses model; prefixed POST routes join prefix and model, while wildcard
// routes split modelAction at its last colon. Missing model information returns
// the composite-prefix-required error before a binding can be selected.
//
// The result still carries the group prefix for APIKey.ResolveCompositeModel to
// validate and split. Nested model-path segments remain in that string, while
// the wildcard action suffix is excluded from group resolution. Callers update
// Gin parameters separately after selecting the internal model.
func CompositeGeminiModelFromParams(c *gin.Context) (string, error) {
	// Route templates supply model, prefix/model, or modelAction parameters.
	// Reduce them to one client model for binding resolution; the action suffix belongs to dispatch.
	if c.Request.Method == http.MethodGet {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", apikey.ErrCompositeKeyPrefixRequired
		}
		return model, nil
	}
	if prefix := strings.TrimSpace(c.Param("prefix")); prefix != "" {
		model := strings.TrimPrefix(strings.TrimSpace(c.Param("model")), "/")
		if model == "" {
			return "", apikey.ErrCompositeKeyPrefixRequired
		}
		return prefix + "/" + model, nil
	}
	modelAction := strings.TrimPrefix(strings.TrimSpace(c.Param("modelAction")), "/")
	separator := strings.LastIndex(modelAction, ":")
	if separator <= 0 {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	return modelAction[:separator], nil
}

// RewriteCompositeGeminiParams replaces Gin model values while retaining action suffixes.
// modelAction keeps the suffix beginning at its last colon, including the
// action name. Callers pass the selected internal model or the target of a
// subsequent key alias, using the same parameter update for either stage.
//
// Replacement changes existing model and modelAction parameters, leaving other
// parameters and the request URL as supplied by the client. Rebuilding the
// wildcard action value keeps its leading slash and final action suffix so
// downstream Gemini dispatch can still identify the requested operation.
func RewriteCompositeGeminiParams(c *gin.Context, actualModel string) {
	for i := range c.Params {
		switch c.Params[i].Key {
		case "model":
			c.Params[i].Value = actualModel
		case "modelAction":
			// Model IDs may contain slashes, so retain the final action suffix while
			// rebuilding the wildcard value expected by Gemini's downstream route parser.
			value := strings.TrimPrefix(c.Params[i].Value, "/")
			if separator := strings.LastIndex(value, ":"); separator > 0 {
				c.Params[i].Value = "/" + actualModel + value[separator:]
			}
		}
	}
}

// CompositeModelFromRequest reads the top-level model from JSON or multipart input.
// Successful body reading restores bytes for downstream handlers. SystemOne
// multipart reads the model inside its first request JSON part; other multipart
// endpoints use a model field. Missing or empty model values return the
// composite-prefix-required error, while malformed, duplicate-key, or
// case-ambiguous bodies return a bad-request application error.
func CompositeModelFromRequest(request *http.Request) (string, error) {
	if IsSystemOneEndpoint(request.URL.Path) {
		body, err := ReadAndRestoreRequestBody(request)
		if err != nil {
			return "", err
		}
		model, err := systemone.RequestModel(body, request.Header.Get("Content-Type"))
		if err != nil {
			return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", err.Error())
		}
		if model == "" {
			return "", apikey.ErrCompositeKeyPrefixRequired
		}
		return model, nil
	}
	// Use the parsed media type for multipart dispatch; other results take JSON
	// handling, whose model check supplies the composite admission error when needed.
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return MultipartModel(request)
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	if err := wirejson.ValidateRequestObject(bytes.TrimPrefix(body, utf8ByteOrderMark)); err != nil {
		return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", RequestBodyParseErrorMessage(err))
	}
	model := strings.TrimSpace(gjson.GetBytes(bytes.TrimPrefix(body, utf8ByteOrderMark), "model").String())
	if model == "" {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	return model, nil
}

// RewriteCompositeRequestModel replaces the request's top-level internal model.
// SystemOne rewrites its JSON envelope and preserves multipart image bytes;
// other multipart endpoints use RewriteMultipartModel. Rebuilt content type,
// body, and length are installed together after success. Errors leave the
// restored original body available and stop the caller's model redirection.
func RewriteCompositeRequestModel(request *http.Request, actualModel string) error {
	if IsSystemOneEndpoint(request.URL.Path) {
		body, err := ReadAndRestoreRequestBody(request)
		if err != nil {
			return err
		}
		body, contentType, err := systemone.ReplaceRequestModel(body, request.Header.Get("Content-Type"), actualModel)
		if err != nil {
			return err
		}
		// SystemOne stores its model inside the request JSON part, unlike image
		// generation forms. Install the rebuilt bytes with their matching multipart boundary.
		request.Header.Set("Content-Type", contentType)
		SetRequestBody(request, body)
		return nil
	}
	// Use the parsed media type for multipart dispatch; otherwise sjson handles
	// the body and supplies the replacement error returned to composite admission.
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		return RewriteMultipartModel(request, actualModel)
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	rewritten, err := sjson.SetBytes(body, "model", actualModel)
	if err != nil {
		// Present the patch failure as a composite-prefix error so admission stops
		// before installing an unsuccessful primary-model rewrite or selecting its provider.
		return apikey.ErrCompositeKeyPrefixRequired
	}
	SetRequestBody(request, rewritten)
	return nil
}

// RewriteCompositeAdditionalModels resolves additional models declared by Responses tools.
// It strips same-group prefixes and rejects cross-group models because a request has one selected route.
// Multipart input or missing selection data is ignored. Tool model strings that
// fail composite resolution remain unchanged, including vendor IDs containing slashes.
//
// Recognized prefixes are checked against the main model's selected group before
// any rewritten body is installed. A different group or failed JSON update
// returns the unsupported-composite error; successful replacements are published
// together so later handlers receive a single group-consistent request body.
func RewriteCompositeAdditionalModels(request *http.Request, apiKey *apikey.APIKey, selected *apikey.APIKeyCompositeGroup) error {
	// Multipart tool payloads are left for media handlers. Other parsed media
	// types use the JSON tools array even when content-type parameters are malformed.
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") || apiKey == nil || selected == nil {
		return nil
	}
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	if err := wirejson.ValidateRequestObject(bytes.TrimPrefix(body, utf8ByteOrderMark)); err != nil {
		return infraerrors.BadRequest("INVALID_REQUEST_BODY", RequestBodyParseErrorMessage(err))
	}
	rewritten := body
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		model := strings.TrimSpace(tool.Get("model").String())
		if model == "" {
			continue
		}
		binding, actualModel, resolveErr := apiKey.ResolveCompositeModel(model)
		if resolveErr != nil {
			// Vendor model IDs may contain slashes without naming a composite group.
			// Keep unresolved tool model strings so ordinary tool-model routing can interpret them.
			continue
		}
		if binding.GroupID != selected.GroupID {
			// Reject a different group before publishing accumulated tool patches.
			// The selected main route supplies this request's authorization and cannot cover that other group.
			return apikey.ErrCompositeKeyUnsupported
		}
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), actualModel)
		if err != nil {
			// Present an unusable tool-model rewrite as an unsupported composite request,
			// preventing partial replacements from being installed for a selected group.
			return apikey.ErrCompositeKeyUnsupported
		}
	}
	SetRequestBody(request, rewritten)
	return nil
}

// ReadAndRestoreRequestBody reads a request body and leaves bytes for later handlers.
// Missing input returns the composite-prefix-required error. After a successful
// raw read, the request is reset before content decoding is attempted.
//
// Encoded bodies are decoded using a cloned request so a decoding failure leaves
// the raw bytes and their encoding available to the caller. Successful decoding
// records decoded payload telemetry, removes Content-Encoding, and installs
// decoded bytes with matching length metadata. An error during the initial raw
// read is returned immediately, so callers cannot assume the partially consumed
// body was restored on that path.
//
// Decoding delegates to httpx, which can truncate decoded data at 64 MiB without
// a size error; callers still validate completeness. The installed reader shares
// the returned bytes. Callers leave those bytes unchanged during downstream reads
// and retain cleanup responsibility for the replaced original body.
func ReadAndRestoreRequestBody(request *http.Request) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, apikey.ErrCompositeKeyPrefixRequired
	}
	startedAt := time.Now()
	rawBody, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	SetRequestBody(request, rawBody)

	encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding")))
	if encoding == "" || encoding == "identity" {
		timing.ObserveBodyDecode(request.Context(), int64(len(rawBody)), false, time.Since(startedAt))
		return rawBody, nil
	}

	// Reset the original before decoding a clone. A decode error then leaves
	// downstream error handling the complete compressed bytes and their original encoding.
	decodeRequest := request.Clone(request.Context())
	decodeRequest.Body = io.NopCloser(bytes.NewReader(rawBody))
	decodeRequest.ContentLength = int64(len(rawBody))
	decodedBody, compressed, err := httpx.ReadRequestBodyWithMaxPrealloc(decodeRequest, gatewayRequestBodyMaxInitCap)
	if err != nil {
		return nil, err
	}
	// Record decompression before clearing Content-Encoding so a later handler
	// reread of restored bytes retains request_compressed=1 and decompression CPU time.
	timing.ObserveBodyDecode(request.Context(), int64(len(decodedBody)), compressed, time.Since(startedAt))
	request.Header.Del("Content-Encoding")
	SetRequestBody(request, decodedBody)
	return decodedBody, nil
}

// SetRequestBody installs readable bytes and their length on an existing request.
// The reader references body, so callers keep those bytes unchanged while
// downstream handlers read them. Content-Length is replaced to match this body.
// Callers provide an initialized Header map. The previous Body is replaced
// without being closed and remains under its existing owner's cleanup responsibility.
func SetRequestBody(request *http.Request, body []byte) {
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

// MultipartModel reads the unique scalar multipart field named model and trims its value.
// It restores the request body before parsing. Missing fields, empty values, or
// an invalid multipart content type return the composite-prefix-required error;
// duplicate or case-variant model fields and malformed multipart bodies return
// bad-request application errors.
func MultipartModel(request *http.Request) (string, error) {
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return "", err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || strings.TrimSpace(params["boundary"]) == "" {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	foundModel := false
	selectedModel := ""
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", "Failed to parse multipart request body")
		}
		name := strings.TrimSpace(part.FormName())
		fileName := strings.TrimSpace(part.FileName())
		if fileName == "" && strings.EqualFold(name, "model") {
			if name != "model" {
				_ = part.Close()
				return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", "ambiguous multipart model field")
			}
			if foundModel {
				_ = part.Close()
				return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", "duplicate multipart model field")
			}
			value, readErr := io.ReadAll(part)
			_ = part.Close()
			if readErr != nil {
				return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", "Failed to parse multipart request body")
			}
			foundModel = true
			selectedModel = strings.TrimSpace(string(value))
			continue
		}
		_, copyErr := io.Copy(io.Discard, part)
		_ = part.Close()
		if copyErr != nil {
			return "", infraerrors.BadRequest("INVALID_REQUEST_BODY", "Failed to parse multipart request body")
		}
	}
	if !foundModel || selectedModel == "" {
		return "", apikey.ErrCompositeKeyPrefixRequired
	}
	return selectedModel, nil
}

// RewriteMultipartModel rebuilds multipart input with actualModel in every model field.
// It copies headers and other contents exposed by multipart.Reader.NextPart in
// input order. NextPart decodes quoted-printable contents and removes their
// transfer-encoding header, so reconstruction uses that decoded representation.
// Missing model fields or an invalid multipart content type return a composite-prefix error.
//
// The replacement writer chooses a new multipart boundary, so its finalized
// content type and bytes are installed together after all parts have been copied.
// Any parsing, writing, or finalization error returns before that installation;
// the readable input established by ReadAndRestoreRequestBody remains available
// for the caller's error handling.
func RewriteMultipartModel(request *http.Request, actualModel string) error {
	body, err := ReadAndRestoreRequestBody(request)
	if err != nil {
		return err
	}
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return apikey.ErrCompositeKeyPrefixRequired
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	found := false
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		// NextPart can decode a part before this copy. Use its exposed headers with
		// its exposed contents so the rebuilt body does not label decoded bytes as quoted-printable.
		target, createErr := writer.CreatePart(part.Header)
		if createErr != nil {
			return createErr
		}
		if part.FormName() == "model" {
			_, err = io.WriteString(target, actualModel)
			found = true
		} else {
			_, err = io.Copy(target, part)
		}
		if err != nil {
			return err
		}
	}
	if !found {
		return apikey.ErrCompositeKeyPrefixRequired
	}
	// Finalize the closing boundary before publishing its content type and body;
	// downstream multipart parsers require those two representations to describe the same parts.
	if err := writer.Close(); err != nil {
		return err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	SetRequestBody(request, output.Bytes())
	return nil
}

// AbortCompositeKeyError writes a structured composite key error in the endpoint's protocol.
// SystemOne and OpenAI-family endpoints receive model-specific error fields;
// other paths receive the Messages shape. Each branch aborts Gin processing after
// writing the status and reason carried by err.
func AbortCompositeKeyError(c *gin.Context, err error) {
	status := httpx.ErrorCode(err)
	code := infraerrors.Reason(err)
	message := infraerrors.Message(err)
	if IsSystemOneEndpoint(c.Request.URL.Path) {
		WriteSystemOneError(c, status, "invalid_request_error", code, "model", message)
		c.Abort()
		return
	}
	if IsOpenAICompositeEndpoint(c.Request.URL.Path) {
		c.JSON(status, gin.H{"error": gin.H{
			"message": message, "type": "invalid_request_error", "param": "model", "code": code,
		}})
		c.Abort()
		return
	}
	c.JSON(status, gin.H{
		"type":  "error",
		"error": gin.H{"type": "invalid_request_error", "message": message, "code": code},
	})
	c.Abort()
}

// IsOpenAICompositeEndpoint reports whether a path uses OpenAI-shaped composite errors.
// It recognizes endpoint fragments and the Codex prefix, so callers use the
// request path when deciding which error envelope to write.
func IsOpenAICompositeEndpoint(path string) bool {
	return strings.Contains(path, "/chat/completions") || strings.Contains(path, "/responses") ||
		strings.Contains(path, "/embeddings") || strings.Contains(path, "/images/") ||
		strings.Contains(path, "/videos/") || strings.Contains(path, "/alpha/search") ||
		strings.Contains(path, "/live") || strings.Contains(path, "/realtime/") ||
		strings.HasPrefix(path, "/backend-api/codex/")
}

// AbortCompositeKeyGoogleError writes a composite key error in Google format.
// It combines the structured reason and message for Google's status envelope,
// then delegates to the API-key HTTP writer to abort further processing.
func AbortCompositeKeyGoogleError(c *gin.Context, err error) {
	keyhttp.AbortGoogleError(c, httpx.ErrorCode(err), infraerrors.Reason(err)+": "+infraerrors.Message(err))
}
