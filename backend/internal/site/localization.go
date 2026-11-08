package site

import (
	"encoding/json"
	"maps"
	"net/url"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
)

// localization.go prepares and resolves localized site text and revisioned
// legal documents.
// Configured site text distinguishes a saved empty string from a field with
// no localized content. Editor reads fill missing fields from legacy source
// keys, while prepared writes retain omitted fields and validate changes
// against each field's current content revision. Public reads resolve saved
// blocks and leave source settings available in an independently owned map.
//
// Legal documents match previous localization by document ID and treat title
// and Markdown as one copy. An omitted localization block retains the previous
// document's tracked source and translations. Revision preparation validates
// supplied copies before replacing that state, while consent revision calculation
// elsewhere excludes translation metadata and responds to source changes.

// Localization keys separate per-field site content from the default language
// used by public readers when a request does not select another language.
const (
	SettingKeySiteTexts     = "site_texts"
	SettingKeyDefaultLocale = "default_locale"
)

// siteTextKeys lists the source fields initialized for the editor when saved
// localized content does not yet define an editable block for those keys.
var siteTextKeys = []string{
	"site_name",
	"site_title",
	"site_subtitle",
	"site_share_title",
	"site_share_description",
	"contact_info",
	"doc_url",
	"home_content",
	"purchase_subscription_url",
	"footer_text",
}

// LocalizedTexts stores site text by field so each field can resolve translations independently.
type LocalizedTexts = locale.TextUpdates

// ParseLocalizedTexts returns editable content from saved groups and legacy source values.
// Each missing siteTextKeys field receives an original content block, with
// TokenRouter used for an empty legacy site_name. Saved blocks remain present,
// including explicitly empty content and any other keys already stored in the
// group.
//
// Filling the editor map does not persist those generated blocks. Their initial
// revision is used by PrepareLocalizedTexts to distinguish an unchanged absent
// field from an administrator's submitted content or review update.
func ParseLocalizedTexts(values map[string]string) LocalizedTexts {
	result := ConfiguredSiteTexts(values)
	for _, key := range siteTextKeys {
		if _, exists := result[key]; !exists {
			value := values[key]
			if key == "site_name" && value == "" {
				value = "TokenRouter"
			}
			result[key] = locale.Update[string]{Content: locale.Original(value)}
		}
	}
	return result
}

