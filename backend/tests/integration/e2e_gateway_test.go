//go:build e2e

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// e2e_gateway_test.go probes an already running deployment whose keys, allowed
// models and mappings are configured externally. Lists check response shape,
// generation probes check message or candidate envelopes, and streaming probes
// sample initial data lines; these observations do not identify the selected
// upstream model.

const (
	// claudeAPIKeyEnv selects the TokenRouter credential used by live
	// Claude-format gateway E2E requests against the configured deployment.
	claudeAPIKeyEnv = "CLAUDE_API_KEY"

	// geminiAPIKeyEnv selects the TokenRouter credential used by live
	// Gemini-format gateway E2E requests against the configured deployment.
	geminiAPIKeyEnv = "GEMINI_API_KEY"
)

var (
	// baseURL, endpointPrefix and testInterval select the deployment,
	// gateway path family and request spacing. The default address uses
	// local port 8080; spacing reduces pressure during sequential model
	// probes.
	baseURL = getEnv("BASE_URL", "http://localhost:8080")
	// endpointPrefix is concatenated before gateway paths. Empty selects
	// ordinary routes, while /antigravity enables the cross-family probes
	// that require that exact prefix.
	endpointPrefix = getEnv("ENDPOINT_PREFIX", "")
	testInterval   = 1 * time.Second // Delay between requests to reduce rate-limit pressure.

	// claudeModels supplies Messages request names that the chosen
	// deployment must admit or map for these acceptance probes; returned
	// model identity is not asserted.
	claudeModels = []string{
		// Opus fixtures.
		"claude-opus-4-5-thinking",
		"claude-opus-4",
		"claude-opus-4-5-20251101",
		// Sonnet fixtures.
		"claude-sonnet-4-5",
		"claude-sonnet-4-5-thinking",
		"claude-sonnet-4-5-20250929",
		"claude-3-5-sonnet-20241022",
		// Haiku request-name probes.
		"claude-haiku-4",
		"claude-haiku-4-5",
		"claude-haiku-4-5-20251001",
		"claude-3-haiku-20240307",
	}

	// geminiModels supplies GenerateContent request names for the chosen
	// deployment; success is checked through HTTP and response shape rather
	// than resolved upstream identity.
	geminiModels = []string{
		"gemini-2.5-flash",
		"gemini-2.5-flash-lite",
		"gemini-3-flash",
		"gemini-3-pro-low",
		"gemini-3-pro-high",
	}
)

// requireClaudeAPIKey reads and trims the Messages test credential, skipping
// the current test when it is empty. The returned value is sent as a Bearer
// credential to the configured TokenRouter deployment.
func requireClaudeAPIKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv(claudeAPIKeyEnv))
	if key == "" {
		t.Skipf("未设置 %s，跳过 Claude 相关 E2E 测试", claudeAPIKeyEnv)
	}
	return key
}

// requireGeminiAPIKey reads and trims the GenerateContent test credential,
// skipping the current test when it is empty. The returned value is sent as a
// Bearer credential to the configured TokenRouter deployment.
func requireGeminiAPIKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv(geminiAPIKeyEnv))
	if key == "" {
		t.Skipf("未设置 %s，跳过 Gemini 相关 E2E 测试", geminiAPIKeyEnv)
	}
	return key
}

// TestClaudeModelsList requests the configured Messages-family model list with
// a gateway key. It requires HTTP 200, object=list and an array-valued data
// field, then logs the array length.
func TestClaudeModelsList(t *testing.T) {
	claudeKey := requireClaudeAPIKey(t)
	url := baseURL + endpointPrefix + "/v1/models"

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+claudeKey)

	// Bound model-list reads when the deployment cannot finish its
	// response.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the list response after decoding or an early HTTP failure so the
	// model-array probe releases its body; cleanup discards close errors.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		// Retain bytes received for HTTP diagnostics; body-read errors
		// are discarded, with the received status or later envelope
		// check determining this probe's result.
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if result["object"] != "list" {
		t.Errorf("期望 object=list, 得到 %v", result["object"])
	}

	data, ok := result["data"].([]any)
	if !ok {
		t.Fatal("响应缺少 data 数组")
	}
	t.Logf("✅ 返回 %d 个模型", len(data))
}

