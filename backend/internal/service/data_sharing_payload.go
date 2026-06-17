package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

func dataShareStatusIsError(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "error", "failed", "failure":
		return true
	default:
		return false
	}
}

func dataShareToolContentLooksError(content any) bool {
	text := dataShareContentText(content)
	if !strings.Contains(text, "Process exited with code ") {
		return false
	}
	return !strings.Contains(text, "Process exited with code 0")
}

func normalizeDataShareTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	seen := map[string]struct{}{}
	var visit func(map[string]any)
	visit = func(tool map[string]any) {
		if nested := mapsFromAny(tool["tools"]); len(nested) > 0 {
			for _, item := range nested {
				visit(item)
			}
		}
		normalized, ok := normalizeDataShareTool(tool)
		if !ok {
			return
		}
		name := stringFromAny(normalized["name"])
		if _, exists := seen[name]; exists {
			return
		}
		seen[name] = struct{}{}
		out = append(out, normalized)
	}
	for _, tool := range tools {
		visit(tool)
	}
	return out
}

func normalizeDataShareTool(tool map[string]any) (map[string]any, bool) {
	if tool == nil {
		return nil, false
	}
	functionMap, _ := mapFromAny(tool["function"])
	name := firstNonBlank(stringFromAny(tool["name"]), stringFromAny(functionMap["name"]), dataShareToolNameFromType(stringFromAny(tool["type"])))
	description := firstNonBlank(stringFromAny(tool["description"]), stringFromAny(functionMap["description"]), defaultDataShareToolDescription(name, stringFromAny(tool["type"])))
	parameters := firstPresentAny(tool["parameters"], functionMap["parameters"], tool["input_schema"], defaultDataShareToolParameters(name, stringFromAny(tool["type"])))
	parameterMap, ok := mapFromAny(parameters)
	if strings.TrimSpace(name) == "" || strings.TrimSpace(description) == "" || !ok || len(parameterMap) == 0 {
		return nil, false
	}
	out := map[string]any{
		"name":        strings.TrimSpace(name),
		"description": strings.TrimSpace(description),
		"parameters":  parameterMap,
	}
	if toolType := normalizeDataShareToolType(stringFromAny(tool["type"])); toolType != "" {
		out["type"] = toolType
	}
	if strict, ok := tool["strict"]; ok {
		out["strict"] = strict
	}
	return out, true
}

func dataShareToolNameFromType(toolType string) string {
	switch strings.TrimSpace(toolType) {
	case "tool_search":
		return "tool_search"
	case "web_search", "web_search_preview", "web_search_20250305":
		return "web_search"
	default:
		return ""
	}
}

func defaultDataShareToolDescription(name string, toolType string) string {
	switch firstNonBlank(name, toolType) {
	case "apply_patch":
		return "Apply a structured patch to files in the workspace."
	case "tool_search":
		return "Search available deferred tools by text query."
	case "web_search", "web_search_preview", "web_search_20250305":
		return "Search the web for relevant information."
	default:
		return ""
	}
}

func defaultDataShareToolParameters(name string, toolType string) map[string]any {
	switch firstNonBlank(name, dataShareToolNameFromType(toolType)) {
	case "apply_patch":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patch": map[string]any{"type": "string", "description": "符合 apply_patch 语法的补丁内容。"},
			},
			"required": []string{"patch"},
		}
	case "web_search":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "搜索关键词。"},
			},
			"required": []string{"query"},
		}
	default:
		return nil
	}
}

func normalizeDataShareToolType(toolType string) string {
	switch strings.TrimSpace(toolType) {
	case "function", "custom", "namespace":
		return strings.TrimSpace(toolType)
	case "tool_search", "web_search", "web_search_preview", "web_search_20250305":
		return "function"
	default:
		return ""
	}
}

func normalizeDataShareContentValue(value any) any {
	if value == nil {
		return ""
	}
	if text := dataShareContentText(value); text != "" && dataShareContentIdentityCanUseText(value) {
		return text
	}
	return value
}

