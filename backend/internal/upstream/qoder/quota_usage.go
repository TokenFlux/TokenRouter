package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// quota_usage.go normalizes Qoder quota observations while distinguishing
// explicit zero fields from missing capacity values.
// Quota responses can report a field as zero or omit it entirely, and those
// cases affect derived remaining capacity. QuotaProgress stores presence flags
// beside numeric values so normalization can distinguish an explicit observation
// from a value synthesized for display.
//
// Canonical and legacy quota objects remain separate pointers in the response,
// allowing the provider reader to choose the first present bucket even when all
// its values are zero. Normalization can use cap or used plus remaining when
// requested, retains an explicitly supplied remaining amount, and clamps
// negative remaining to zero. Percentage values from zero through one are
// interpreted as fractions before rounding to two decimal percent places.

// QuotaUsageResponse contains site quota metadata and alternative wire names for
// capacity buckets.
type QuotaUsageResponse struct {
	DisplayMode          string           `json:"displayMode,omitempty"`               // upstream usage presentation mode
	Enterprise           *EnterpriseUsage `json:"enterpriseUsage,omitempty"`           // external enterprise usage view
	Dedicated            []DedicatedQuota `json:"dedicatedResourcePackages,omitempty"` // restricted credit package observations
	UserID               string           `json:"userId"`                              // quota user identifier
	UserType             string           `json:"userType"`                            // quota account classification
	UsageType            string           `json:"usageType"`                           // quota usage category
	TotalUsagePercentage float64          `json:"totalUsagePercentage"`                // percent or fraction
	IsQuotaExceeded      bool             `json:"isQuotaExceeded"`                     // reported quota exhaustion
	ExpiresAt            FlexibleInt64    `json:"expiresAt"`                           // upstream quota expiry
	UpgradeURL           string           `json:"upgradeUrl"`                          // plan upgrade URL
	AddCreditsURL        string           `json:"addCreditsUrl"`                       // credit purchase URL
	UserQuota            *QuotaProgress   `json:"userQuota"`                           // primary user quota
	// AddOnQuota and AddOnQuotaSnake are alternative addon buckets. The provider
	// reader selects the canonical pointer first, including a present zero-valued
	// object.
	AddOnQuota      *QuotaProgress `json:"addOnQuota"`   // canonical addon quota
	AddOnQuotaSnake *QuotaProgress `json:"add_on_quota"` // legacy addon quota
	// OrgResourcePackage, OrgResourcePkgSnake, SharedQuota, and SharedQuotaSnake
	// name alternative organization buckets. The provider reader chooses the first
	// present pointer in this order.
	OrgResourcePackage  *QuotaProgress `json:"orgResourcePackage"`   // canonical organization quota
	OrgResourcePkgSnake *QuotaProgress `json:"org_resource_package"` // legacy organization quota
	SharedQuota         *QuotaProgress `json:"sharedQuota"`          // canonical shared quota
	SharedQuotaSnake    *QuotaProgress `json:"shared_quota"`         // legacy shared quota
	IsPlanQuotaProrated bool           `json:"isPlanQuotaProrated"`  // reported prorated plan
}

// EnterpriseUsage identifies an upstream-owned usage view without numeric quota.
type EnterpriseUsage struct {
	OpenMode    string `json:"openMode"`              // upstream navigation mode
	DetailURL   string `json:"detailUrl"`             // upstream detail destination
	FallbackURL string `json:"fallbackUrl,omitempty"` // optional HTTPS browser destination
}

// QuotaLabel retains the upstream description of a dedicated package's scope.
type QuotaLabel struct {
	Dimension string `json:"dimension"` // restriction dimension name
	Value     string `json:"value"`     // restriction display value
}

// DedicatedQuota contains credits restricted to an upstream-described scope.
// These amounts never contribute to the unrestricted account balance because
// display labels alone do not establish machine-readable model eligibility.
type DedicatedQuota struct {
	ID          string        `json:"id"`            // upstream package identifier
	Name        string        `json:"name"`          // upstream package name
	Description string        `json:"description"`   // upstream scope explanation
	Status      string        `json:"status"`        // upstream package state
	Available   bool          `json:"available"`     // observed package availability
	Total       float64       `json:"total"`         // package credit total
	Used        float64       `json:"used"`          // package credits consumed
	Remaining   float64       `json:"remaining"`     // package credits remaining
	Percentage  float64       `json:"percentage"`    // observed utilization percentage
	Unit        string        `json:"unit"`          // upstream credit unit
	ExpiresAt   FlexibleInt64 `json:"expiresAt"`     // upstream package expiry
	Labels      []QuotaLabel  `json:"displayLabels"` // upstream scope display labels
}

