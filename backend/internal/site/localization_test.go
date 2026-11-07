package site

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// homeTextSource supplies the same site settings to the public API and HTML injection.
type homeTextSource map[string]string

// TestLegalTranslationKeepsConsent prepares a reviewed translation and then edits source Markdown.
// It verifies that consent revisions track source changes, and that the
// translation resolves only until its reviewed source revision becomes stale.
func TestLegalTranslationKeepsConsent(t *testing.T) {
	zh := "zh-Hans"
	docs := []LoginAgreementDocument{{ID: "terms", Title: "条款", ContentMD: "正文"}}
	revision := BuildLoginAgreementRevision("2026-03-31", docs)
	raw, err := json.Marshal(docs)
	require.NoError(t, err)
	docs[0].Localization = &locale.Update[LoginAgreementCopy]{Content: locale.Original(LoginAgreementCopy{Title: "条款", ContentMD: "正文"}), ReviewedLocales: []string{"en"}}
	docs[0].Localization.SourceLocale = &zh
	docs[0].Localization.Translations["en"] = locale.Translation[LoginAgreementCopy]{Value: LoginAgreementCopy{Title: "Terms", ContentMD: "Content"}}
	saved, err := PrepareLegalDocuments(string(raw), docs)
	require.NoError(t, err)
	require.Equal(t, revision, BuildLoginAgreementRevision("2026-03-31", saved))
	copy, info := saved[0].Localization.Resolve("en")
	require.Equal(t, "Terms", copy.Title)
	require.False(t, info.Fallback)
	raw, err = json.Marshal(saved)
	require.NoError(t, err)
	saved[0].Localization.Source.ContentMD = "修改正文"
	changed, err := PrepareLegalDocuments(string(raw), saved)
	require.NoError(t, err)
	require.NotEqual(t, revision, BuildLoginAgreementRevision("2026-03-31", changed))
	copy, info = changed[0].Localization.Resolve("en")
	require.Equal(t, "修改正文", copy.ContentMD)
	require.True(t, info.Fallback)
}

// TestOptionalSiteTextDistinguishesEmpty compares unchanged editor defaults with an explicit empty copy.
// It verifies that a saved empty site title remains a configured override,
// while merely parsing missing content does not persist that field.
func TestOptionalSiteTextDistinguishesEmpty(t *testing.T) {
	values := map[string]string{}
	editor := ParseLocalizedTexts(values)
	saved, err := PrepareLocalizedTexts(values, editor)
	require.NoError(t, err)
	require.Empty(t, saved)
	en := "en"
	title := editor["site_title"]
	title.SourceLocale = &en
	saved, err = PrepareLocalizedTexts(values, LocalizedTexts{"site_title": title})
	require.NoError(t, err)
	require.Contains(t, saved, "site_title")
	require.Empty(t, saved["site_title"].Source)
	raw, err := json.Marshal(saved)
	require.NoError(t, err)
	values[SettingKeySiteTexts] = string(raw)
	require.Contains(t, ResolveSiteTexts(values, "zh-Hans"), "site_title")
}

// TestSiteTextOverrides supplies missing, empty, whitespace, translated, and edited site copy.
// It verifies that public API and HTML injection report the same configured
// override keys regardless of the displayed text value.
func TestSiteTextOverrides(t *testing.T) {
	en := "en"
	for _, tc := range []struct {
		name         string                                // named content scenario
		source       string                                // saved source text
		language     *string                               // saved source language
		revision     int64                                 // saved content revision
		translations map[string]locale.Translation[string] // saved translated copies
		configured   bool                                  // group contains field
	}{
		{name: "missing"},
		{name: "saved empty", revision: 1, configured: true},
		{name: "saved whitespace", source: " \t\n", revision: 1, configured: true},
		{name: "custom source", source: "自定义标题", revision: 1, configured: true},
		{name: "translated source", revision: 1, translations: map[string]locale.Translation[string]{"en": {Value: "Custom title", SourceRevision: 1}}, configured: true},
		{name: "intentional empty", language: &en, revision: 1, configured: true},
		{name: "edited content", revision: 2, configured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := locale.Content[string]{SourceLocale: tc.language, Source: tc.source, Revision: tc.revision, SourceRevision: 1, Translations: tc.translations}
			for _, key := range []string{"site_title", "site_subtitle"} {
				contents := map[string]locale.Content[string]{}
				if tc.configured {
					contents[key] = doc
				}
				raw, err := json.Marshal(contents)
				require.NoError(t, err)
				values := map[string]string{SettingKeySiteTexts: string(raw)}
				_, configured := ConfiguredSiteTexts(values)[key]
				require.Equal(t, tc.configured, configured, key)
				editor := ParseLocalizedTexts(values)
				if !tc.configured {
					require.Zero(t, editor[key].Revision)
				}
				service := NewPublicService(homeTextSource(values), timezone.NewCalendar(time.UTC), "UTC")
				for _, language := range []string{"en", "zh-Hans"} {
					ctx := locale.WithLanguage(context.Background(), language)
					public, err := service.GetPublicSettings(ctx)
					require.NoError(t, err)
					if tc.configured {
						require.Contains(t, public.SiteTextOverrides, key)
					} else {
						require.NotContains(t, public.SiteTextOverrides, key)
					}
					injection, err := service.GetPublicSettingsForInjection(ctx)
					require.NoError(t, err)
					encoded, err := json.Marshal(injection)
					require.NoError(t, err)
					var injected struct {
						Overrides []string `json:"site_text_overrides"` // reported override keys
					}
					require.NoError(t, json.Unmarshal(encoded, &injected))
					require.Equal(t, public.SiteTextOverrides, injected.Overrides)
				}
			}
		})
	}
}

// LoadSitePublicInputs supplies the fixture's raw values with empty auth and usage data.
// The returned Values map is the fixture map itself, letting both public
// response paths begin from exactly the same saved content.
func (s homeTextSource) LoadSitePublicInputs(context.Context) (PublicInputs, error) {
	return PublicInputs{Values: s}, nil
}

// PublicVersion returns a fixed build label for site-copy response assertions.
func (homeTextSource) PublicVersion() string { return "home-copy-test" }

// LoadSiteShareImage leaves this text-only fixture without an uploaded cover.
func (homeTextSource) LoadSiteShareImage(context.Context) (string, error) { return "", nil }