// TestGeminiModelsList requests the configured Gemini-family model list with a
// gateway key. It requires HTTP 200 and an array-valued models field, leaving
// the specific deployment model set unconstrained.
func TestGeminiModelsList(t *testing.T) {
	geminiKey := requireGeminiAPIKey(t)
	url := baseURL + endpointPrefix + "/v1beta/models"

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+geminiKey)

	// Bound model-list reads when the deployment cannot finish its
	// response.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the response after inspecting the models array or failing on
	// status, releasing this list request's body; cleanup discards close errors.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		// Retain bytes received for HTTP diagnostics; body-read errors
		// are discarded, with the received status or later envelope
		// check determining this probe's result.
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	models, ok := result["models"].([]any)
	if !ok {
		t.Fatal("响应缺少 models 数组")
	}
	t.Logf("✅ 返回 %d 个模型", len(models))
}

// TestClaudeMessages sends each configured model name through both Messages
// response modes. Nonstreaming checks a message envelope; streaming requires
// initial SSE data after a successful HTTP response.
func TestClaudeMessages(t *testing.T) {
	claudeKey := requireClaudeAPIKey(t)
	for i, model := range claudeModels {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_非流式", func(t *testing.T) {
			testClaudeMessage(t, claudeKey, model, false)
		})
		// Separate nonstreaming and streaming probes with the
		// configured delay so each mode can report its own response
		// without an immediate burst of paired requests.
		time.Sleep(testInterval)
		t.Run(model+"_流式", func(t *testing.T) {
			testClaudeMessage(t, claudeKey, model, true)
		})
	}
}

// testClaudeMessage posts one short Messages prompt with the supplied gateway
// key and model. HTTP 200 is required; stream mode samples at most three data
// lines, while JSON mode checks type=message.
func testClaudeMessage(t *testing.T, claudeKey string, model string, stream bool) {
	url := baseURL + endpointPrefix + "/v1/messages"

	payload := map[string]any{
		"model":      model,
		"max_tokens": 50,
		"stream":     stream,
		"messages": []map[string]string{
			{"role": "user", "content": "Say 'hello' in one word."},
		},
	}
	// These literal payload values are supported by encoding/json.
	// Discarding the marshal error relies on that fixed fixture shape
	// before making the deployment request.
	body, _ := json.Marshal(payload)

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// Bound generation probes, including a stream that sends no initial
	// data.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the response when this model-mode probe exits, including an early
	// stop after its third raw data line; cleanup discards close errors.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		// Retain bytes received for HTTP diagnostics; body-read errors
		// are discarded, with the received status or later envelope
		// check determining this probe's result.
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if stream {
		// Limit the probe to three data lines so acceptance needs no
		// complete generation. Any positive count passes; terminal
		// events and scanner errors are outside this assertion.
		scanner := bufio.NewScanner(resp.Body)
		eventCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				eventCount++
				if eventCount >= 3 {
					break
				}
			}
		}
		if eventCount == 0 {
			t.Fatal("未收到任何 SSE 事件")
		}
		t.Logf("✅ 收到 %d+ 个 SSE 事件", eventCount)
	} else {
		// A non-streaming response must decode as a message.
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if result["type"] != "message" {
			t.Errorf("期望 type=message, 得到 %v", result["type"])
		}
		t.Logf("✅ 收到消息响应 id=%v", result["id"])
	}
}

// TestGeminiGenerateContent probes each configured Gemini name through
// generateContent and streamGenerateContent. Successful responses must expose
// candidates in JSON mode or initial SSE data in streaming mode.
func TestGeminiGenerateContent(t *testing.T) {
	geminiKey := requireGeminiAPIKey(t)
	for i, model := range geminiModels {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_非流式", func(t *testing.T) {
			testGeminiGenerate(t, geminiKey, model, false)
		})
		// Separate nonstreaming and streaming probes with the
		// configured delay so each mode can report its own response
		// without an immediate burst of paired requests.
		time.Sleep(testInterval)
		t.Run(model+"_流式", func(t *testing.T) {
			testGeminiGenerate(t, geminiKey, model, true)
		})
	}
}

