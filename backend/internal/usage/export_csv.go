package usage

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// export_csv.go formats usage records into UTF-8-BOM CSV streams for regular
// users and administrators while neutralizing spreadsheet formula prefixes.
// User and administrator exports project different column sets from the same
// UsageLog snapshot: regular users receive 19 customer-facing columns with
// 8-decimal charges, whereas administrators receive 38 operational columns with
// outbound model diagnostics, provider multipliers, 6-decimal costs, and
// serialized request-timing observations.
//
// Writing a leading UTF-8 byte order mark before the header ensures desktop
// spreadsheet applications decode Chinese headers and API-key names without
// mojibake. Untrusted text cells beginning with spreadsheet formula triggers
// receive a leading apostrophe before CSV quoting so user-supplied model names,
// emails, or user agents cannot execute formulas when opened in Excel or Calc.

// UTF-8 byte order mark and billing-mode constants standardize CSV output across
// user and administrator export scopes.
const (
	utf8BOM = "\xEF\xBB\xBF" // leading UTF-8 byte order mark for spreadsheet compatibility

	exportBillingModeToken      = "token"       // token-based pricing mode
	exportBillingModePerRequest = "per_request" // per-request pricing mode
	exportBillingModeImage      = "image"       // per-image pricing mode
	exportBillingModeVideo      = "video"       // video pricing mode
)

// usageExportCSVWriter streams localized CSV headers and usage-log batches in
// the task's snapshotted IANA timezone and currency unit.
type usageExportCSVWriter struct {
	csvw            *csv.Writer    // buffered RFC 4180 CSV encoder
	scope           string         // user or admin column layout selector
	locale          string         // normalized zh or en label locale
	balanceUnitName string         // snapshotted user charge currency label
	loc             *time.Location // validated IANA timezone location
	recordBuf       []string       // reusable per-row column slice
}

// newUsageExportCSVWriter writes the UTF-8 BOM and localized column header row
// immediately so even zero-row exports produce a valid spreadsheet document.
// Nil locations default to UTC, and blank currency units fall back to USD.
func newUsageExportCSVWriter(w io.Writer, scope, locale, balanceUnitName string, loc *time.Location) (*usageExportCSVWriter, error) {
	if w == nil {
		return nil, fmt.Errorf("csv destination writer is nil")
	}
	if loc == nil {
		loc = time.UTC
	}
	normalizedLocale := NormalizeUsageExportLocale(locale)
	unit := strings.TrimSpace(balanceUnitName)
	if unit == "" {
		unit = defaultUsageExportBalanceUnit
	}

	// Prepend the UTF-8 BOM once before csv.Writer emits header cells so Excel
	// recognizes UTF-8 encoding without corrupting the first header label.
	if _, err := io.WriteString(w, utf8BOM); err != nil {
		return nil, fmt.Errorf("write utf-8 bom: %w", err)
	}

	csvw := csv.NewWriter(w)
	headers := usageExportHeaders(scope, normalizedLocale, unit)
	if err := csvw.Write(headers); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}
	csvw.Flush()
	if err := csvw.Error(); err != nil {
		return nil, fmt.Errorf("flush csv header: %w", err)
	}

	return &usageExportCSVWriter{
		csvw:            csvw,
		scope:           scope,
		locale:          normalizedLocale,
		balanceUnitName: unit,
		loc:             loc,
		recordBuf:       make([]string, len(headers)),
	}, nil
}

