package site

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// share_image_test.go exercises cover validation and public settings separation.
// Small generated PNGs keep tests independent of external image services, while
// invalid data and localized text fixtures verify the published descriptor and
// copy without permitting image payloads to enter browser initialization JSON.

// coverDataURL encodes a solid PNG fixture with the requested pixel dimensions.
func coverDataURL(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, img))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

// TestParseShareImage validates headers and the complete image stream before publication.
// Invalid MIME claims, oversized images, and truncated data must never produce descriptors.
func TestParseShareImage(t *testing.T) {
	valid := coverDataURL(t, 2, 3)
	cover, err := ParseShareImage(valid)
	require.NoError(t, err)
	require.Len(t, cover.Metadata.Hash, 64)
	require.Equal(t, 2, cover.Metadata.Width)
	require.Equal(t, 3, cover.Metadata.Height)
	for _, raw := range []string{
		strings.Replace(valid, "image/png", "image/jpeg", 1),
		"data:image/svg+xml;base64,PHN2Zy8+", "https://example.com/image.png",
		"data:image/png;base64,!!!", coverDataURL(t, 4097, 1),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(cover.Data[:len(cover.Data)-12]),
		"data:image/png;base64," + strings.Repeat("A", base64.StdEncoding.EncodedLen(ShareImageMaxBytes)+4),
	} {
		_, err := ParseShareImage(raw)
		require.Error(t, err)
	}
	empty, err := ParseShareImage("")
	require.NoError(t, err)
	require.Empty(t, empty.Metadata.Hash)
}

// TestShareImageSettingsParticipant exercises preparation without committing a write.
// Cover bytes and metadata travel together, omitted input preserves both, and explicit clearing removes the public descriptor.
func TestShareImageSettingsParticipant(t *testing.T) {
	p := SettingsParticipant()
	raw := coverDataURL(t, 2, 3)
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	prepared, err := p.Prepare(context.Background(), settings.Fields{SettingKeySiteShareImage: encoded}, nil)
	require.NoError(t, err)
	require.Equal(t, raw, prepared.Values[SettingKeySiteShareImage])
	require.Equal(t, 2, ReadShareImageMetadata(prepared.Values[SettingKeySiteShareImageMeta]).Width)
	unchanged, err := p.Prepare(context.Background(), settings.Fields{"site_logo": json.RawMessage(`"logo"`)}, prepared.Values)
	require.NoError(t, err)
	require.NotContains(t, unchanged.Values, SettingKeySiteShareImage)
	require.NotContains(t, unchanged.Values, SettingKeySiteShareImageMeta)
	cleared, err := p.Prepare(context.Background(), settings.Fields{SettingKeySiteShareImage: json.RawMessage(`""`)}, prepared.Values)
	require.NoError(t, err)
	require.Empty(t, cleared.Values[SettingKeySiteShareImage])
	require.Empty(t, ReadShareImageMetadata(cleared.Values[SettingKeySiteShareImageMeta]).Hash)
}

// TestShareMetadataLocalization resolves reviewed copy for public JSON and HTML settings.
// The public allowlist excludes uploaded bytes, while translated titles and digest paths remain available to both consumers.
func TestShareMetadataLocalization(t *testing.T) {
	en := "en"
	content := locale.Original("Share title")
	content.SourceLocale = &en
	content.SourceRevision = 1
	content.Translations = map[string]locale.Translation[string]{"zh-Hans": {Value: "分享标题", SourceRevision: 1}}
	localized, err := json.Marshal(map[string]locale.Content[string]{"site_share_title": content})
	require.NoError(t, err)
	cover, err := ParseShareImage(coverDataURL(t, 2, 3))
	require.NoError(t, err)
	metadata, err := json.Marshal(cover.Metadata)
	require.NoError(t, err)
	values := homeTextSource{SettingKeySiteTexts: string(localized), SettingKeySiteShareImageMeta: string(metadata)}
	service := NewPublicService(values, timezone.NewCalendar(time.UTC), "UTC")
	ctx := locale.WithLanguage(context.Background(), "zh-Hans")
	public, err := service.GetPublicSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "分享标题", public.SiteShareTitle)
	require.Contains(t, public.SiteShareImageURL, cover.Metadata.Hash)
	injected, err := service.GetPublicSettingsForInjection(ctx)
	require.NoError(t, err)
	body, err := json.Marshal(injected)
	require.NoError(t, err)
	require.Contains(t, string(body), "分享标题")
	require.NotContains(t, string(body), "base64")
	require.NotContains(t, PublicInputKeys(), SettingKeySiteShareImage)
	require.NotContains(t, PublicValueKeys(), SettingKeySiteShareImage)
}