// PrepareLocalizedTexts returns configured site text after preparing submitted content changes.
// Omitted fields remain as saved, and fields absent from the prepared prior
// map are rejected. Equal content with no reviewed locales causes no write,
// including editor defaults generated for fields that were never configured.
//
// Changed copies pass through locale.Prepare using their current revisions.
// Link fields accept relative, HTTP, and HTTPS destinations; homepage HTML uses
// a separate larger text limit. Saved empty optional text remains distinguishable
// from absence, while a changed site_name must contain a nonblank name.
func PrepareLocalizedTexts(values map[string]string, input LocalizedTexts) (LocalizedTexts, error) {
	previous := ParseLocalizedTexts(values)
	result := ConfiguredSiteTexts(values)
	for key, update := range input {
		current, known := previous[key]
		if !known {
			return nil, apperror.BadRequest("UNKNOWN_LOCALIZED_FIELD", "Unknown localized field.")
		}
		if reflect.DeepEqual(current.Content, update.Content) && len(update.ReviewedLocales) == 0 {
			continue
		}
		next, err := locale.Prepare(current.Content, update, func(value string) error {
			if key == "doc_url" || key == "purchase_subscription_url" || key == "home_content" && !strings.HasPrefix(strings.TrimSpace(value), "<") {
				parsed, err := url.Parse(strings.TrimSpace(value))
				if err != nil || parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
					return apperror.BadRequest("INVALID_LINK_URL", "Link URL is invalid.")
				}
			}
			// Ordinary display copy is bounded in Unicode characters.
			limit := 2000
			if key == "home_content" {
				// Homepage content permits larger HTML or link source text.
				limit = 1 << 20
			}
			if utf8.RuneCountInString(value) > limit {
				return apperror.BadRequest("LOCALIZED_TEXT_TOO_LONG", "Text exceeds the allowed length.").WithMetadata(map[string]string{"field": key})
			}
			if key == "site_name" && strings.TrimSpace(value) == "" {
				return apperror.BadRequest("SITE_NAME_REQUIRED", "Site name is required.")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		result[key] = locale.Update[string]{Content: next}
	}
	return result, nil
}

// ResolveSiteTexts returns a separate settings map with saved text resolved for code.
// Unconfigured source keys retain their values, while a configured block may
// resolve to an intentionally empty string that overrides the source setting.
func ResolveSiteTexts(values map[string]string, code string) map[string]string {
	result := make(map[string]string, len(values))
	maps.Copy(result, values)
	for key, content := range ConfiguredSiteTexts(values) {
		result[key], _ = content.Resolve(code)
	}
	return result
}

// PrepareLegalDocuments returns source copies and localization prepared against
// the previously stored documents.
// Document IDs match previous content, and an omitted localization block reuses
// that document's saved localization and source copy. Nonempty input receives
// a copied outer slice; empty input returns nil. Unchanged submitted localization
// pointers can remain shared with input, so callers copy retained nested content
// before modifying it independently.
//
// Supplied updates use title and Markdown as one revisioned copy. A changed
// copy requires a nonblank title, a body of at most one mebibyte, and a nonblank
// body when the submitted source body is nonblank. Preparation updates each
// returned document's source fields; it performs no persistence.
func PrepareLegalDocuments(raw string, input []LoginAgreementDocument) ([]LoginAgreementDocument, error) {
	current := map[string]LoginAgreementDocument{}
	for _, doc := range ParseLoginAgreementDocuments(raw) {
		current[doc.ID] = doc
	}
	result := append([]LoginAgreementDocument(nil), input...)
	for i := range result {
		doc := &result[i]
		if doc.Localization == nil {
			if previous, exists := current[doc.ID]; exists && previous.Localization != nil {
				doc.Localization = previous.Localization
				doc.Title, doc.ContentMD = previous.Localization.Source.Title, previous.Localization.Source.ContentMD
			}
			continue
		}
		previous := current[doc.ID]
		original := locale.Original(LoginAgreementCopy{Title: previous.Title, ContentMD: previous.ContentMD})
		if previous.Localization != nil {
			original = previous.Localization.Content
		}
		if reflect.DeepEqual(original, doc.Localization.Content) && len(doc.Localization.ReviewedLocales) == 0 {
			doc.Title, doc.ContentMD = original.Source.Title, original.Source.ContentMD
			continue
		}
		next, err := locale.Prepare(original, *doc.Localization, func(copy LoginAgreementCopy) error {
			if strings.TrimSpace(copy.Title) == "" {
				return apperror.BadRequest("LEGAL_TITLE_REQUIRED", "Document title is required.")
			}
			// Legal Markdown is limited in bytes, independently of title length.
			if len(copy.ContentMD) > 1<<20 {
				return apperror.BadRequest("LEGAL_CONTENT_TOO_LONG", "Document exceeds the allowed length.")
			}
			if strings.TrimSpace(doc.Localization.Source.ContentMD) != "" && strings.TrimSpace(copy.ContentMD) == "" {
				return apperror.BadRequest("LEGAL_CONTENT_REQUIRED", "Document content is required.")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		doc.Localization = &locale.Update[LoginAgreementCopy]{Content: next}
		doc.Title, doc.ContentMD = next.Source.Title, next.Source.ContentMD
	}
	return result, nil
}

// ConfiguredSiteTexts returns content decoded from the grouped site_texts setting.
// Decoder errors are discarded, retaining any populated entries; nil decoded
// maps become empty maps. Missing fields remain absent instead of acquiring
// editor defaults, so callers can identify deliberately configured empty text.
func ConfiguredSiteTexts(values map[string]string) LocalizedTexts {
	result := LocalizedTexts{}
	// Public override reporting depends on the saved group's actual keys. Keep
	// decoder-populated entries and leave default insertion to the editor-specific parsing function.
	_ = json.Unmarshal([]byte(values[SettingKeySiteTexts]), &result)
	if result == nil {
		return LocalizedTexts{}
	}
	return result
}