// WriteBatch formats and flushes one keyset batch of usage records to the
// underlying CSV stream. Empty batches succeed without flushing extra bytes.
func (w *usageExportCSVWriter) WriteBatch(rows []UsageLog) error {
	if len(rows) == 0 {
		return nil
	}
	for i := range rows {
		if w.scope == UsageExportScopeAdmin {
			w.fillAdminRow(&rows[i])
		} else {
			w.fillUserRow(&rows[i])
		}
		if err := w.csvw.Write(w.recordBuf); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	w.csvw.Flush()
	if err := w.csvw.Error(); err != nil {
		return fmt.Errorf("flush csv batch: %w", err)
	}
	return nil
}

// usageExportHeaders returns the ordered localized header labels for user (19)
// or administrator (38) CSV exports.
func usageExportHeaders(scope, locale, balanceUnit string) []string {
	isEN := locale == UsageExportLocaleEN
	if scope == UsageExportScopeAdmin {
		if isEN {
			return []string{
				"Time",
				"User",
				"API Key",
				"Provider",
				"Model",
				"Upstream",
				"Upstream response model",
				"Response model differs",
				"Requested Reasoning Effort",
				"Reasoning Effort",
				"Group",
				"Inbound Endpoint",
				"Upstream Endpoint",
				"Type",
				"Input Tokens",
				"Output Tokens",
				"Cache Read Tokens",
				"Cache Creation Tokens",
				"Input Cost (USD)",
				"Output Cost (USD)",
				"Cache Read Cost (USD)",
				"Cache Creation Cost (USD)",
				"Rate",
				"Provider rate",
				"Original (USD)",
				fmt.Sprintf("User billed (%s)", balanceUnit),
				"Provider billed (USD)",
				"First output / event (ms)",
				"Timing mode",
				"Duration",
				"TPS (tok/s)",
				"Detailed Timing",
				"Request ID",
				"Upstream ID",
				"Request source",
				"Cost completeness",
				"User-Agent",
				"IP",
			}
		}
		return []string{
			"时间",
			"用户",
			"API 密钥",
			"提供商",
			"模型",
			"上游",
			"上游响应模型",
			"响应模型不同",
			"请求推理强度",
			"推理强度",
			"分组",
			"入站端点",
			"上游端点",
			"类型",
			"输入 Token",
			"输出 Token",
			"缓存读取 Token",
			"缓存创建 Token",
			"输入费用 (USD)",
			"输出费用 (USD)",
			"缓存读取费用 (USD)",
			"缓存创建费用 (USD)",
			"倍率",
			"提供商倍率",
			"原始 (USD)",
			fmt.Sprintf("用户扣费 (%s)", balanceUnit),
			"提供商计费 (USD)",
			"首字／首包(ms)",
			"统计口径",
			"耗时",
			"TPS (tok/s)",
			"详细耗时",
			"请求ID",
			"上游ID",
			"请求来源",
			"费用完整性",
			"User-Agent",
			"IP",
		}
	}

	if isEN {
		return []string{
			"Time",
			"API Key",
			"Model",
			"Reasoning Effort",
			"Inbound Endpoint",
			"IP",
			"Type",
			"Billing Mode",
			"Input Tokens",
			"Output Tokens",
			"Cache Read Tokens",
			"Cache Creation Tokens",
			"Rate",
			fmt.Sprintf("User billed (%s)", balanceUnit),
			"Original (USD)",
			"First output / event (ms)",
			"Timing mode",
			"Duration",
			"TPS (tok/s)",
		}
	}
	return []string{
		"时间",
		"API 密钥",
		"模型",
		"推理强度",
		"入站端点",
		"IP",
		"类型",
		"计费模式",
		"输入 Token",
		"输出 Token",
		"缓存读取 Token",
		"缓存创建 Token",
		"倍率",
		fmt.Sprintf("用户扣费 (%s)", balanceUnit),
		"原始 (USD)",
		"首字／首包(ms)",
		"统计口径",
		"耗时",
		"TPS (tok/s)",
	}
}

// fillUserRow populates the 19 user-facing CSV columns into w.recordBuf.
func (w *usageExportCSVWriter) fillUserRow(log *UsageLog) {
	keyName := ""
	if log.APIKey != nil {
		keyName = log.APIKey.Name
	}
	w.recordBuf[0] = log.CreatedAt.In(w.loc).Format(time.RFC3339)
	w.recordBuf[1] = sanitizeSpreadsheetCell(keyName)
	w.recordBuf[2] = sanitizeSpreadsheetCell(displayUsageModel(log))
	w.recordBuf[3] = sanitizeReasoningCell(formatReasoningEffortMapping(log.RequestedReasoningEffort, log.ReasoningEffort))
	w.recordBuf[4] = sanitizeSpreadsheetCell(optionalString(log.InboundEndpoint))
	w.recordBuf[5] = sanitizeSpreadsheetCell(optionalString(log.IPAddress))
	w.recordBuf[6] = formatRequestTypeLabel(log.EffectiveRequestType(), w.locale)
	w.recordBuf[7] = formatBillingModeLabel(displayBillingMode(log), w.locale)
	w.recordBuf[8] = strconv.Itoa(log.InputTokens)
	w.recordBuf[9] = strconv.Itoa(log.OutputTokens)
	w.recordBuf[10] = strconv.Itoa(log.CacheReadTokens)
	w.recordBuf[11] = strconv.Itoa(log.CacheCreationTokens)
	w.recordBuf[12] = strconv.FormatFloat(log.RateMultiplier, 'f', -1, 64)
	w.recordBuf[13] = fmt.Sprintf("%.8f", log.ActualCost)
	w.recordBuf[14] = fmt.Sprintf("%.8f", log.TotalCost)
	w.recordBuf[15] = formatOptionalInt(log.Timings.TTFTMs())
	w.recordBuf[16] = formatTimingModeLabel(log.Timings, w.locale)
	w.recordBuf[17] = formatOptionalInt(log.DurationMs)
	w.recordBuf[18] = formatOptionalTPS(log.Timings.TPS(log.OutputTokens))
}

// fillAdminRow populates the 38 administrator CSV columns into w.recordBuf.
func (w *usageExportCSVWriter) fillAdminRow(log *UsageLog) {
	userEmail := ""
	if log.User != nil {
		userEmail = log.User.Email
	}
	keyName := ""
	if log.APIKey != nil {
		keyName = log.APIKey.Name
	}
	providerName := ""
	if log.Provider != nil {
		providerName = log.Provider.Name
	}
	groupName := ""
	if log.Group != nil {
		groupName = log.Group.Name
	}
	requestedEffortSource := log.RequestedReasoningEffort
	if requestedEffortSource == nil || strings.TrimSpace(*requestedEffortSource) == "" {
		requestedEffortSource = log.ReasoningEffort
	}
	providerRate := 1.0
	if log.ProviderRateMultiplier != nil {
		providerRate = *log.ProviderRateMultiplier
	}
	costState := ""
	if log.SystemUsage != nil {
		costState = log.SystemUsage.CostState
	}

	w.recordBuf[0] = log.CreatedAt.In(w.loc).Format(time.RFC3339)
	w.recordBuf[1] = sanitizeSpreadsheetCell(userEmail)
	w.recordBuf[2] = sanitizeSpreadsheetCell(keyName)
	w.recordBuf[3] = sanitizeSpreadsheetCell(providerName)
	w.recordBuf[4] = sanitizeSpreadsheetCell(displayUsageModel(log))
	w.recordBuf[5] = sanitizeSpreadsheetCell(optionalString(log.UpstreamModel))
	w.recordBuf[6] = sanitizeSpreadsheetCell(optionalString(log.UpstreamResponseModel))
	w.recordBuf[7] = formatOptionalBoolLabel(log.UpstreamModelMismatch, w.locale)
	w.recordBuf[8] = sanitizeReasoningCell(formatReasoningEffort(requestedEffortSource))
	w.recordBuf[9] = sanitizeReasoningCell(formatReasoningEffort(log.ReasoningEffort))
	w.recordBuf[10] = sanitizeSpreadsheetCell(groupName)
	w.recordBuf[11] = sanitizeSpreadsheetCell(optionalString(log.InboundEndpoint))
	w.recordBuf[12] = sanitizeSpreadsheetCell(optionalString(log.UpstreamEndpoint))
	w.recordBuf[13] = formatRequestTypeLabel(log.EffectiveRequestType(), w.locale)
	w.recordBuf[14] = strconv.Itoa(log.InputTokens)
	w.recordBuf[15] = strconv.Itoa(log.OutputTokens)
	w.recordBuf[16] = strconv.Itoa(log.CacheReadTokens)
	w.recordBuf[17] = strconv.Itoa(log.CacheCreationTokens)
	w.recordBuf[18] = fmt.Sprintf("%.6f", log.InputCost)
	w.recordBuf[19] = fmt.Sprintf("%.6f", log.OutputCost)
	w.recordBuf[20] = fmt.Sprintf("%.6f", log.CacheReadCost)
	w.recordBuf[21] = fmt.Sprintf("%.6f", log.CacheCreationCost)
	w.recordBuf[22] = formatSignificantFour(log.RateMultiplier)
	w.recordBuf[23] = formatSignificantFour(providerRate)
	w.recordBuf[24] = fmt.Sprintf("%.6f", log.TotalCost)
	w.recordBuf[25] = fmt.Sprintf("%.6f", log.ActualCost)
	w.recordBuf[26] = formatAdminProviderBilled(log, providerRate, costState)
	w.recordBuf[27] = formatOptionalInt(log.Timings.TTFTMs())
	w.recordBuf[28] = formatTimingModeLabel(log.Timings, w.locale)
	w.recordBuf[29] = formatOptionalInt(log.DurationMs)
	w.recordBuf[30] = formatOptionalTPS(log.Timings.TPS(log.OutputTokens))
	w.recordBuf[31] = formatDetailedTimingForExport(log.Timings)
	w.recordBuf[32] = sanitizeSpreadsheetCell(log.RequestID)
	w.recordBuf[33] = sanitizeSpreadsheetCell(optionalString(log.UpstreamRequestID))
	w.recordBuf[34] = formatRequestSourceLabel(log.EffectiveRequestSource(), w.locale)
	w.recordBuf[35] = sanitizeSpreadsheetCell(costState)
	w.recordBuf[36] = sanitizeSpreadsheetCell(optionalString(log.UserAgent))
	w.recordBuf[37] = sanitizeSpreadsheetCell(optionalString(log.IPAddress))
}

// sanitizeSpreadsheetCell neutralizes spreadsheet formula prefixes in untrusted
// text cells by prepending a single quote when the first byte is a formula trigger.
func sanitizeSpreadsheetCell(value string) string {
	if value == "" {
		return ""
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		// Spreadsheet applications evaluate leading =, +, -, @, tab, and CR as
		// formulas or DDE commands; a leading apostrophe forces plain-text parsing.
		return "'" + value
	default:
		return value
	}
}

// sanitizeReasoningCell preserves the sentinel "-" placeholder while guarding
// custom provider reasoning strings against formula injection.
func sanitizeReasoningCell(value string) string {
	if value == "-" {
		return value
	}
	return sanitizeSpreadsheetCell(value)
}

// displayUsageModel returns RequestedModel when recorded, falling back to Model
// for historical records that lack a separate client model observation.
func displayUsageModel(log *UsageLog) string {
	if log == nil {
		return ""
	}
	if log.RequestedModel != "" {
		return log.RequestedModel
	}
	return log.Model
}

// optionalString dereferences a nullable string field or returns empty text.
func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// formatOptionalInt formats a nullable integer measurement or returns empty text.
func formatOptionalInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

// formatOptionalTPS formats a nullable tokens-per-second measurement to one decimal.
func formatOptionalTPS(value *float64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%.1f", *value)
}

// formatSignificantFour formats a multiplier to four significant digits,
// matching JavaScript's Number.prototype.toPrecision(4) for rate display.
func formatSignificantFour(value float64) string {
	return strings.TrimSuffix(fmt.Sprintf("%#.4g", value), ".")
}

// formatAdminProviderBilled formats provider-side USD cost or returns empty text
// when system-usage cost completeness is explicitly unknown.
func formatAdminProviderBilled(log *UsageLog, providerRate float64, costState string) string {
	if costState == "unknown" {
		return ""
	}
	base := log.TotalCost
	if log.ProviderStatsCost != nil {
		base = *log.ProviderStatsCost
	}
	return fmt.Sprintf("%.6f", base*providerRate)
}

// formatOptionalBoolLabel renders a tri-state boolean as localized Yes/No or empty text.
func formatOptionalBoolLabel(value *bool, locale string) string {
	if value == nil {
		return ""
	}
	if locale == UsageExportLocaleEN {
		if *value {
			return "Yes"
		}
		return "No"
	}
	if *value {
		return "是"
	}
	return "否"
}

// formatRequestTypeLabel renders the localized display label for a normalized request category.
func formatRequestTypeLabel(rt RequestType, locale string) string {
	isEN := locale == UsageExportLocaleEN
	switch rt {
	case RequestTypeWSV2:
		return "WS"
	case RequestTypeStream:
		if isEN {
			return "Stream"
		}
		return "流式"
	case RequestTypeSync:
		if isEN {
			return "Sync"
		}
		return "同步"
	case RequestTypeCyberBlocked:
		if isEN {
			return "Cyber Blocked"
		}
		return "Cyber 阻断"
	case RequestTypeLive:
		return "Live"
	default:
		if isEN {
			return "Unknown"
		}
		return "未知"
	}
}

// displayBillingMode infers image billing when positive image counts accompany
// a non-token, non-video mode, matching the frontend billingMode utility.
func displayBillingMode(log *UsageLog) string {
	mode := ""
	if log.BillingMode != nil {
		mode = *log.BillingMode
	}
	if log.ImageCount > 0 && mode != exportBillingModeToken && mode != exportBillingModeVideo {
		return exportBillingModeImage
	}
	return mode
}

// formatBillingModeLabel renders the localized billing mode label, defaulting
// unrecognized or absent modes to token billing.
func formatBillingModeLabel(mode, locale string) string {
	isEN := locale == UsageExportLocaleEN
	switch mode {
	case exportBillingModePerRequest:
		if isEN {
			return "Per Request"
		}
		return "按次"
	case exportBillingModeImage:
		if isEN {
			return "Image"
		}
		return "按次(图片)"
	case exportBillingModeVideo:
		if isEN {
			return "Video"
		}
		return "按次(视频)"
	default:
		if isEN {
			return "Token"
		}
		return "按量"
	}
}

// formatTimingModeLabel renders the localized first-token/first-event policy
// label or empty text when no timing snapshot was recorded.
func formatTimingModeLabel(perf *timing.Performance, locale string) string {
	if perf == nil {
		return ""
	}
	isEN := locale == UsageExportLocaleEN
	switch perf.Mode {
	case timing.FirstEvent:
		if isEN {
			return "First event"
		}
		return "首包"
	case timing.FirstOutput:
		if isEN {
			return "First"
		}
		return "首字"
	default:
		return ""
	}
}

// formatRequestSourceLabel renders the localized administrator request-source label.
func formatRequestSourceLabel(source, locale string) string {
	isEN := locale == UsageExportLocaleEN
	switch source {
	case "system":
		if isEN {
			return "System request"
		}
		return "系统请求"
	case SourceGroupProbe:
		if isEN {
			return "Group probe"
		}
		return "分组探针"
	case SourceProviderTest:
		if isEN {
			return "Manual provider test"
		}
		return "手动提供商测试"
	case SourceScheduledTest:
		if isEN {
			return "Scheduled provider test"
		}
		return "定时提供商测试"
	case SourceQuotaProbe:
		if isEN {
			return "Quota probe"
		}
		return "额度探测"
	default:
		if isEN {
			return "User request"
		}
		return "用户请求"
	}
}

// formatReasoningEffort normalizes known reasoning levels or capitalizes the
// leading rune of unrecognized values, returning "-" when absent or minimal.
func formatReasoningEffort(effort *string) string {
	if effort == nil {
		return "-"
	}
	raw := strings.TrimSpace(*effort)
	if raw == "" {
		return "-"
	}
	normalized := normalizeReasoningToken(raw)
	switch normalized {
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh", "extrahigh":
		return "XHigh"
	case "max":
		return "Max"
	case "none", "minimal":
		return "-"
	default:
		r, width := utf8.DecodeRuneInString(raw)
		if r == utf8.RuneError && width <= 1 {
			return strings.ToUpper(raw)
		}
		return string(unicode.ToUpper(r)) + raw[width:]
	}
}

// formatReasoningEffortMapping renders requested and forwarded reasoning levels
// as a single label when equal or absent on one side, or "Req → Fwd" when rewritten.
func formatReasoningEffortMapping(requested, forwarded *string) string {
	reqLabel := formatReasoningEffort(requested)
	fwdLabel := formatReasoningEffort(forwarded)
	if reqLabel == "-" && fwdLabel == "-" {
		return "-"
	}
	if reqLabel == "-" || reasoningEffortsEqual(requested, forwarded) {
		if fwdLabel == "-" {
			return reqLabel
		}
		return fwdLabel
	}
	if fwdLabel == "-" {
		return reqLabel
	}
	return reqLabel + " → " + fwdLabel
}

// reasoningEffortsEqual compares two reasoning strings after stripping spaces,
// hyphens, underscores, and case differences.
func reasoningEffortsEqual(left, right *string) bool {
	if left == nil || right == nil {
		return false
	}
	a := normalizeReasoningToken(*left)
	b := normalizeReasoningToken(*right)
	return a != "" && a == b
}

// normalizeReasoningToken strips spaces, hyphens, and underscores from a
// lowercased reasoning string so spelling variants compare identically.
func normalizeReasoningToken(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if r == '-' || r == '_' || unicode.IsSpace(r) {
			continue
		}
		_, _ = b.WriteRune(r)
	}
	return b.String()
}