func dataShareContentText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := dataShareContentText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []map[string]any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := dataShareContentText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "parts", "output", "summary"} {
			if text := dataShareContentText(v[key]); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func extractSystemPromptFromMessages(messages []map[string]any) string {
	for _, msg := range messages {
		role := strings.TrimSpace(stringFromAny(msg["role"]))
		if role == "system" || role == "developer" {
			if text := strings.TrimSpace(dataShareContentText(msg["content"])); text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeDataShareUsage(usage map[string]any) map[string]any {
	out := cloneDataShareMap(usage)
	inputTokens := intFromAny(out["input_tokens"])
	outputTokens := intFromAny(out["output_tokens"])
	cacheReadTokens := intFromAny(firstPresentAny(out["cache_read_input_tokens"], out["cache_read_tokens"]))
	cacheCreateTokens := intFromAny(firstPresentAny(out["cache_creation_input_tokens"], out["cache_creation_tokens"]))
	totalTokens := intFromAny(out["total_tokens"])
	if totalTokens <= 0 {
		totalTokens = inputTokens + outputTokens + cacheReadTokens + cacheCreateTokens
	}
	return map[string]any{
		"input_tokens":                inputTokens,
		"output_tokens":               outputTokens,
		"total_tokens":                totalTokens,
		"cache_creation_input_tokens": cacheCreateTokens,
		"cache_read_input_tokens":     cacheReadTokens,
	}
}

func normalizeDataShareMeta(meta map[string]any) map[string]any {
	out := cloneDataShareMap(meta)
	sourceIDs := appendStringValues(nil, stringsFromAny(out["source_request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringsFromAny(out["request_ids"])...)
	sourceIDs = appendStringValues(sourceIDs, stringFromAny(out["request_id"]))
	out["source_request_ids"] = sourceIDs
	delete(out, "request_ids")
	return out
}

func validateDataSharePayloadQuality(payload map[string]any) []string {
	return ValidateDataShareSessionQuality(
		stringFromAny(payload["model"]),
		stringFromAny(payload["system_prompt"]),
		mapsFromAny(payload["messages"]),
		mapsFromAny(payload["tools"]),
		normalizeDataShareUsage(mapAnyFromAny(payload["usage"])),
	)
}

// DataSharePayloadQualityStatus 把附件质量规则归纳成完整、部分完整、无效三态。
func DataSharePayloadQualityStatus(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) string {
	status, _ := DataShareSessionQuality(model, systemPrompt, messages, tools, usage)
	return status
}

func dataShareCompletionState(qualityStatus string) (string, bool) {
	if qualityStatus == DataShareQualityComplete {
		return DataShareStatusCompleted, true
	}
	return DataShareStatusTerminated, false
}

// DataShareQualityExportable 表示默认导出是否应包含该质量状态。
func DataShareQualityExportable(qualityStatus string) bool {
	return qualityStatus == DataShareQualityComplete || qualityStatus == DataShareQualityPartial
}

// exportableDataShareMessages 仅裁掉尾部未闭合工具链，裁切后仍需完整通过同一套交付校验。
func exportableDataShareMessages(model string, systemPrompt string, messages []map[string]any, tools []map[string]any, usage map[string]any) ([]map[string]any, []string) {
	compact := CompactDataShareMessages(normalizeDataShareMessages(messages))
	if report := evaluateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage); report.Status == DataShareQualityComplete {
		return append([]map[string]any{}, compact...), nil
	} else if report.Status != DataShareQualityPartial {
		return nil, report.Errors
	}
	if end := dataShareCompleteTrimPrefixLen(model, systemPrompt, compact, tools, usage); end > 0 {
		return append([]map[string]any{}, compact[:end]...), nil
	}
	return nil, validateCompactDataShareSessionQuality(model, systemPrompt, compact, tools, usage)
}

func exportPayloadFromSession(session *DataShareSession) map[string]any {
	return exportPayloadFromSessionWithOptions(session, false)
}

func exportPayloadFromFinalizedSession(session *DataShareSession) map[string]any {
	return exportPayloadFromSessionWithOptions(session, true)
}

func exportPayloadFromSessionWithOptions(session *DataShareSession, finalized bool) map[string]any {
	if session == nil {
		return map[string]any{}
	}
	payload := cloneDataShareMap(session.SessionJSON)
	payload["trajectory_id"] = session.TrajectoryID
	payload["session_id"] = session.SessionID
	payload["dataset"] = session.Dataset
	payload["provider"] = session.Provider
	payload["model"] = session.Model
	payload["request_path"] = firstNonBlank(session.RequestPath, stringFromAny(payload["request_path"]), stringFromAny(session.Meta["request_path"]), stringFromAny(session.Meta["inbound_endpoint"]))
	payload["user_agent"] = firstNonBlank(session.UserAgent, stringFromAny(payload["user_agent"]), stringFromAny(session.Meta["user_agent"]))
	payload["created_at"] = session.CreatedAt.Format(time.RFC3339Nano)
	if session.EndedAt != nil {
		payload["ended_at"] = session.EndedAt.Format(time.RFC3339Nano)
	}
	payload["status"] = session.Status
	payload["is_final_snapshot"] = session.IsFinalSnapshot
	payload["source_request_count"] = session.SourceRequestCount
	messages := firstNonEmptyMaps(session.Messages, mapsFromAny(payload["messages"]))
	tools := firstNonEmptyMaps(session.Tools, mapsFromAny(payload["tools"]))
	usage := firstNonEmptyMap(session.Usage, mapAnyFromAny(payload["usage"]))
	meta := firstNonEmptyMap(session.Meta, mapAnyFromAny(payload["meta"]))
	if !finalized {
		messages = CompactDataShareMessages(normalizeDataShareMessages(messages))
		tools = normalizeDataShareTools(tools)
		usage = normalizeDataShareUsage(usage)
		meta = normalizeDataShareMeta(meta)
	}
	systemPrompt := firstNonBlank(optionalStringValue(session.SystemPrompt), stringFromAny(payload["system_prompt"]), extractSystemPromptFromMessages(messages))
	payload["system_prompt"] = systemPrompt
	payload["tools"] = tools
	payload["messages"] = messages
	payload["usage"] = usage
	payload["meta"] = meta
	delete(payload, "quality_status")
	return payload
}

// PublicDataShareSessionPayload 返回对外可见 payload，剥离采集内部状态。
func PublicDataShareSessionPayload(payload map[string]any) map[string]any {
	out := cloneDataShareMap(payload)
	if meta := mapAnyFromAny(out["meta"]); meta != nil {
		out["meta"] = stripDataShareInternalCaptureStateFromMeta(meta)
	}
	return out
}

// PublicDataShareSessionMeta 返回对外可见 meta，剥离采集内部状态。
func PublicDataShareSessionMeta(meta map[string]any) map[string]any {
	return stripDataShareInternalCaptureStateFromMeta(meta)
}

// exportDownloadPayloadFromSession 仅用于下载导出，在保留库内原始采集数据的同时剔除敏感字段。
func exportDownloadPayloadFromSession(session *DataShareSession) (map[string]any, error) {
	payload, ok := redactDataShareExportFields(PublicDataShareSessionPayload(exportPayloadFromSession(session))).(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	if err := recheckDataShareExportPayload(payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func recheckDataShareExportPayload(payload map[string]any) error {
	messages := mapsFromAny(payload["messages"])
	if len(messages) == 0 {
		return nil
	}
	if dataShareHasReplayDuplicateBlock(messages) {
		return fmt.Errorf("%w: %s", ErrDataShareExportPayloadInvalid, dataShareQualityErrorReplayDuplicateBlock)
	}
	// 导出前再做一轮幂等性复核，兜住达到轮数上限后仍可继续收缩的阶梯 replay。
	if len(compactDataShareMessagesOnce(messages)) != len(messages) {
		return fmt.Errorf("%w: %s", ErrDataShareExportPayloadInvalid, dataShareQualityErrorReplayDuplicateBlock)
	}
	return nil
}

// BuildDataShareSessionPayload 生成可导出、可压缩持久化的规范 session payload。
func BuildDataShareSessionPayload(session *DataShareSession) map[string]any {
	return exportPayloadFromSession(session)
}

// BuildFinalizedDataShareSessionPayload 复用最终化阶段已规范化的字段，避免超大快照重复 compact。
func BuildFinalizedDataShareSessionPayload(session *DataShareSession) map[string]any {
	return exportPayloadFromFinalizedSession(session)
}

func normalizeDataShareProvider(provider string, apiKey *APIKey) string {
	provider = strings.TrimSpace(provider)
	if provider != "" {
		return provider
	}
	if apiKey != nil && apiKey.Group != nil {
		return apiKey.Group.Platform
	}
	return "unknown"
}

func normalizeDataShareRequestPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func normalizeDataShareUserAgent(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	// 统计维度只保留客户端产品名，避免版本号、系统架构把同一客户端打散成大量分组。
	if idx := strings.Index(userAgent, "/"); idx > 0 {
		userAgent = strings.TrimSpace(userAgent[:idx])
	}
	if len(userAgent) > 512 {
		return userAgent[:512]
	}
	return userAgent
}

func normalizeDataShareSessionID(sessionID string, requestID string, body []byte, apiKeyID int64) string {
	for _, candidate := range []string{
		sessionID,
		gjson.GetBytes(body, "session_id").String(),
		gjson.GetBytes(body, "conversation_id").String(),
		gjson.GetBytes(body, "metadata.session_id").String(),
		gjson.GetBytes(body, "metadata.conversation_id").String(),
		gjson.GetBytes(body, "metadata.prompt_cache_key").String(),
		gjson.GetBytes(body, "prompt_cache_key").String(),
		gjson.GetBytes(body, "metadata.user_id").String(),
		requestID,
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	sum := sha256.Sum256(append(body, []byte(strconv.FormatInt(apiKeyID, 10))...))
	return hex.EncodeToString(sum[:16])
}

func buildTrajectoryID(provider string, sessionID string, apiKeyID int64, groupID int64) string {
	seed := fmt.Sprintf("%s:%s:%d:%d", provider, sessionID, apiKeyID, groupID)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

func extractSystemPromptFromRequest(body []byte) string {
	sys := gjson.GetBytes(body, "system")
	if sys.Exists() {
		if sys.Type == gjson.String {
			return sys.String()
		}
		return sys.Raw
	}
	if sys = gjson.GetBytes(body, "system_instruction"); sys.Exists() {
		return sys.Raw
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func optionalStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstPresentAny(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func boolFromAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	default:
		return false
	}
}

func mapFromAny(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, true
	default:
		return nil, false
	}
}

func mapAnyFromAny(v any) map[string]any {
	if m, ok := mapFromAny(v); ok {
		return m
	}
	return nil
}

func mapsFromAny(v any) []map[string]any {
	switch x := v.(type) {
	case []map[string]any:
		return x
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := mapFromAny(item); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func anySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		out := make([]any, 0, len(x))
		for _, item := range x {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func stringsFromAny(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(x) == "" {
			return nil
		}
		return []string{x}
	default:
		return nil
	}
}

func appendStringValues(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(values))
	out := make([]string, 0, len(existing)+len(values))
	for _, item := range existing {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func cloneDataShareMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// redactDataShareExportFields 递归清理导出 payload 中的敏感字段，保留其它业务字段。
func redactDataShareExportFields(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if _, excluded := dataShareExportExcludedFields[key]; excluded {
				continue
			}
			out[key] = redactDataShareExportFields(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			redacted, _ := redactDataShareExportFields(item).(map[string]any)
			out = append(out, redacted)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, redactDataShareExportFields(item))
		}
		return out
	default:
		return value
	}
}

func firstNonEmptyMaps(values ...[]map[string]any) []map[string]any {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func firstNonEmptyMap(values ...map[string]any) map[string]any {
	for _, v := range values {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}

func rawJSONToMap(raw string) map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]any{"raw": raw}
	}
	return out
}

func rawJSONToAny(raw string) any {
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return raw
	}
	return out
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return 0
	}
}

// WriteSingleSessionJSONL 输出单条 session 的下载 JSONL，并剔除不允许外发的身份字段。
func WriteSingleSessionJSONL(w io.Writer, session *DataShareSession) error {
	if session == nil {
		return ErrDataShareSessionNotFound
	}
	var buf bytes.Buffer
	payload, err := exportDownloadPayloadFromSession(session)
	if err != nil {
		return err
	}
	line, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := buf.Write(line); err != nil {
		return err
	}
	if err := buf.WriteByte('\n'); err != nil {
		return err
	}
	_, err = w.Write(buf.Bytes())
	return err
}

func IsDataShareNotFound(err error) bool {
	return errors.Is(err, ErrDataShareSessionNotFound)
}

func cloneDataSharingRequestBody(body []byte) []byte {
	if len(body) == 0 {
		return nil
	}
	return append([]byte(nil), body...)
}