// testGeminiGenerate chooses the generation action and SSE query from stream,
// then posts one short prompt. HTTP 200 is required; JSON checks candidates
// presence, and streaming checks initial data-line arrival.
func testGeminiGenerate(t *testing.T, geminiKey string, model string, stream bool) {
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	url := fmt.Sprintf("%s%s/v1beta/models/%s:%s", baseURL, endpointPrefix, model, action)
	// Gemini streaming requests select the SSE wire form through alt=sse so
	// the shared data-line assertion reads events rather than a JSON
	// response array.
	if stream {
		url += "?alt=sse"
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{"text": "Say 'hello' in one word."},
				},
			},
		},
		"generationConfig": map[string]int{
			"maxOutputTokens": 50,
		},
	}
	// These literal payload values are supported by encoding/json.
	// Discarding the marshal error relies on that fixed fixture shape
	// before making the deployment request.
	body, _ := json.Marshal(payload)

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+geminiKey)

	// Bound generation probes, including a stream that sends no initial
	// data.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close this generation response after the sampled SSE prefix or JSON
	// candidates check, releasing even partly read bodies; cleanup discards
	// close errors.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		// Retain bytes received for HTTP diagnostics; body-read errors
		// are discarded, with the received status or later envelope
		// check determining this probe's result.
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if stream {
		// Limit the probe to three data lines so acceptance needs no
		// complete generation. Any positive count passes; terminal
		// events and scanner errors are outside this assertion.
		scanner := bufio.NewScanner(resp.Body)
		eventCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				eventCount++
				if eventCount >= 3 {
					break
				}
			}
		}
		if eventCount == 0 {
			t.Fatal("未收到任何 SSE 事件")
		}
		t.Logf("✅ 收到 %d+ 个 SSE 事件", eventCount)
	} else {
		// A non-streaming response must contain candidates.
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if _, ok := result["candidates"]; !ok {
			t.Error("响应缺少 candidates 字段")
		}
		t.Log("✅ 收到 candidates 响应")
	}
}

// TestClaudeMessagesWithComplexTools submits nested schemas and schemas
// omitting selected declarations under two Messages model names. HTTP 200 must
// contain type=message; a received 429 or 503 skips that model's probe.
func TestClaudeMessagesWithComplexTools(t *testing.T) {
	claudeKey := requireClaudeAPIKey(t)
	// Probe both a dated Opus name and a Haiku name. The deployment
	// selects any required mapping, while this probe observes status and
	// the message envelope without inspecting the mapping decision.
	models := []string{
		"claude-opus-4-5-20251101",
		"claude-haiku-4-5-20251001",
	}

	for i, model := range models {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_复杂工具", func(t *testing.T) {
			testClaudeMessageWithTools(t, claudeKey, model)
		})
	}
}

