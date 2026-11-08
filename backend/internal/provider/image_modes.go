package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// image_modes.go reads and validates per-endpoint Images transport capabilities.
// OpenAI API-key providers can declare different modes for generation and
// editing. Missing values follow the client through the both-mode default;
// validation rejects unsupported values before any provider configuration is
// persisted by creation, editing, import, or batch operations.

const (
	// These credential keys store independent capabilities, allowing bulk patches
	// to change one endpoint without replacing the other endpoint's configured mode.
	ImagesGIFEnabledKey      = "images_gif_enabled"
	ImagesGenerationsModeKey = "images_generations_mode"
	ImagesEditsModeKey       = "images_edits_mode"
)

// ImageStreamMode reads the configured Images capability for the requested
// operation. Missing keys and inapplicable provider types follow the client;
// malformed stored values return an error before a generation can be submitted.
func (r *Record) ImageStreamMode(operation protocol.ProtocolID) (protocol.ImageStreamMode, error) {
	if r == nil || r.Platform != PlatformOpenAI || r.Type != ProviderTypeAPIKey {
		return protocol.ImageModeBoth, nil
	}
	key := ImagesGenerationsModeKey
	if operation == protocol.ProtocolImagesEdits {
		key = ImagesEditsModeKey
	}
	return readImageStreamMode(r.Credentials, key)
}

// validateImageStreamModes checks supplied capabilities before provider persistence.
// Configuration is accepted only for OpenAI API-key records, including when the
// corresponding Images protocol is currently disabled and retains its settings.
func validateImageStreamModes(r *Record) error {
	if value, exists := r.Extra[ImagesGIFEnabledKey]; exists {
		if _, valid := value.(bool); !valid || r.Platform != PlatformOpenAI || (r.Type != ProviderTypeOAuth && r.Type != ProviderTypeAPIKey) {
			return apperror.BadRequest("IMAGE_GIF_SETTING_INVALID", "images_gif_enabled must be a boolean on an OpenAI OAuth or API key provider")
		}
	}
	for _, key := range []string{ImagesGenerationsModeKey, ImagesEditsModeKey} {
		if _, exists := r.Credentials[key]; !exists {
			continue
		}
		if r.Platform != PlatformOpenAI || r.Type != ProviderTypeAPIKey {
			return apperror.BadRequest("IMAGE_STREAM_MODE_INVALID", "Image stream modes require an OpenAI API key provider")
		}
		if _, err := readImageStreamMode(r.Credentials, key); err != nil {
			return err
		}
	}
	return nil
}

// readImageStreamMode parses one optional credential value using the shared enum.
// An absent key defaults to both; present null, empty, and non-string values are
// invalid so configuration mistakes cannot silently select another transport.
func readImageStreamMode(credentials map[string]any, key string) (protocol.ImageStreamMode, error) {
	raw, exists := credentials[key]
	if !exists {
		return protocol.ImageModeBoth, nil
	}
	value, ok := raw.(string)
	mode := protocol.ImageStreamMode(value)
	if !ok || !mode.Valid() {
		return "", apperror.BadRequest("IMAGE_STREAM_MODE_INVALID", key+" must be non_streaming, streaming, or both")
	}
	return mode, nil
}

// SupportsImageGIF reports configured GIF eligibility for one image operation.
// Missing settings enable supported OpenAI credentials. Native Images endpoints
// configured as streaming-only remain ineligible because GIFs require buffered output.
func (r *Record) SupportsImageGIF(operation protocol.ProtocolID) bool {
	if r == nil || r.Platform != PlatformOpenAI || (r.Type != ProviderTypeOAuth && r.Type != ProviderTypeAPIKey) {
		return false
	}
	if value, present := r.Extra[ImagesGIFEnabledKey]; present {
		enabled, valid := value.(bool)
		if !valid || !enabled {
			return false
		}
	}
	if operation == protocol.ProtocolImagesGenerations || operation == protocol.ProtocolImagesEdits {
		mode, err := r.ImageStreamMode(operation)
		return err == nil && mode != protocol.ImageModeStreaming
	}
	return true
}