// QuotaProgress holds observed quota amounts and JSON presence needed for
// display derivation.
type QuotaProgress struct {
	Total float64 `json:"total"` // observed total capacity
	Cap   float64 `json:"cap"`   // observed base capacity
	Used  float64 `json:"used"`  // observed consumed capacity
	// Remaining holds an observed value when its JSON field is present.
	// NormalizeQuotaProgress infers total minus used only when that observation is
	// absent.
	Remaining  float64 `json:"remaining"`  // observed remaining capacity
	Percentage float64 `json:"percentage"` // percent or fraction
	Unit       string  `json:"unit"`       // quota display unit
	// DetailURL and DetailURLSnake hold alternate bucket-detail links.
	// NormalizeQuotaProgress prefers nonblank DetailURL, then DetailURLSnake, and
	// trims the chosen text before returning display metadata.
	DetailURL      string `json:"detailUrl"`  // canonical detail URL
	DetailURLSnake string `json:"detail_url"` // legacy detail URL
	// Available retains an explicitly supplied availability flag. With cap-based
	// normalization, an absent JSON flag is derived from observed base capacity.
	Available      bool   `json:"available"`      // reported bucket availability
	OrganizationID string `json:"organizationId"` // quota organization identifier

	// totalSet, capSet, usedSet, remainingSet, percentageSet, and availableSet
	// distinguish nonnull JSON fields from omissions. Explicit zero or false values
	// remain observations during normalization.
	totalSet      bool // nonnull total present
	capSet        bool // nonnull cap present
	usedSet       bool // nonnull used present
	remainingSet  bool // nonnull remaining present
	percentageSet bool // nonnull percentage present
	availableSet  bool // nonnull available present
}

// GetUsagePresentation reads the desktop OpenAPI usage view with a device token.
// The caller supplies provider transport and cancellation. Response bodies are
// bounded and closed, and failed responses use the shared credential redaction.
func (c *OAuthClient) GetUsagePresentation(ctx context.Context, token string) (*QuotaUsageResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/sash/api/v2/me/usage", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent())
	response, err := c.do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	// Read one extra byte to reject oversized responses instead of accepting a
	// truncated prefix as an authoritative zero or empty account observation.
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		return nil, errors.New("qoder: usage response exceeds size limit")
	}
	if response.StatusCode != http.StatusOK {
		return nil, ParseAPIErrorBody(response.StatusCode, string(body))
	}
	return ParseUsagePresentation(body)
}