// testClaudeMessageWithTools posts the supplied tool schema variants and
// checks the returned message envelope. HTTP 400 fails the scenario; 429 and
// 503 skip it, and other non-200 statuses fail.
func testClaudeMessageWithTools(t *testing.T, claudeKey string, model string) {
	url := baseURL + endpointPrefix + "/v1/messages"

	// Nested schema keywords and missing declarations probe how the chosen
	// deployment accepts tool definitions. The HTTP response supplies
	// acceptance evidence; the test does not capture the upstream schema.
	tools := []map[string]any{
		{
			"name":        "read_file",
			"description": "Read file contents",
			"input_schema": map[string]any{
				"$schema": "http://json-schema.org/draft-07/schema#",
				"type":    "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path",
						"minLength":   1,
						"maxLength":   4096,
						"pattern":     "^[^\\x00]+$",
					},
					"encoding": map[string]any{
						"type":    []string{"string", "null"},
						"default": "utf-8",
						"enum":    []string{"utf-8", "ascii", "latin-1"},
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "write_file",
			"description": "Write content to file",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":      "string",
						"minLength": 1,
					},
					"content": map[string]any{
						"type":      "string",
						"maxLength": 1048576,
					},
				},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
				"strict":               true,
			},
		},
		{
			"name":        "list_files",
			"description": "List files in directory",
			"input_schema": map[string]any{
				"$id":  "https://example.com/list-files.schema.json",
				"type": "object",
				"properties": map[string]any{
					"directory": map[string]any{
						"type": "string",
					},
					"patterns": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":      "string",
							"minLength": 1,
						},
						"minItems":    1,
						"maxItems":    100,
						"uniqueItems": true,
					},
					"recursive": map[string]any{
						"type":    "boolean",
						"default": false,
					},
				},
				"required":             []string{"directory"},
				"additionalProperties": false,
			},
		},
		{
			"name":        "search_code",
			"description": "Search code in files",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":      "string",
						"minLength": 1,
						"format":    "regex",
					},
					"max_results": map[string]any{
						"type":             "integer",
						"minimum":          1,
						"maximum":          1000,
						"exclusiveMinimum": 0,
						"default":          100,
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
				"examples": []map[string]any{
					{"query": "function.*test", "max_results": 50},
				},
			},
		},
		// Exercise a required name absent from properties.
		{
			"name":        "invalid_required_tool",
			"description": "Tool with invalid required field",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type": "string",
					},
				},
				// The required list mentions a field with no
				// declared property schema. This input probes
				// whether the deployment accepts that tool
				// definition before returning a message.
				"required": []string{"name", "nonexistent_field"},
			},
		},
		// Exercise an object schema with no properties.
		{
			"name":        "no_properties_tool",
			"description": "Tool without properties",
			"input_schema": map[string]any{
				"type":     "object",
				"required": []string{"should_be_removed"},
			},
		},
		// Supply properties with an omitted object type for the
		// acceptance probe.
		{
			"name":        "no_type_tool",
			"description": "Tool without type",
			"input_schema": map[string]any{
				"properties": map[string]any{
					"value": map[string]any{
						"type": "string",
					},
				},
			},
		},
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": 100,
		"stream":     false,
		"messages": []map[string]string{
			{"role": "user", "content": "List files in the current directory"},
		},
		"tools": tools,
	}
	// These literal payload values are supported by encoding/json.
	// Discarding the marshal error relies on that fixed fixture shape
	// before making the deployment request.
	body, _ := json.Marshal(payload)

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// The one-minute client budget bounds this nonstreaming request and its
	// response-body read before the message envelope is checked.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the tool-schema response after envelope checking or a 429/503
	// skip, releasing the body on every reached response path; cleanup
	// discards close errors.
	defer func() { _ = resp.Body.Close() }()

	// Retain bytes received for HTTP diagnostics; body-read errors are
	// discarded, with the received status or later envelope check
	// determining this probe's result.
	respBody, _ := io.ReadAll(resp.Body)

	// Treat HTTP 400 as failure of this tool-schema acceptance probe.
	if resp.StatusCode == 400 {
		t.Fatalf("Schema 清理失败，收到 400 错误: %s", string(respBody))
	}

	// HTTP 503 supplies no accepted message for this probe. Skip the
	// attempt while retaining the received status and response bytes in the
	// deployment test report.
	if resp.StatusCode == 503 {
		t.Skipf("提供商暂时不可用 (503): %s", string(respBody))
	}

	// HTTP 429 reports a rate-limited attempt, so the helper skips message
	// acceptance checks and retains the received status and response bytes
	// for diagnosis.
	if resp.StatusCode == 429 {
		t.Skipf("请求被限流 (429): %s", string(respBody))
	}

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if result["type"] != "message" {
		t.Errorf("期望 type=message, 得到 %v", result["type"])
	}
	t.Logf("✅ 复杂工具 schema 测试通过, id=%v", result["id"])
}

// TestClaudeMessagesWithThinkingAndTools sends thinking-enabled assistant
// tool history with an omitted signature and a matching tool_result. It checks
// HTTP 200 and type=message when the helper receives neither 429 nor 503.
func TestClaudeMessagesWithThinkingAndTools(t *testing.T) {
	claudeKey := requireClaudeAPIKey(t)
	models := []string{
		"claude-haiku-4-5-20251001",
	}
	for i, model := range models {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_thinking模式工具调用", func(t *testing.T) {
			testClaudeThinkingWithToolHistory(t, claudeKey, model)
		})
	}
}