// formatDetailedTimingForExport serializes present timing observations in the
// exact field order of dto.UsageLogTiming, appending "ms" to millisecond keys.
func formatDetailedTimingForExport(perf *timing.Performance) string {
	if perf == nil {
		return ""
	}
	var parts []string
	parts = append(parts,
		"upstream_stream="+strconv.FormatBool(perf.Stream),
		"upstream_complete="+strconv.FormatBool(perf.Complete),
		"usage_complete="+strconv.FormatBool(perf.UsageComplete),
	)

	orderedNumericKeys := []string{
		"upstream_started_ms",
		"upstream_first_output_ms",
		"upstream_last_output_ms",
		"upstream_completed_ms",
		"upstream_finished_ms",
		"attempt_number",
		"request_content_length",
		"request_raw_bytes",
		"request_decoded_bytes",
		"request_compressed",
		"upstream_request_bytes",
		"timing_stream",
		"timing_admitted",
		"request_body_read_ms",
		"request_body_decode_ms",
		"preprocess_ms",
		"ingress_validate_ms",
		"bootstrap_ms",
		"moderation_extract_ms",
		"moderation_external_ms",
		"session_ms",
		"scheduler_select_ms",
		"provider_prepare_ms",
		"user_slot_wait_ms",
		"provider_slot_wait_ms",
		"retry_provider_slot_wait_ms",
		"provider_slot_acquired_ms",
		"upstream_get_conn_ms",
		"upstream_got_conn_ms",
		"upstream_wrote_request_ms",
		"upstream_first_response_byte_ms",
		"upstream_first_event_ms",
		"first_visible_output_ms",
		"first_downstream_flush_ms",
		"upstream_get_conn_count",
		"upstream_got_conn_count",
		"upstream_attempt_count",
		"upstream_first_response_byte_count",
	}
	for _, key := range orderedNumericKeys {
		v, ok := perf.Values[key]
		if !ok {
			continue
		}
		suffix := ""
		if strings.HasSuffix(key, "_ms") {
			suffix = "ms"
		}
		parts = append(parts, key+"="+strconv.FormatInt(v, 10)+suffix)
	}

	for _, key := range []string{"upstream_connection_reused", "upstream_wrote_request_error"} {
		v, ok := perf.Values[key]
		if !ok {
			continue
		}
		parts = append(parts, key+"="+strconv.FormatBool(v == 1))
	}

	return strings.Join(parts, "; ")
}