// ParseUsagePresentation distinguishes numeric credit views from enterprise
// navigation views. Invalid or mixed envelopes fail instead of becoming zero
// balances. Browser destinations must be HTTPS URLs without embedded credentials.
func ParseUsagePresentation(body []byte) (*QuotaUsageResponse, error) {
	var envelope struct {
		Mode       string          `json:"displayMode"`
		Quota      json.RawMessage `json:"qoderUsage"`
		Enterprise json.RawMessage `json:"enterpriseUsage"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, errors.New("qoder: invalid usage presentation")
	}
	switch envelope.Mode {
	case "qoder":
		if envelope.Enterprise != nil {
			return nil, errors.New("qoder: mixed usage presentation")
		}
		return parseCreditPresentation(envelope.Quota)
	case "enterprise":
		var view EnterpriseUsage
		if envelope.Quota != nil || json.Unmarshal(envelope.Enterprise, &view) != nil || view.DetailURL == "" {
			return nil, errors.New("qoder: invalid enterprise usage presentation")
		}
		switch view.OpenMode {
		case "externalBrowser":
			if !validUsageBrowserURL(view.DetailURL) || view.FallbackURL != "" {
				return nil, errors.New("qoder: invalid enterprise usage destination")
			}
		case "systemDeepLink":
			destination, err := url.Parse(view.DetailURL)
			if err != nil || destination.Scheme == "" || slices.Contains([]string{"http", "file", "javascript", "data"}, destination.Scheme) || view.FallbackURL != "" && !validUsageBrowserURL(view.FallbackURL) {
				return nil, errors.New("qoder: invalid enterprise usage destination")
			}
		default:
			return nil, errors.New("qoder: unknown enterprise usage navigation")
		}
		return &QuotaUsageResponse{DisplayMode: "enterprise", Enterprise: &view}, nil
	default:
		return nil, errors.New("qoder: unsupported usage presentation mode")
	}
}

// parseCreditPresentation validates the full desktop observation before publication.
// The legacy quota endpoint permits omitted flags. This endpoint requires explicit
// exhaustion and proration booleans, since interpreting an absent flag as false
// can release an active scheduling pause. Each supplied pool and dedicated
// package also has to be usable before the response can replace a saved snapshot.
//
// Desktop amount and scope fields have two spellings. Normalize them together
// before decoding the credit view so validation, display and scheduling use
// the same source when upstream supplies both versions of an optional field.
func parseCreditPresentation(raw json.RawMessage) (*QuotaUsageResponse, error) {
	invalid := errors.New("qoder: invalid credit usage presentation")
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !quotaBoolean(fields["isQuotaExceeded"]) || !quotaBoolean(fields["isPlanQuotaProrated"]) {
		return nil, invalid
	}
	for snake, camel := range map[string]string{
		"user_quota":                  "userQuota",
		"add_on_quota":                "addOnQuota",
		"org_resource_package":        "orgResourcePackage",
		"shared_quota":                "sharedQuota",
		"dedicated_resource_packages": "dedicatedResourcePackages",
		"total_usage_percentage":      "totalUsagePercentage",
		"expires_at":                  "expiresAt",
	} {
		if value := fields[snake]; value != nil && string(value) != "null" {
			fields[camel] = value
		}
		delete(fields, snake)
	}
	if !validQuotaFraction(fields["totalUsagePercentage"]) {
		return nil, invalid
	}
	for _, name := range []string{"userQuota", "addOnQuota", "orgResourcePackage", "sharedQuota"} {
		if bucket, present := fields[name]; present {
			var metrics map[string]json.RawMessage
			if json.Unmarshal(bucket, &metrics) != nil || metrics == nil || !validQuotaFraction(metrics["percentage"]) {
				return nil, invalid
			}
		}
	}
	if packages, present := fields["dedicatedResourcePackages"]; present {
		var items []map[string]json.RawMessage
		if json.Unmarshal(packages, &items) != nil || items == nil {
			return nil, invalid
		}
		for _, item := range items {
			var id string
			if json.Unmarshal(item["id"], &id) != nil || strings.TrimSpace(id) == "" || !quotaBoolean(item["available"]) || !validQuotaFraction(item["percentage"]) {
				return nil, invalid
			}
		}
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return nil, invalid
	}
	var quota QuotaUsageResponse
	if json.Unmarshal(normalized, &quota) != nil || strings.TrimSpace(quota.UserID) == "" || strings.TrimSpace(quota.UserType) == "" || quota.UsageType != "credits" {
		return nil, invalid
	}
	// Scheduling consumes the exact boolean fields validated above, including
	// explicit false. Unknown case variants must not override those observations.
	quota.IsQuotaExceeded = string(fields["isQuotaExceeded"]) == "true"
	quota.IsPlanQuotaProrated = string(fields["isPlanQuotaProrated"]) == "true"
	quota.DisplayMode = "qoder"
	return &quota, nil
}

// quotaBoolean accepts an explicitly supplied JSON boolean, including false.
func quotaBoolean(raw json.RawMessage) bool { return string(raw) == "true" || string(raw) == "false" }

// validQuotaFraction accepts absent utilization or an observed fraction from zero to one.
// Null, percentage strings and out-of-range numbers invalidate the desktop observation.
func validQuotaFraction(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	var fraction *float64
	return json.Unmarshal(raw, &fraction) == nil && fraction != nil && *fraction >= 0 && *fraction <= 1
}

// validUsageBrowserURL accepts HTTPS navigation without userinfo or control bytes.
func validUsageBrowserURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && !strings.ContainsAny(value, "\r\n\x00")
}

// UnmarshalJSON normalizes the optional dedicated balance and utilization fields.
// An explicit zero is authoritative. When the service omits remaining it can
// be derived from this package alone; its balance never joins general credits.
func (p *DedicatedQuota) UnmarshalJSON(data []byte) error {
	type quota DedicatedQuota
	var value quota
	fields := struct {
		*quota
		Remaining      *float64       `json:"remaining"`
		Percentage     *float64       `json:"percentage"`
		ExpiresAtSnake *FlexibleInt64 `json:"expires_at"`     // desktop expiration alias
		LabelsSnake    *[]QuotaLabel  `json:"display_labels"` // desktop model scope alias
	}{quota: &value}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields.ExpiresAtSnake != nil {
		value.ExpiresAt = *fields.ExpiresAtSnake
	}
	if fields.LabelsSnake != nil {
		value.Labels = *fields.LabelsSnake
	}
	value.Remaining = max(value.Total-value.Used, 0)
	if fields.Remaining != nil {
		value.Remaining = *fields.Remaining
	}
	if fields.Percentage != nil {
		value.Percentage = *fields.Percentage
	} else if value.Total > 0 {
		value.Percentage = value.Used / value.Total
	}
	*p = DedicatedQuota(value)
	return nil
}

// UnmarshalJSON decodes quota values and records which derivation inputs were
// present. The receiver is replaced with the decoded value before checking raw
// field presence. A JSON null field counts as absent, while a supplied numeric
// zero or false value counts as present. NormalizeQuotaProgress uses these flags
// to keep explicit zero remaining capacity from being replaced by total minus
// used and to retain supplied availability.
func (r *QuotaProgress) UnmarshalJSON(data []byte) error {
	// alias decodes quota values without recursively invoking the presence-tracking
	// JSON method.
	type alias QuotaProgress
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = QuotaProgress(decoded)

	var fields map[string]json.RawMessage
	// Numeric decoding already succeeded. If raw presence inspection fails, retain
	// the decoded values and let nonzero-value inference serve manually built
	// buckets.
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil
	}
	r.totalSet = qoderJSONHasAnyField(fields, "total")
	r.capSet = qoderJSONHasAnyField(fields, "cap")
	r.usedSet = qoderJSONHasAnyField(fields, "used")
	r.remainingSet = qoderJSONHasAnyField(fields, "remaining")
	r.percentageSet = qoderJSONHasAnyField(fields, "percentage")
	r.availableSet = qoderJSONHasAnyField(fields, "available")
	return nil
}

// qoderJSONHasAnyField reports whether any named raw JSON field is present and
// nonnull. An explicit zero or false is present; missing names and null values
// are absent for quota derivation.
func qoderJSONHasAnyField(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		return true
	}
	return false
}

// NormalizeQuotaProgress derives a display bucket from observed quota values and
// presence flags. Nil input returns nil. When useCapAsTotal is enabled, missing
// or nonpositive total can use cap and then used plus remaining; explicit
// remaining wins over inferred total minus used.
//
// Negative remaining becomes zero, and percentage uses an explicit value or the
// used/total fraction. Availability inference uses observed base cap or total
// when its JSON field was absent, keeping an explicit false authoritative. The
// result is newly allocated and trims display metadata.
func NormalizeQuotaProgress(raw *QuotaProgress, useCapAsTotal bool) *QuotaProgress {
	if raw == nil {
		return nil
	}
	total := 0.0
	if qoderQuotaRawFieldSet(raw.totalSet, raw.Total) {
		total = raw.Total
	}
	// Some buckets describe capacity with cap or used plus remaining. Apply those
	// alternatives when requested so the provider display can derive a complete
	// total.
	if useCapAsTotal && total <= 0 {
		if qoderQuotaRawFieldSet(raw.capSet, raw.Cap) {
			total = raw.Cap
		}
		if total <= 0 && (qoderQuotaRawFieldSet(raw.usedSet, raw.Used) || qoderQuotaRawFieldSet(raw.remainingSet, raw.Remaining)) {
			total = raw.Used + raw.Remaining
		}
	}
	used := 0.0
	if qoderQuotaRawFieldSet(raw.usedSet, raw.Used) {
		used = raw.Used
	}
	remaining := 0.0
	if qoderQuotaRawFieldSet(raw.remainingSet, raw.Remaining) {
		remaining = raw.Remaining
	} else if total > used {
		remaining = total - used
	}
	// Display remaining capacity cannot be negative after normalization.
	if remaining < 0 {
		remaining = 0
	}
	percentage := 0.0
	if qoderQuotaRawFieldSet(raw.percentageSet, raw.Percentage) {
		percentage = raw.Percentage
	} else if total > 0 && used > 0 {
		percentage = used / total
	}
	percentage = NormalizeQuotaPercentage(percentage)
	available := raw.Available
	if useCapAsTotal && !raw.availableSet {
		baseCapacity := 0.0
		if qoderQuotaRawFieldSet(raw.capSet, raw.Cap) {
			baseCapacity = raw.Cap
		} else if qoderQuotaRawFieldSet(raw.totalSet, raw.Total) {
			baseCapacity = raw.Total
		}
		available = baseCapacity > 0
	}
	return &QuotaProgress{
		Total:          total,
		Used:           used,
		Remaining:      remaining,
		Percentage:     percentage,
		Unit:           strings.TrimSpace(raw.Unit),
		DetailURL:      strings.TrimSpace(FirstNonEmptyQoder(raw.DetailURL, raw.DetailURLSnake)),
		Cap:            raw.Cap,
		Available:      available,
		OrganizationID: strings.TrimSpace(raw.OrganizationID),
	}
}

// qoderQuotaRawFieldSet treats nonzero manually constructed values as present
// alongside decoded presence flags.
func qoderQuotaRawFieldSet(explicit bool, value float64) bool {
	return explicit || value != 0
}

// NormalizeQuotaPercentage converts a reported fraction or percent and rounds
// it to two decimal places. Values through one become percentages; negative,
// NaN, and infinite inputs become zero. Large finite values reach the arithmetic
// unchanged, so rounding multiplication can overflow.
func NormalizeQuotaPercentage(value float64) float64 {
	// Invalid display percentages become zero so callers can format the result as a
	// finite number even when the upstream bucket has no usable percentage.
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	// Fractions through one become percentage units for display.
	if value >= 0 && value <= 1 {
		value *= 100
	}
	// Round the displayed percentage to two decimal places.
	return math.Round(value*100) / 100
}