// testClaudeThinkingWithToolHistory sends an unsigned tool call paired with its
// user result and enables thinking. The response must be a message at HTTP 200;
// 429 or 503 skip the probe.
func testClaudeThinkingWithToolHistory(t *testing.T, claudeKey string, model string) {
	url := baseURL + endpointPrefix + "/v1/messages"

	// The assistant tool_use and following tool_result share one ID, so
	// this probe sends a matched exchange with thinking enabled and no
	// signature field on the tool call.
	payload := map[string]any{
		"model":      model,
		"max_tokens": 200,
		"stream":     false,
		// Enable thinking for the continuation.
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 1024,
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "List files in the current directory",
			},
			// The assistant history contains an unsigned tool_use
			// block.
			map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type": "text",
						"text": "I'll list the files for you.",
					},
					{
						"type":  "tool_use",
						"id":    "toolu_01XGmNv",
						"name":  "Bash",
						"input": map[string]any{"command": "ls -la"},
						// Signature intentionally
						// omitted.
					},
				},
			},
			// Supply the result of the preceding tool call.
			map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": "toolu_01XGmNv",
						"content":     "file1.txt\nfile2.txt\ndir1/",
					},
				},
			},
		},
		"tools": []map[string]any{
			{
				"name":        "Bash",
				"description": "Execute bash commands",
				"input_schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type": "string",
						},
					},
					"required": []string{"command"},
				},
			},
		},
	}
	// These literal payload values are supported by encoding/json.
	// Discarding the marshal error relies on that fixed fixture shape
	// before making the deployment request.
	body, _ := json.Marshal(payload)

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// The one-minute client budget bounds this nonstreaming request and its
	// response-body read before the message envelope is checked.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the history-continuation response after the message check or a
	// status-triggered skip; its body is owned by this helper, which discards
	// close errors.
	defer func() { _ = resp.Body.Close() }()

	// Retain bytes received for HTTP diagnostics; body-read errors are
	// discarded, with the received status or later envelope check
	// determining this probe's result.
	respBody, _ := io.ReadAll(resp.Body)

	// Treat a bad request as a failure of this unsigned-tool-history
	// scenario.
	if resp.StatusCode == 400 {
		t.Fatalf("thought_signature 处理失败，收到 400 错误: %s", string(respBody))
	}

	// HTTP 503 supplies no accepted message for this probe. Skip the
	// attempt while retaining the received status and response bytes in the
	// deployment test report.
	if resp.StatusCode == 503 {
		t.Skipf("提供商暂时不可用 (503): %s", string(respBody))
	}

	// HTTP 429 reports a rate-limited attempt, so the helper skips message
	// acceptance checks and retains the received status and response bytes
	// for diagnosis.
	if resp.StatusCode == 429 {
		t.Skipf("请求被限流 (429): %s", string(respBody))
	}

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if result["type"] != "message" {
		t.Errorf("期望 type=message, 得到 %v", result["type"])
	}
	t.Logf("✅ thinking 模式工具调用测试通过, id=%v", result["id"])
}

// TestClaudeMessagesWithGeminiModel probes Gemini request names through the
// Antigravity Messages route in both response modes. The exact /antigravity
// prefix is required, with envelope or initial stream-data assertions delegated
// to the common helper.
func TestClaudeMessagesWithGeminiModel(t *testing.T) {
	if endpointPrefix != "/antigravity" {
		t.Skip("仅在 Antigravity 模式下运行")
	}
	claudeKey := requireClaudeAPIKey(t)

	// Gemini names sent through the Messages endpoint.
	geminiViaClaude := []string{
		"gemini-3-flash",
		"gemini-3-pro-low",
		"gemini-3-pro-high",
		"gemini-3-pro",
		"gemini-3-pro-preview",
	}

	for i, model := range geminiViaClaude {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_通过Claude端点", func(t *testing.T) {
			testClaudeMessage(t, claudeKey, model, false)
		})
		// Separate nonstreaming and streaming probes with the
		// configured delay so each mode can report its own response
		// without an immediate burst of paired requests.
		time.Sleep(testInterval)
		t.Run(model+"_通过Claude端点_流式", func(t *testing.T) {
			testClaudeMessage(t, claudeKey, model, true)
		})
	}
}

// TestClaudeMessagesWithNoSignature continues an assistant thinking block
// whose signature is absent. HTTP 200 must contain type=message; received 429
// or 503 responses skip the model's acceptance probe.
func TestClaudeMessagesWithNoSignature(t *testing.T) {
	claudeKey := requireClaudeAPIKey(t)
	models := []string{
		"claude-haiku-4-5-20251001",
	}
	for i, model := range models {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_无signature", func(t *testing.T) {
			testClaudeWithNoSignature(t, claudeKey, model)
		})
	}
}

