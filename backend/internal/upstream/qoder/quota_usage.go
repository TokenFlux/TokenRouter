package qoder

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
)

// QuotaUsageResponse 保留站点额度报文及历史字段别名。
type QuotaUsageResponse struct {
	UserID               string         `json:"userId"`
	UserType             string         `json:"userType"`
	UsageType            string         `json:"usageType"`
	TotalUsagePercentage float64        `json:"totalUsagePercentage"`
	IsQuotaExceeded      bool           `json:"isQuotaExceeded"`
	ExpiresAt            FlexibleInt64  `json:"expiresAt"`
	UpgradeURL           string         `json:"upgradeUrl"`
	AddCreditsURL        string         `json:"addCreditsUrl"`
	UserQuota            *QuotaProgress `json:"userQuota"`
	AddOnQuota           *QuotaProgress `json:"addOnQuota"`
	AddOnQuotaSnake      *QuotaProgress `json:"add_on_quota"`
	OrgResourcePackage   *QuotaProgress `json:"orgResourcePackage"`
	OrgResourcePkgSnake  *QuotaProgress `json:"org_resource_package"`
	SharedQuota          *QuotaProgress `json:"sharedQuota"`
	SharedQuotaSnake     *QuotaProgress `json:"shared_quota"`
	IsPlanQuotaProrated  bool           `json:"isPlanQuotaProrated"`
}

// QuotaProgress 区分缺省字段与显式零值，避免误推导可用额度。
type QuotaProgress struct {
	Total          float64 `json:"total"`
	Cap            float64 `json:"cap"`
	Used           float64 `json:"used"`
	Remaining      float64 `json:"remaining"`
	Percentage     float64 `json:"percentage"`
	Unit           string  `json:"unit"`
	DetailURL      string  `json:"detailUrl"`
	DetailURLSnake string  `json:"detail_url"`
	Available      bool    `json:"available"`
	OrganizationID string  `json:"organizationId"`

	totalSet      bool
	capSet        bool
	usedSet       bool
	remainingSet  bool
	percentageSet bool
	availableSet  bool
}

func (r *QuotaProgress) UnmarshalJSON(data []byte) error {
	type alias QuotaProgress
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = QuotaProgress(decoded)

	var fields map[string]json.RawMessage
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

// NormalizeQuotaProgress 只归一化供应商计量，不执行账号健康或资金决策。
func NormalizeQuotaProgress(raw *QuotaProgress, useCapAsTotal bool) *QuotaProgress {
	if raw == nil {
		return nil
	}
	total := 0.0
	if qoderQuotaRawFieldSet(raw.totalSet, raw.Total) {
		total = raw.Total
	}
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

func qoderQuotaRawFieldSet(explicit bool, value float64) bool {
	return explicit || value != 0
}

func NormalizeQuotaPercentage(value float64) float64 {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value >= 0 && value <= 1 {
		value *= 100
	}
	return math.Round(value*100) / 100
}
