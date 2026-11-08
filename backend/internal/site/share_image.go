package site

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	_ "golang.org/x/image/webp"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// share_image.go validates uploaded covers and reads their public descriptors.
// A cover is a single administrator-owned image stored with the settings batch.
// Its content digest identifies the public resource, while a separate descriptor
// keeps image bytes out of ordinary public settings and injected HTML responses.
//
// Preparation fully decodes bounded image data before a transaction can publish
// the descriptor. Reads compare the requested digest with the currently stored
// bytes, so replacing or removing a cover cannot serve different content at an
// earlier address. Database backups include the original image together with
// its descriptor, without requiring a separate filesystem backup or upload cleanup.

const (
	// Cover keys belong to the site participant; the descriptor is derived rather
	// than accepted from administrator input, preventing mismatched public URLs.
	SettingKeySiteShareImage     = "site_share_image"
	SettingKeySiteShareImageMeta = "site_share_image_meta"
	// ShareImageMaxBytes bounds decoded uploads before expensive image decoding or
	// persistence; encoded data URLs require approximately one third more space.
	ShareImageMaxBytes     = 2 << 20
	ShareImageMaxDimension = 4096
)

// ShareImageMetadata describes validated bytes without exposing their encoded payload.
type ShareImageMetadata struct {
	Hash   string `json:"hash"`   // SHA-256 hexadecimal digest
	Type   string `json:"type"`   // validated image MIME
	Width  int    `json:"width"`  // pixels
	Height int    `json:"height"` // pixels
}

// ShareImage combines decoded cover bytes with their validated public descriptor.
type ShareImage struct {
	Data     []byte             // decoded image bytes
	Metadata ShareImageMetadata // validated public descriptor
}

// ParseShareImage validates a data URL and returns independently decoded image bytes.
// Empty input removes the cover. PNG, JPEG, and WebP must match the declared
// MIME type, decode fully, and fit both byte and dimension limits before use.
func ParseShareImage(value string) (ShareImage, error) {
	cover, err := decodeShareImage(value)
	if err != nil || len(cover.Data) == 0 {
		return cover, err
	}
	// Header validation cannot detect truncated pixels. Fully decode only at the
	// administrator write boundary; digest-addressed public reads reuse the validated stored bytes.
	if _, _, err := image.Decode(bytes.NewReader(cover.Data)); err != nil {
		return ShareImage{}, apperror.BadRequest("INVALID_SHARE_IMAGE", "Cover image data is damaged.")
	}
	return cover, nil
}

// decodeShareImage bounds encoded input and derives its descriptor from image headers.
// It accepts empty input as removal and validates the MIME declaration, byte size,
// dimensions, and digest. Write callers additionally verify complete pixel decoding.
func decodeShareImage(value string) (ShareImage, error) {
	var result ShareImage
	if value == "" {
		return result, nil
	}
	invalid := apperror.BadRequest("INVALID_SHARE_IMAGE", "Cover must be a valid PNG, JPEG, or WebP image up to 2 MiB and 4096 pixels per side.")
	if len(value) > base64.StdEncoding.EncodedLen(ShareImageMaxBytes)+32 {
		return result, invalid
	}
	header, encoded, ok := strings.Cut(value, ",")
	if !ok {
		return result, invalid
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	if header != "data:"+mime+";base64" || (mime != "image/png" && mime != "image/jpeg" && mime != "image/webp") {
		return result, invalid
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > ShareImageMaxBytes {
		return result, invalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mime || config.Width < 1 || config.Height < 1 || config.Width > ShareImageMaxDimension || config.Height > ShareImageMaxDimension {
		return result, invalid
	}
	hash := sha256.Sum256(data)
	return ShareImage{Data: data, Metadata: ShareImageMetadata{Hash: hex.EncodeToString(hash[:]), Type: mime, Width: config.Width, Height: config.Height}}, nil
}

// ReadShareImageMetadata decodes the descriptor used by public settings responses.
// Missing or malformed metadata produces an empty descriptor, leaving the page
// usable without advertising a cover that cannot be identified safely.
func ReadShareImageMetadata(raw string) ShareImageMetadata {
	var metadata ShareImageMetadata
	// A malformed stored descriptor cannot identify a published cover. Omit it from
	// public responses while retaining all other site settings for browser initialization.
	if json.Unmarshal([]byte(raw), &metadata) != nil {
		return ShareImageMetadata{}
	}
	digest, err := hex.DecodeString(metadata.Hash)
	if err != nil || len(digest) != sha256.Size {
		return ShareImageMetadata{}
	}
	return metadata
}

// GetShareImage reads the current uploaded cover for an exact content digest.
// Missing or replaced images return not found. Storage and invalid stored-image
// errors propagate rather than becoming a successful image response.
func (s *PublicService) GetShareImage(ctx context.Context, hash string) (ShareImage, error) {
	if len(hash) != sha256.Size*2 {
		return ShareImage{}, apperror.NotFound("SHARE_IMAGE_NOT_FOUND", "Cover image not found.")
	}
	value, err := s.source.LoadSiteShareImage(ctx)
	if err != nil {
		return ShareImage{}, err
	}
	cover, err := decodeShareImage(value)
	if err != nil {
		return ShareImage{}, apperror.InternalServer("INVALID_STORED_SHARE_IMAGE", "Stored cover image is invalid.")
	}
	if cover.Metadata.Hash != hash {
		return ShareImage{}, apperror.NotFound("SHARE_IMAGE_NOT_FOUND", "Cover image not found.")
	}
	return cover, nil
}

// shareImagePath returns the digest-addressed resource path for a configured cover.
// Empty descriptors have no public resource. The HTML renderer resolves this
// path against the configured frontend address or the trusted request origin.
func shareImagePath(metadata ShareImageMetadata) string {
	if metadata.Hash == "" {
		return ""
	}
	return "/api/v1/settings/share-image/" + metadata.Hash
}