// testClaudeWithNoSignature posts unsigned thinking history and checks HTTP
// acceptance plus type=message. HTTP 400 fails the scenario, 429 and 503 skip
// it, and remaining non-200 responses fail.
func testClaudeWithNoSignature(t *testing.T, claudeKey string, model string) {
	url := baseURL + endpointPrefix + "/v1/messages"

	// Omit signature metadata from the earlier thinking block, then ask
	// another question. HTTP status and the returned message envelope
	// observe acceptance of that supplied continuation on this deployment.
	payload := map[string]any{
		"model":      model,
		"max_tokens": 200,
		"stream":     false,
		// Enable thinking for the continuation.
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 1024,
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "What is 2+2?",
			},
			// The assistant history contains an unsigned thinking
			// block.
			map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{
						"type":     "thinking",
						"thinking": "Let me calculate 2+2...",
						// Signature intentionally
						// omitted.
					},
					{
						"type": "text",
						"text": "2+2 equals 4.",
					},
				},
			},
			map[string]any{
				"role":    "user",
				"content": "What is 3+3?",
			},
		},
	}
	// These literal payload values are supported by encoding/json.
	// Discarding the marshal error relies on that fixed fixture shape
	// before making the deployment request.
	body, _ := json.Marshal(payload)

	// The fixture assumes the configured address and gateway path form a
	// valid URL. Request-construction errors are discarded before headers
	// are assigned, so malformed configuration can panic.
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+claudeKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	// The one-minute client budget bounds this nonstreaming request and its
	// response-body read before the message envelope is checked.
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	// Close the unsigned-thinking response after envelope checking or a
	// status-triggered skip so this probe releases its body; cleanup discards
	// close errors.
	defer func() { _ = resp.Body.Close() }()

	// Retain bytes received for HTTP diagnostics; body-read errors are
	// discarded, with the received status or later envelope check
	// determining this probe's result.
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 400 {
		t.Fatalf("无 signature thinking 处理失败，收到 400 错误: %s", string(respBody))
	}

	// HTTP 503 supplies no accepted message for this probe. Skip the
	// attempt while retaining the received status and response bytes in the
	// deployment test report.
	if resp.StatusCode == 503 {
		t.Skipf("提供商暂时不可用 (503): %s", string(respBody))
	}

	// HTTP 429 reports a rate-limited attempt, so the helper skips message
	// acceptance checks and retains the received status and response bytes
	// for diagnosis.
	if resp.StatusCode == 429 {
		t.Skipf("请求被限流 (429): %s", string(respBody))
	}

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	if result["type"] != "message" {
		t.Errorf("期望 type=message, 得到 %v", result["type"])
	}
	t.Logf("✅ 无 signature thinking 处理测试通过, id=%v", result["id"])
}

// TestGeminiEndpointWithClaudeModel probes Claude request names through the
// Antigravity Gemini route in both response modes. The exact /antigravity
// prefix is required; candidates presence and initial stream data are checked
// separately.
func TestGeminiEndpointWithClaudeModel(t *testing.T) {
	if endpointPrefix != "/antigravity" {
		t.Skip("仅在 Antigravity 模式下运行")
	}
	geminiKey := requireGeminiAPIKey(t)

	// Claude names sent through the Gemini endpoint.
	claudeViaGemini := []string{
		"claude-sonnet-4-5",
		"claude-opus-4-5-thinking",
	}

	for i, model := range claudeViaGemini {
		if i > 0 {
			// Space model probes so a preceding request does not
			// immediately add pressure to the deployment's
			// credential or provider request limits.
			time.Sleep(testInterval)
		}
		t.Run(model+"_通过Gemini端点", func(t *testing.T) {
			testGeminiGenerate(t, geminiKey, model, false)
		})
		// Separate nonstreaming and streaming probes with the
		// configured delay so each mode can report its own response
		// without an immediate burst of paired requests.
		time.Sleep(testInterval)
		t.Run(model+"_通过Gemini端点_流式", func(t *testing.T) {
			testGeminiGenerate(t, geminiKey, model, true)
		})
	}
}
