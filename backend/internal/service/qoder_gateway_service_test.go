//nolint:errcheck // Qoder gateway tests assert decoded fixture shapes with single-value type assertions.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const qoderXMLToolCallFixture = `<tool_call>Read<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>`
const qoderJSONShellToolCallFixture = `<tool_call>{"name":"shell","arguments":{"command":"pwd","description":"Print working directory"}}</tool_call>`
const qoderDSMLToolCallFixture = `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="Bash">
<｜｜DSML｜｜parameter name="command" string="true">ls -la</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="description" string="true">List root files</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

func qoderNoIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`},
		{Type: "tool_call_delta", ToolName: "Bash", Arguments: `{"command":"ls -la","description":"List files in current directory"}`},
		{Type: "tool_call_delta", ToolName: "glob", Arguments: `{"pattern":"**/*.md"}`},
		{IsDone: true},
	}
}

func qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t *testing.T) string {
	t.Helper()
	return qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`}},
			map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"ls -la","description":"List files in current directory"}`}},
			map[string]any{"type": "function", "function": map[string]any{"name": "glob", "arguments": `{"pattern":"**/*.md"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
}

func qoderRepeatedIndexNamedParallelToolCallEventsForTest() []qoder.SSEEvent {
	return []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"pwd","description":"Print working directory"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "Bash", Arguments: `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolName: "glob", Arguments: `{"pattern":"docs/*.md"}`},
		{IsDone: true},
	}
}

func qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest(t *testing.T) string {
	t.Helper()
	return qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd","description":"Print working directory"}`}},
		}}},
	}}) +
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`}},
			}}},
		}}) +
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "type": "function", "function": map[string]any{"name": "glob", "arguments": `{"pattern":"docs/*.md"}`}},
			}}},
		}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
}

var qoderCachedUsageEventForTest = qoder.SSEEvent{
	Type:             "usage",
	PromptTokens:     66637,
	CompletionTokens: 6,
	TotalTokens:      66643,
	UsageDetails: qoder.UsageDetails{
		PromptTokensDetails:     &qoder.PromptTokensDetails{CachedTokens: 66612, CacheableTokens: 19},
		CompletionTokensDetails: &qoder.CompletionTokensDetails{ReasoningTokens: 0},
	},
	HasUsage: true,
}

const qoderCachedUsageSSEForTest = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":66637,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":66643,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":66612,\\\"cacheable_tokens\\\":19},\\\"completion_tokens_details\\\":{\\\"reasoning_tokens\\\":0}}}\"}\n\n"

type qoderRateLimitRepoStub struct {
	stubOpenAIAccountRepo
	rateLimitedID int64
	resetAt       time.Time
}

func (r *qoderRateLimitRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.resetAt = resetAt
	return nil
}

type qoderRefreshAccountRepoStub struct {
	stubOpenAIAccountRepo
	updatedCredentials map[string]any
	updateCalls        int
}

func (r *qoderRefreshAccountRepoStub) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updatedCredentials = cloneCredentials(credentials)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Credentials = cloneCredentials(credentials)
			return nil
		}
	}
	return nil
}

type qoderRefreshRaceRepoStub struct {
	qoderRefreshAccountRepoStub
	raceAccount  *Account
	getByIDCalls int
}

func (r *qoderRefreshRaceRepoStub) GetByID(ctx context.Context, id int64) (*Account, error) {
	r.getByIDCalls++
	if r.getByIDCalls > 1 && r.raceAccount != nil {
		return r.raceAccount, nil
	}
	return r.stubOpenAIAccountRepo.GetByID(ctx, id)
}

type qoderRefreshLockCacheStub struct{}

func (qoderRefreshLockCacheStub) GetAccessToken(context.Context, string) (string, error) {
	return "", nil
}

func (qoderRefreshLockCacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (qoderRefreshLockCacheStub) DeleteAccessToken(context.Context, string) error {
	return nil
}

func (qoderRefreshLockCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func (qoderRefreshLockCacheStub) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func TestBuildQoderPayloadFromChatCompletions(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":123,
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":[{"type":"text","text":"hello"}]},
			{"role":"assistant","content":"hi"},
			{"role":"tool","tool_call_id":"call_1","content":"tool output"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "auto", modelKey)
	require.Equal(t, true, payload["stream"])
	require.Equal(t, "personal_standard", payload["aliyun_user_type"])
	parameters, _ := payload["parameters"].(map[string]any)
	modelConfig, _ := payload["model_config"].(map[string]any)
	chatContext, _ := payload["chat_context"].(map[string]any)
	chatText, _ := chatContext["text"].(map[string]any)
	require.Equal(t, 123, parameters["max_tokens"])
	require.Equal(t, "auto", modelConfig["key"])
	require.Equal(t, "hello", chatText["text"])

	messagesRaw, ok := payload["messages"].([]any)
	require.True(t, ok)
	messages := messagesRaw
	require.Len(t, messages, 4)
	firstMsg, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "system", firstMsg["role"])
	require.Equal(t, "be terse", firstMsg["content"])
	secondMsg, ok := messages[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user", secondMsg["role"])
	require.Equal(t, "", secondMsg["content"])
	userContents, ok := secondMsg["contents"].([]any)
	require.True(t, ok)
	firstContent, ok := userContents[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "hello", firstContent["text"])
	lastMsg, ok := messages[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool", lastMsg["role"])
	require.Equal(t, "tool output", lastMsg["content"])
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
}

func TestBuildQoderPayloadUserSystemReplacesBuiltInSystem(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"custom system"},
			{"role":"user","content":"hello"}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	systemMessages := 0
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		require.True(t, ok)
		if msg["role"] == "system" {
			systemMessages++
			require.Equal(t, "custom system", msg["content"])
		}
	}
	require.Equal(t, 1, systemMessages)
}

func TestBuildQoderPayloadFromChatCompletionsPreservesToolHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"run ls"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"ls"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"file.txt"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	assistant := messages[1].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "call_1", toolCall["id"])
	require.Equal(t, "function", toolCall["type"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, function["arguments"].(string))

	tool := messages[2].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
}

func TestBuildQoderPayloadFromChatCompletionsMergesParallelToolHistory(t *testing.T) {
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[
			{"role":"user","content":"run both commands"},
			{"role":"assistant","reasoning_content":"Need two shell checks.","content":"","tool_calls":[
				{"id":"call_a","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf a\\n\"}"}},
				{"id":"call_b","type":"function","function":{"name":"bash","arguments":"{\"command\":\"printf b\\n\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_a","content":"a\n"},
			{"role":"tool","tool_call_id":"call_b","content":"b\n"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}]
	}`)

	payload, _, err := BuildQoderPayloadFromChatCompletions(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	require.Len(t, messages, 4)
	assistant := messages[1].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.Empty(t, assistant["contents"], "DeepSeek reasoning_content must not be replayed as visible assistant text before tool_calls")
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 2)
	require.Equal(t, "call_a", toolCalls[0].(map[string]any)["id"])
	require.Equal(t, "call_b", toolCalls[1].(map[string]any)["id"])

	firstTool := messages[2].(map[string]any)
	require.Equal(t, "tool", firstTool["role"])
	require.Equal(t, "call_a", firstTool["tool_call_id"])
	require.Equal(t, "call_a", firstTool["tool_call_call_id"])
	require.Equal(t, "bash", firstTool["name"])
	secondTool := messages[3].(map[string]any)
	require.Equal(t, "tool", secondTool["role"])
	require.Equal(t, "call_b", secondTool["tool_call_id"])
	require.Equal(t, "call_b", secondTool["tool_call_call_id"])
	require.Equal(t, "bash", secondTool["name"])
}

func TestBuildQoderPayloadAddsCacheControlToLastEligibleTextBlock(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"text","text":"first","cache_control":{"type":"ephemeral"}},{"type":"tool_use","id":"ignored","name":"bash","input":{}},{"type":"thinking","thinking":"ignore"}]},
			{"role":"assistant","content":[{"type":"redacted_thinking","data":"ignore"},{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"},{"type":"text","text":"last"}]}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	messages := payload["messages"].([]any)
	firstContents := messages[0].(map[string]any)["contents"].([]any)
	require.Equal(t, "ephemeral", firstContents[0].(map[string]any)["cache_control"].(map[string]any)["type"])
	lastContents := messages[len(messages)-1].(map[string]any)["contents"].([]any)
	lastText := lastContents[len(lastContents)-1].(map[string]any)
	require.Equal(t, "last", lastText["text"])
	require.Equal(t, "ephemeral", lastText["cache_control"].(map[string]any)["type"])
	for _, rawMessage := range messages {
		for _, rawBlock := range rawMessage.(map[string]any)["contents"].([]any) {
			block := rawBlock.(map[string]any)
			if block["type"] != "text" {
				require.NotContains(t, block, "cache_control")
			}
		}
	}
}

func TestBuildQoderPayloadFromAnthropicMessages(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"max_tokens":456,
		"system":[{"type":"text","text":"system one"},{"type":"text","text":"system two"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"tool result"}]}]}
		]
	}`)

	payload, modelKey, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)
	require.Equal(t, "ultimate", modelKey)
	require.Equal(t, 456, payload["parameters"].(map[string]any)["max_tokens"])
	require.Equal(t, "ultimate", payload["model_config"].(map[string]any)["key"])

	messages := payload["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "system one\nsystem two", messages[0].(map[string]any)["content"])
	require.Equal(t, "", messages[1].(map[string]any)["content"])
	userContents := messages[1].(map[string]any)["contents"].([]any)
	require.Equal(t, "hello", userContents[0].(map[string]any)["text"])
	toolResult := messages[2].(map[string]any)
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "t1", toolResult["tool_call_id"])
	require.Equal(t, "tool result", toolResult["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesPreservesToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"ls"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"file.txt"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	assistant := messages[0].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "call_1", toolCall["id"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"cmd":"ls"}`, function["arguments"].(string))

	tool := messages[1].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	require.Equal(t, "file.txt", tool["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesIgnoresThinkingToolUseHistory(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-4-6",
		"messages":[
			{"role":"user","content":"inspect files"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"need to inspect","signature":"sig"},
				{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"README.md"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"contents"}
			]}
		],
		"tools":[{"name":"Read","input_schema":{"type":"object"}}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	require.Len(t, messages, 3)
	assistant := messages[1].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.Equal(t, "", assistant["content"])
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "toolu_1", toolCall["id"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "Read", function["name"])
	require.JSONEq(t, `{"file_path":"README.md"}`, function["arguments"].(string))
	toolResult := messages[2].(map[string]any)
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "toolu_1", toolResult["tool_call_id"])
	require.Equal(t, "toolu_1", toolResult["tool_call_call_id"])
	require.Equal(t, "Read", toolResult["name"])
	require.Equal(t, "contents", toolResult["content"])
}

func TestBuildQoderPayloadFromAnthropicMessagesDoesNotInventMissingToolResultID(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":[{"type":"tool_result","content":"file.txt"}]}
		]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	messages := payload["messages"].([]any)
	require.Len(t, messages, 1)
	tool := messages[0].(map[string]any)
	require.Equal(t, "user", tool["role"])
	require.NotContains(t, tool, "tool_calls")
	require.NotContains(t, tool, "tool_call_id")
}

func TestBuildQoderPayloadFromAnthropicMessagesConvertsTools(t *testing.T) {
	body := []byte(`{
		"model":"auto",
		"max_tokens":456,
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{
			"name":"bash",
			"description":"run command",
			"input_schema":{
				"type":"object",
				"properties":{"cmd":{"type":"string"}},
				"required":["cmd"]
			}
		}]
	}`)

	payload, _, err := BuildQoderPayloadFromAnthropicMessages(body, "personal_standard")
	require.NoError(t, err)

	tools := payload["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	require.NotContains(t, tool, "input_schema")
	function := tool["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.Equal(t, "run command", function["description"])
	parameters := function["parameters"].(map[string]any)
	require.Equal(t, "object", parameters["type"])
	require.Contains(t, parameters, "properties")
}

func TestQoderConversationKeyPrefersExplicitSessionOverClaudeCodeStableSeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")

	request := qoderPayloadRequest{
		model:    "deepseek-v4-pro",
		messages: []qoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoderConversationKey(c, &Account{ID: 7}, "anthropic_messages", request)

	require.Equal(t, "header", source)
	require.Equal(t, "header:"+isolateOpenAISessionID(0, "header-session"), key)
	require.NotContains(t, key, "stable_seed")
}

func TestQoderConversationKeyPrefersMetadataOverClaudeCodeStableSeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")

	request := qoderPayloadRequest{
		model:          "deepseek-v4-pro",
		metadataUserID: FormatMetadataUserID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "session-123", "2.1.80"),
		messages:       []qoderMessage{{Role: "user", Text: "inspect"}},
	}

	key, source := qoderConversationKey(c, &Account{ID: 7}, "anthropic_messages", request)

	require.Equal(t, "metadata_user_id", source)
	require.Equal(t, "metadata_user_id:"+isolateOpenAISessionID(0, "session-123"), key)
	require.NotContains(t, key, "stable_seed")
}

func TestResolveQoderModelUsesOpus46AliasForUltimate(t *testing.T) {
	resetQoderModelAliasesForTest()
	t.Cleanup(resetQoderModelAliasesForTest)

	info := resolveQoderModel("claude-opus-4-6")
	require.Equal(t, "ultimate", info.Key)
	require.Equal(t, "system", info.Source)

	legacy := resolveQoderModel("claude-opus-4-5")
	require.Equal(t, "claude-opus-4-5", legacy.Key)

	codex := resolveQoderModel("gpt-5-codex")
	require.Equal(t, "gpt-5-codex", codex.Key)
}

func TestQoderGatewayAppliesAccountModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &Account{
		ID:       88,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
			"model_whitelist": []any{},
		},
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &QoderGatewayService{
		tokenProvider: &QoderTokenProvider{},
		client:        client,
	}
	svc.tokenProvider.sessions = map[int64]qoderSessionCacheEntry{
		account.ID: {
			credentialsHash: qoderCredentialsHash(account.Credentials),
			session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}],"stream":true}`)

	result, err := svc.ForwardChatCompletions(context.Background(), c, account, body)

	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", result.Model)
	require.Equal(t, "ultimate", result.UpstreamModel)
	require.Equal(t, "ultimate", client.headers["x-model-key"])
	require.Contains(t, rec.Body.String(), `"model":"claude-opus-4-6"`)
}

func TestQoderGatewayChatCompletionsReusesSessionAndSendsFullReplay(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()

	first := qoderForwardChatCompletionsForTest(t, svc, account, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "stable-chat-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be terse"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.Len(t, client.bodies, 2)
	require.Equal(t, first["session_id"], second["session_id"])
	firstMessages := first["messages"].([]any)
	require.Len(t, firstMessages, 2)
	require.Equal(t, "system", firstMessages[0].(map[string]any)["role"])

	secondMessages := second["messages"].([]any)
	require.Len(t, secondMessages, 4)
	require.Equal(t, "system", secondMessages[0].(map[string]any)["role"])
	require.Equal(t, "user", secondMessages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", secondMessages[2].(map[string]any)["role"])
	require.Equal(t, "user", secondMessages[3].(map[string]any)["role"])
	require.Equal(t, "next", second["chat_context"].(map[string]any)["text"].(map[string]any)["text"])
}

func TestQoderGatewayChatCompletionsWithoutSessionDoesNotReuseByFirstText(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()

	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"how many turns?"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := second["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "hello", qoderPayloadMessageTextForTest(messages[0].(map[string]any)))
}

func TestQoderGatewayChatCompletionsMapsUpstreamToolNameToDeclaredOpenAITool(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}],
		"stream":false
	}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_calls", gjson.Get(response, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.Get(response, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := upstream["tools"].([]any)
	require.Equal(t, "bash", tools[0].(map[string]any)["function"].(map[string]any)["name"])
}

func TestQoderGatewayMessagesMapsUpstreamToolNameToDeclaredAnthropicTool(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"name":"bash","input_schema":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body)

	require.False(t, result.Stream)
	require.Equal(t, "tool_use", gjson.Get(response, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.Get(response, "content.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "content.0.name").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := upstream["tools"].([]any)
	require.Equal(t, "bash", tools[0].(map[string]any)["function"].(map[string]any)["name"])
}

func TestQoderGatewayResponsesMapsUpstreamToolNameToDeclaredFunctionCall(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":false
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, account, body)

	require.False(t, result.Stream)
	require.Equal(t, "response", gjson.Get(response, "object").String())
	require.Equal(t, "function_call", gjson.Get(response, "output.0.type").String())
	require.Equal(t, "bash", gjson.Get(response, "output.0.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.Get(response, "output.0.arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
	upstream := qoderLastUpstreamPayloadForTest(t, client)
	tools := upstream["tools"].([]any)
	require.Equal(t, "bash", tools[0].(map[string]any)["function"].(map[string]any)["name"])
}

func TestQoderResponsesPayloadPreservesControlRoleInputAsSystemPrompt(t *testing.T) {
	request, err := parseQoderResponsesPayload([]byte(`{
		"model":"deepseek-v4-pro",
		"instructions":"top-level instructions",
		"input":[
			{"type":"message","role":"system","content":[{"type":"input_text","text":"system item"}]},
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer item"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		]
	}`))

	require.NoError(t, err)
	require.Contains(t, request.system, "top-level instructions")
	require.Contains(t, request.system, "system item")
	require.Contains(t, request.system, "developer item")
	require.Len(t, request.messages, 1)
	require.Equal(t, "user", request.messages[0].Role)
	require.Equal(t, "hello", request.messages[0].Text)
}

func TestQoderGatewayAssemblesResponsesKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := BuildQoderResponsesResponse("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	functionCalls := gjson.GetBytes(body, `output.#(type=="function_call")#`).Array()
	require.Len(t, functionCalls, 3, string(body))
	require.Equal(t, "Bash", functionCalls[0].Get("name").String())
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, functionCalls[0].Get("arguments").String())
	require.Equal(t, "Bash", functionCalls[1].Get("name").String())
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, functionCalls[1].Get("arguments").String())
	require.Equal(t, "glob", functionCalls[2].Get("name").String())
	require.JSONEq(t, `{"pattern":"**/*.md"}`, functionCalls[2].Get("arguments").String())
	for _, call := range functionCalls {
		require.NotContains(t, call.Get("arguments").String(), `}{`)
	}
}

func TestQoderGatewayAssemblesResponsesKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {
	body, err := BuildQoderResponsesResponse("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	functionCalls := gjson.GetBytes(body, `output.#(type=="function_call")#`).Array()
	require.Len(t, functionCalls, 3, string(body))
	require.Equal(t, "Bash", functionCalls[0].Get("name").String())
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, functionCalls[0].Get("arguments").String())
	require.Equal(t, "Bash", functionCalls[1].Get("name").String())
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, functionCalls[1].Get("arguments").String())
	require.Equal(t, "glob", functionCalls[2].Get("name").String())
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, functionCalls[2].Get("arguments").String())
	for _, call := range functionCalls {
		require.NotContains(t, call.Get("arguments").String(), `}{`)
	}
}

func TestQoderGatewayResponsesStreamsDeclaredFunctionCallEvents(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
		map[string]any{"delta": map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
		}}},
	}}) +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}],
		"stream":true
	}`)

	result, response := qoderForwardResponsesResultAndBodyForTest(t, svc, account, body)

	require.True(t, result.Stream)
	events := qoderResponsesStreamEventsForTest(t, response)
	require.Equal(t, "response.created", events[0].Get("type").String())
	var added gjson.Result
	var argsDone gjson.Result
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				added = event
			}
		case "response.function_call_arguments.done":
			argsDone = event
		}
	}
	require.Equal(t, "bash", added.Get("item.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, argsDone.Get("arguments").String())
	require.NotContains(t, response, `"name":"Bash"`)
}

func TestQoderGatewayResponsesStreamCompletedOutputIncludesTextMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"content": "Hello "}},
		}}) +
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "world"}},
			}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := WriteQoderResponsesStreamResponse(context.Background(), c, "deepseek-v4-pro", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Len(t, completed.Get("response.output").Array(), 1)
	require.Equal(t, "message", completed.Get("response.output.0.type").String())
	require.Equal(t, "assistant", completed.Get("response.output.0.role").String())
	require.Equal(t, "completed", completed.Get("response.output.0.status").String())
	require.Equal(t, "output_text", completed.Get("response.output.0.content.0.type").String())
	require.Equal(t, "Hello world", completed.Get("response.output.0.content.0.text").String())
}

func TestQoderGatewayResponsesStreamCompletedOutputIncludesFunctionCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{Body: io.NopCloser(bytes.NewBufferString(
		qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
			map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
			}}},
		}}) +
			"data: {\"body\":\"[DONE]\"}\n\n"))}

	result, err := WriteQoderResponsesStreamResponse(context.Background(), c, "deepseek-v4-pro", resp, qoderResponsesStreamToolNameMapper(qoderDeclaredToolNameMapper([]any{map[string]any{"type": "function", "name": "bash"}})))
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	completed := qoderResponsesCompletedEventForTest(t, rec.Body.String())
	require.Len(t, completed.Get("response.output").Array(), 1)
	require.Equal(t, "function_call", completed.Get("response.output.0.type").String())
	require.Equal(t, "call_1", completed.Get("response.output.0.call_id").String())
	require.Equal(t, "bash", completed.Get("response.output.0.name").String())
	require.Equal(t, "completed", completed.Get("response.output.0.status").String())
	require.JSONEq(t, `{"command":"pwd"}`, completed.Get("response.output.0.arguments").String())
}

func TestQoderGatewayResponsesToolContinuationUsesToolResultsAsPrompt(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}]`
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[{"role":"user","content":"run two commands"}],
		"tools":` + tools + `,
		"stream":true
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"run two commands"},
			{"type":"function_call","call_id":"call_a","name":"bash","arguments":"{\"command\":\"printf A\"}"},
			{"type":"function_call","call_id":"call_b","name":"bash","arguments":"{\"command\":\"printf B\"}"},
			{"type":"function_call_output","call_id":"call_a","output":"A\n"},
			{"type":"function_call_output","call_id":"call_b","output":"B\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, firstBody, qoderHeader("session_id", "responses-tool-continuation"))
	qoderForwardResponsesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("session_id", "responses-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "run two commands", prompt)
	require.Contains(t, prompt, `<tool_result id="call_a">`)
	require.Contains(t, prompt, "A\n")
	require.Contains(t, prompt, `<tool_result id="call_b">`)
	require.Contains(t, prompt, "B\n")
}

func TestQoderGatewayResponsesToolContinuationGroupsFunctionCallsIntoOneAssistantTurn(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	tools := `[{"type":"function","name":"bash","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}},{"type":"function","name":"glob","parameters":{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}}]`
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"input":[
			{"role":"user","content":"list docs and print marker"},
			{"type":"function_call","call_id":"call_glob","name":"glob","arguments":"{\"pattern\":\"docs/*.md\"}"},
			{"type":"function_call","call_id":"call_bash","name":"bash","arguments":"{\"command\":\"echo CHAIN_OPENAI_OK\"}"},
			{"type":"function_call_output","call_id":"call_glob","output":"docs/a.md\ndocs/b.md"},
			{"type":"function_call_output","call_id":"call_bash","output":"CHAIN_OPENAI_OK\n"}
		],
		"tools":` + tools + `,
		"stream":true
	}`)

	qoderForwardResponsesResultAndBodyForTest(t, svc, account, body, qoderHeader("session_id", "responses-grouped-tool-continuation"))

	payload := qoderLastUpstreamPayloadForTest(t, client)
	messages := payload["messages"].([]any)
	require.Len(t, messages, 4)
	assistant := messages[1].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, assistant["tool_calls"].([]any), 2)

	prompt := qoderPayloadPromptForTest(t, payload)
	require.NotEqual(t, "list docs and print marker", prompt)
	require.Contains(t, prompt, `<tool_result id="call_glob">`)
	require.Contains(t, prompt, "docs/a.md")
	require.Contains(t, prompt, `<tool_result id="call_bash">`)
	require.Contains(t, prompt, "CHAIN_OPENAI_OK")
}

func TestQoderGatewayWritesResponsesStreamKeepsNoIndexNamedParallelFunctionCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := WriteQoderResponsesStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	addedNames := make([]string, 0)
	argsDone := make([]string, 0)
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				addedNames = append(addedNames, event.Get("item.name").String())
			}
		case "response.function_call_arguments.done":
			arguments := event.Get("arguments").String()
			require.NotContains(t, arguments, `}{`)
			argsDone = append(argsDone, arguments)
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, addedNames)
	require.Len(t, argsDone, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, argsDone[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, argsDone[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, argsDone[2])
}

func TestQoderGatewayWritesResponsesStreamKeepsRepeatedIndexNamedParallelFunctionCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderRepeatedIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := WriteQoderResponsesStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	addedNames := make([]string, 0)
	argsDone := make([]string, 0)
	for _, event := range events {
		switch event.Get("type").String() {
		case "response.output_item.added":
			if event.Get("item.type").String() == "function_call" {
				addedNames = append(addedNames, event.Get("item.name").String())
			}
		case "response.function_call_arguments.done":
			arguments := event.Get("arguments").String()
			require.NotContains(t, arguments, `}{`)
			argsDone = append(argsDone, arguments)
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, addedNames)
	require.Len(t, argsDone, 3)
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, argsDone[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, argsDone[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, argsDone[2])
}

func TestQoderGatewayWritesResponsesStreamDoesNotReserveOutputIndexForTypeOnlyPlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"type": "function"},
				}}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"tool_calls": []any{
						map[string]any{"type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
					}}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n")),
	}

	result, err := WriteQoderResponsesStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	events := qoderResponsesStreamEventsForTest(t, rec.Body.String())
	for _, event := range events {
		if event.Get("type").String() != "response.output_item.added" || event.Get("item.type").String() != "function_call" {
			continue
		}
		require.Equal(t, int64(0), event.Get("output_index").Int(), event.Raw)
		require.Equal(t, "Bash", event.Get("item.name").String())
		return
	}
	t.Fatalf("function_call output_item.added not found in %s", rec.Body.String())
}

func TestQoderGatewayClaudeRequestsWithoutSessionDoNotReuseByFirstText(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你一共和我对话了几句话？"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	require.Len(t, second["messages"].([]any), 4)
}

func TestQoderGatewayClaudeCodeContextWithoutSessionUsesStablePrefixKey(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are Claude Code, Anthropic's official CLI for Claude.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"claude-opus-4-6",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`),
		qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"),
		qoderHeader("X-Test-Claude-Code-Context", "true"),
	)

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])
	require.Equal(t, "continue", second["chat_context"].(map[string]any)["text"].(map[string]any)["text"])
}

func TestQoderGatewayDoesNotCommitConversationOnUpstreamFailure(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.err = errors.New("upstream failed")
	body := []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	_, err := svc.ForwardChatCompletions(context.Background(), c, account, body)
	require.Error(t, err)

	client.err = nil
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", body)
	require.Len(t, first["messages"].([]any), 1)
}

func TestQoderGatewayReservesConversationAfterUpstreamAcceptsBeforeStreamCompletes(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	client := newBlockingQoderClientStub(t)
	svc.client = client
	body1 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"reserve-before-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	gin.SetMode(gin.TestMode)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = svc.ForwardMessages(context.Background(), firstCtx, account, body1)
	}()
	client.waitForCalls(1)

	secondPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	firstPayload := qoderPayloadAtForTest(t, client, 0)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.NotEmpty(t, secondPayload["tools"].([]any))
	messages := secondPayload["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])
}

func TestQoderGatewayDoesNotCommitFailedPostToolStreamAsComplete(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	client := newBlockingQoderClientStub(t)
	svc.client = client
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"failed-post-tool-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	gin.SetMode(gin.TestMode)
	firstRec := httptest.NewRecorder()
	firstCtx, _ := gin.CreateTestContext(firstRec)
	firstCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body1))
	firstCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = svc.ForwardMessages(context.Background(), firstCtx, account, body1)
	}()
	client.waitForCalls(1)

	client.mu.Lock()
	client.nextError = true
	client.mu.Unlock()
	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := svc.ForwardMessages(context.Background(), failedCtx, account, body2)
	require.Error(t, failedErr)

	retryPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	client.finishFirst()
	wg.Wait()
	require.NoError(t, firstErr)

	require.Equal(t, qoderPayloadAtForTest(t, client, 0)["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, retryPayload["tools"].([]any))
	messages := retryPayload["messages"].([]any)
	require.Len(t, messages, 4)
	system := messages[0].(map[string]any)
	require.Equal(t, "system", system["role"])
	user := messages[1].(map[string]any)
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := messages[2].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.NotEmpty(t, assistant["tool_calls"].([]any))
	toolResult := messages[3].(map[string]any)
	require.Equal(t, "tool", toolResult["role"])
	require.Equal(t, "call_1", toolResult["tool_call_id"])
	require.Equal(t, "call_1", toolResult["tool_call_call_id"])
	require.Equal(t, "Bash", toolResult["name"])
	require.Equal(t, "/repo", toolResult["content"])
}

func TestQoderGatewayRollsBackAcceptedConversationOnStreamParseFailure(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	body1 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	body2 := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"rollback-accepted-stream",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"/repo"}]}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	firstPayload := qoderForwardMessagesForTest(t, svc, account, "", body1, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""
	gin.SetMode(gin.TestMode)
	failedRec := httptest.NewRecorder()
	failedCtx, _ := gin.CreateTestContext(failedRec)
	failedCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body2))
	failedCtx.Request.Header.Set("User-Agent", "claude-cli/2.1.177 (external, cli)")
	_, failedErr := svc.ForwardMessages(context.Background(), failedCtx, account, body2)
	require.Error(t, failedErr)

	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	retryPayload := qoderForwardMessagesForTest(t, svc, account, "", body2, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, firstPayload["session_id"], retryPayload["session_id"])
	require.NotEmpty(t, retryPayload["tools"].([]any))
	messages := retryPayload["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "tool", messages[3].(map[string]any)["role"])
}

func TestQoderGatewayFallsBackToFullReplayWhenPrefixDoesNotMatch(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "stable-session", []byte(`{
		"model":"auto",
		"messages":[{"role":"user","content":"first"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "stable-session", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"user","content":"changed"},
			{"role":"assistant","content":"answer"},
			{"role":"user","content":"next"}
		],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := second["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "changed", messages[0].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"])
}

func TestQoderGatewayFallsBackToFullReplayWhenSystemOrToolsChange(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"read","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"messages":[
			{"role":"system","content":"be detailed"},
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"hi"},
			{"role":"user","content":"next"}
		],
		"tools":[{"type":"function","function":{"name":"write","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.NotEqual(t, first["session_id"], second["session_id"])
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "be detailed", messages[0].(map[string]any)["content"])
	tools := second["tools"].([]any)
	require.Equal(t, "write", tools[0].(map[string]any)["function"].(map[string]any)["name"])
}

func TestQoderGatewayUsesExplicitBodySessionID(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"session_id":"body-session-1",
		"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"next"}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.Len(t, second["messages"].([]any), 3)
}

func TestQoderGatewayAnthropicMetadataSessionWinsOverChangingHeader(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	metadata := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	first := qoderForwardMessagesForTest(t, svc, account, "volatile-header-1", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "volatile-header-2", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata)+`},
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := second["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "user", messages[0].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[1].(map[string]any)["role"])
	require.Equal(t, "user", messages[2].(map[string]any)["role"])
	require.Equal(t, "continue", second["chat_context"].(map[string]any)["text"].(map[string]any)["text"])
}

func TestQoderGatewayClaudeCodeUsesExplicitHeaderSessionBeforeStableSeed(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardMessagesForTest(t, svc, account, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "stable-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])

	other := qoderForwardMessagesForTest(t, svc, account, "other-header", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.162 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
}

func TestQoderGatewayClaudeCodeUsesMetadataSessionBeforeStableSeed(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	metadata1 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"11111111-2222-3333-4444-555555555555"}`
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata1)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])
	require.Equal(t, "continue", second["chat_context"].(map[string]any)["text"].(map[string]any)["text"])

	metadata2 := `{"device_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","account_uuid":"","session_id":"66666666-7777-8888-9999-aaaaaaaaaaaa"}`
	other := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"deepseek-v4-pro",
		"metadata":{"user_id":`+strconv.Quote(metadata2)+`},
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.NotEqual(t, first["session_id"], other["session_id"])
	require.NotEmpty(t, other["tools"].([]any))
}

func TestQoderGatewayClaudeCodeIgnoresVolatileBillingCCHForSystemReuse(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"billing-cch-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])
}

func TestQoderGatewayClaudeCodeUltimateStablePromptCacheKeyReportsCacheRead(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"You are a Claude agent, built on Anthropic's Claude Agent SDK.\n" +
		"Stable Claude Code system body."
	firstBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system1) + `,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + largeTools + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"claude-opus-4-6",
		"prompt_cache_key":"ultimate-cache-hit-session",
		"system":` + strconv.Quote(system2) + `,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + largeTools + `,
		"stream":false
	}`)

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	firstPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.Equal(t, "ultimate", firstResult.UpstreamModel)
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, "ultimate", client.headers["x-model-key"])

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1500,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":1533,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":1400,\\\"cacheable_tokens\\\":100}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)

	require.Equal(t, "ultimate", secondResult.UpstreamModel)
	require.Equal(t, firstPayload["session_id"], secondPayload["session_id"])
	require.Equal(t, 100, secondResult.Usage.InputTokens)
	require.Equal(t, 1400, secondResult.Usage.CacheReadInputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(1400), gjson.Get(secondResponse, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(100), gjson.Get(secondResponse, "usage.input_tokens").Int())
}

func TestQoderGatewayStillFullReplaysWhenNonBillingSystemChanges(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	system1 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=29156;\n" +
		"Stable Claude Code system body."
	system2 := "x-anthropic-billing-header: cc_version=2.1.177.19c; cc_entrypoint=sdk-cli; cch=40d8d;\n" +
		"Changed Claude Code system body."
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system1)+`,
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"system-change-session",
		"system":`+strconv.Quote(system2)+`,
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, sdk-cli)"))

	require.NotEqual(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	require.Len(t, second["messages"].([]any), 4)
}

func TestQoderGatewayReusedAnthropicConversationOmitsUnchangedTools(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"你好"}],
		"tools":`+largeTools+`,
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "stable-session", []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`))

	require.NotEmpty(t, first["tools"].([]any))
	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.Equal(t, "user", messages[3].(map[string]any)["role"])
}

func TestQoderGatewayUsageComesFromUpstreamSSE(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1234,\\\"completion_tokens\\\":56,\\\"total_tokens\\\":1290}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{
		"model":"deepseek-v4-pro",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"你好"},
			{"role":"user","content":"你好"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := svc.ForwardMessages(context.Background(), c, account, body)

	require.NoError(t, err)
	require.Equal(t, 1234, result.Usage.InputTokens)
	require.Equal(t, 56, result.Usage.OutputTokens)
	require.NotEqual(t, len(body), result.Usage.InputTokens)
}

func TestQoderGatewayUsageSplitsCachedPromptTokens(t *testing.T) {
	usage := qoderUsageFromEvents([]qoder.SSEEvent{qoderCachedUsageEventForTest})
	require.Equal(t, 25, usage.InputTokens)
	require.Equal(t, 66612, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.OutputTokens)
	require.Equal(t, 0, usage.CacheCreationInputTokens)
}

func TestQoderGatewayUsageClampsCachedPromptTokens(t *testing.T) {
	event := qoderCachedUsageEventForTest
	promptDetails := *event.UsageDetails.PromptTokensDetails
	event.UsageDetails.PromptTokensDetails = &promptDetails
	event.UsageDetails.PromptTokensDetails.CachedTokens = 70000
	usage := qoderUsageFromEvents([]qoder.SSEEvent{event})
	require.Equal(t, 0, usage.InputTokens)
	require.Equal(t, 70000, usage.CacheReadInputTokens)
}

func TestQoderGatewayUsageKeepsOldBehaviorWhenDetailsMissing(t *testing.T) {
	event := qoder.SSEEvent{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true}
	usage := qoderUsageFromEvents([]qoder.SSEEvent{event})
	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 0, usage.CacheReadInputTokens)
	require.Equal(t, 34, usage.OutputTokens)
}

func TestQoderGatewayBuildsClientVisibleOpenAIUsageWithUpstreamTotals(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(66637), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(19), gjson.Get(response, "usage.prompt_tokens_details.cacheable_tokens").Int())
	require.Equal(t, int64(0), gjson.Get(response, "usage.completion_tokens_details.reasoning_tokens").Int())
}

func TestQoderGatewayOpenAIUsageKeepsUpstreamPromptWhenCachedExceedsPrompt(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":16,\\\"prompt_tokens_details\\\":{\\\"cached_tokens\\\":15,\\\"cacheable_tokens\\\":1}}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", body)

	require.Equal(t, 0, result.Usage.InputTokens)
	require.Equal(t, 15, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(10), gjson.Get(response, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.completion_tokens").Int())
	require.Equal(t, int64(16), gjson.Get(response, "usage.total_tokens").Int())
	require.Equal(t, int64(15), gjson.Get(response, "usage.prompt_tokens_details.cached_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

func TestQoderGatewayOpenAIUsageDerivesTotalFromPromptWhenUpstreamTotalMissing(t *testing.T) {
	event := qoderCachedUsageEventForTest
	event.TotalTokens = 0
	body, err := BuildQoderOpenAICompletion("auto", []qoder.SSEEvent{
		{Type: "text_delta", Text: "OK"},
		event,
		{IsDone: true},
	})
	require.NoError(t, err)

	require.Equal(t, int64(66637), gjson.GetBytes(body, "usage.prompt_tokens").Int())
	require.Equal(t, int64(6), gjson.GetBytes(body, "usage.completion_tokens").Int())
	require.Equal(t, int64(66643), gjson.GetBytes(body, "usage.total_tokens").Int())
	require.Equal(t, int64(66612), gjson.GetBytes(body, "usage.prompt_tokens_details.cached_tokens").Int())
}

func TestQoderGatewayBuildsClientVisibleAnthropicUsageWithCacheRead(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		qoderCachedUsageSSEForTest +
		"data: {\"body\":\"[DONE]\"}\n\n"
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}],"stream":false}`)

	result, response := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body)

	require.Equal(t, 25, result.Usage.InputTokens)
	require.Equal(t, 66612, result.Usage.CacheReadInputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	require.Equal(t, int64(25), gjson.Get(response, "usage.input_tokens").Int())
	require.Equal(t, int64(66612), gjson.Get(response, "usage.cache_read_input_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(response, "usage.output_tokens").Int())
	require.False(t, gjson.Get(response, "usage.cache_creation_input_tokens").Exists())
}

func TestQoderGatewayDoesNotSubtractPreviousUsageOnFullReplay(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult := qoderForwardMessagesResultForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult := qoderForwardMessagesResultForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, secondPayload["tools"].([]any))
	require.Len(t, secondPayload["messages"].([]any), 4)
}

func TestQoderGatewayReturnsDeltaUsageToAnthropicClientOnReusedConversation(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	firstBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)
	secondBody := []byte(`{
		"model":"deepseek-v4-pro",
		"prompt_cache_key":"client-usage-delta-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Equal(t, int64(1200), gjson.Get(firstResponse, "usage.input_tokens").Int())

	client.body = "data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondResponse := qoderForwardMessagesResultAndBodyForTest(t, svc, account, secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Equal(t, int64(10000), gjson.Get(secondResponse, "usage.input_tokens").Int())
	require.Equal(t, int64(33), gjson.Get(secondResponse, "usage.output_tokens").Int())

	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, secondPayload["tools"].([]any))
}

func TestQoderConversationStoreExpiresState(t *testing.T) {
	store := newQoderConversationStore(5 * time.Millisecond)
	messages := []qoderMessage{{Role: "user", Text: "hello"}}

	plan := store.plan("key", "", nil, messages)
	require.NotNil(t, plan)
	plan.commit()

	time.Sleep(10 * time.Millisecond)

	next := store.plan("key", "", nil, messages)
	require.False(t, next.reused)
	require.True(t, next.includeSystem)
	require.Len(t, next.messagesToSend, 1)
}

func TestQoderGatewayAnthropicToolUseResultSendsIncrementalTail(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"anthropic-tool-session",
		"system":"be useful",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]}
		],
		"tools":[{"name":"bash","input_schema":{"type":"object"}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 4)

	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	user := messages[1].(map[string]any)
	require.Equal(t, "user", user["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(user))

	assistant := messages[2].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	toolCalls := assistant["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	require.Equal(t, "call_1", toolCalls[0].(map[string]any)["id"])

	tool := messages[3].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	require.Equal(t, "repo", tool["content"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "repo\n")
}

func TestQoderGatewayOpenAIToolCallsSendIncrementalTail(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	first := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))
	second := qoderForwardChatCompletionsForTest(t, svc, account, "", []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-tool-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"}
		],
		"tools":[{"type":"function","function":{"name":"bash","parameters":{"type":"object"}}}],
		"stream":false
	}`))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 3)
	user := messages[0].(map[string]any)
	require.Equal(t, "user", user["role"])
	require.Equal(t, "run pwd", qoderPayloadMessageTextForTest(user))
	assistant := messages[1].(map[string]any)
	require.Equal(t, "assistant", assistant["role"])
	require.Len(t, assistant["tool_calls"].([]any), 1)
	tool := messages[2].(map[string]any)
	require.Equal(t, "tool", tool["role"])
	require.Equal(t, "call_1", tool["tool_call_id"])
	require.Equal(t, "call_1", tool["tool_call_call_id"])
	require.Equal(t, "bash", tool["name"])
	prompt := qoderPayloadPromptForTest(t, second)
	require.Contains(t, prompt, `<tool_result id="call_1">`)
	require.Contains(t, prompt, "/repo\n")
}

func TestQoderGatewayRepeatedClaudeCodeRequestKeepsNonEmptyIncrementalTail(t *testing.T) {
	account, svc, _ := newQoderGatewayForwardTestService()
	body := []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"repeated-claude-request-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":false
	}`)

	first := qoderForwardMessagesForTest(t, svc, account, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", body, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 6)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
}

func TestQoderGatewayStreamsDeltaUsageToOpenAIClientOnReusedConversation(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	firstBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[{"role":"user","content":"run pwd"}],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)
	secondBody := []byte(`{
		"model":"auto",
		"prompt_cache_key":"openai-stream-usage-delta-session",
		"messages":[
			{"role":"user","content":"run pwd"},
			{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":{"cmd":"pwd"}}}]},
			{"role":"tool","tool_call_id":"call_1","name":"bash","content":"/repo"},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":` + qoderLargeToolsJSONForTest() + `,
		"stream":true
	}`)

	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1200,\\\"completion_tokens\\\":30,\\\"total_tokens\\\":1230}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	firstResult, firstStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", firstBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	require.Equal(t, 1200, firstResult.Usage.InputTokens)
	require.Contains(t, firstStream, `"prompt_tokens":1200`)

	client.body = "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10000,\\\"completion_tokens\\\":33,\\\"total_tokens\\\":10033}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	secondResult, secondStream := qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, "", secondBody, qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	secondPayload := qoderLastUpstreamPayloadForTest(t, client)
	require.NotEmpty(t, secondPayload["tools"].([]any))
	require.Equal(t, 10000, secondResult.Usage.InputTokens)
	require.Equal(t, 33, secondResult.Usage.OutputTokens)
	require.Contains(t, secondStream, `"prompt_tokens":10000`)
	require.Contains(t, secondStream, `"completion_tokens":33`)
}

func TestQoderGatewayAnthropicConversationAfterToolResultKeepsReducingPayload(t *testing.T) {
	account, svc, client := newQoderGatewayForwardTestService()
	largeTools := qoderLargeToolsJSONForTest()
	first := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[{"role":"user","content":"inspect"}],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))
	second := qoderForwardMessagesForTest(t, svc, account, "", []byte(`{
		"model":"glm-5.1",
		"prompt_cache_key":"post-tool-reducing-session",
		"system":"You are Claude Code, Anthropic's official CLI for Claude.",
		"messages":[
			{"role":"user","content":"inspect"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"pwd"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"repo"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		],
		"tools":`+largeTools+`,
		"stream":false
	}`), qoderHeader("User-Agent", "claude-cli/2.1.177 (external, cli)"))

	require.Equal(t, first["session_id"], second["session_id"])
	require.NotEmpty(t, second["tools"].([]any))
	messages := second["messages"].([]any)
	require.Len(t, messages, 6)
	require.Equal(t, "system", messages[0].(map[string]any)["role"])
	require.Equal(t, "user", messages[1].(map[string]any)["role"])
	require.Equal(t, "inspect", qoderPayloadMessageTextForTest(messages[1].(map[string]any)))
	require.Equal(t, "assistant", messages[2].(map[string]any)["role"])
	require.NotEmpty(t, messages[2].(map[string]any)["tool_calls"].([]any))
	require.Equal(t, "tool", messages[3].(map[string]any)["role"])
	require.Equal(t, "call_1", messages[3].(map[string]any)["tool_call_id"])
	require.Equal(t, "assistant", messages[4].(map[string]any)["role"])
	require.Equal(t, "user", messages[5].(map[string]any)["role"])

	require.GreaterOrEqual(t, len(client.bodyAt(1)), len(client.bodyAt(0)))
}

func TestQoderGatewayWritesOpenAIStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hel"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayWritesOpenAIToolCallsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"index":0`)
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, `"arguments":"{\"cmd\":"`)
	require.Contains(t, body, `"arguments":"\"pwd\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsEmptyArgumentPlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"arguments":"{}"`)
	require.NotContains(t, body, `"arguments":"{}{\"command\"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamSkipsTypeOnlyPlaceholderChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	chunks := qoderOpenAIStreamChunksForTest(t, rec.Body.String())
	for _, chunk := range chunks {
		toolCalls := gjson.GetBytes(chunk, "choices.0.delta.tool_calls")
		if toolCalls.Exists() {
			require.NotEqual(t, int64(0), toolCalls.Get("#").Int(), string(chunk))
		}
	}
	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"pwd\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamMergesIndexDriftForSameCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, `"arguments":"{\"cmd\":"`)
	require.Contains(t, body, `"arguments":"\"pwd\"}"`)
	require.NotContains(t, body, `"index":1`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIToolCallsStreamKeepsParallelCallIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	chunks := qoderOpenAIStreamChunksForTest(t, rec.Body.String())
	toolDeltas := make([]map[string]any, 0)
	for _, chunk := range chunks {
		rawDeltas := gjson.GetBytes(chunk, "choices.0.delta.tool_calls").Array()
		for _, rawDelta := range rawDeltas {
			var delta map[string]any
			require.NoError(t, json.Unmarshal([]byte(rawDelta.Raw), &delta))
			toolDeltas = append(toolDeltas, delta)
		}
	}
	require.Len(t, toolDeltas, 4)
	require.Equal(t, float64(0), toolDeltas[2]["index"])
	require.Equal(t, `{"path":"a"}`, toolDeltas[2]["function"].(map[string]any)["arguments"])
	require.Equal(t, float64(1), toolDeltas[3]["index"])
	require.Equal(t, `{"path":"b"}`, toolDeltas[3]["function"].(map[string]any)["arguments"])
}

func TestQoderGatewayWritesOpenAIToolCallsStreamDropsAmbiguousParallelArgumentDelta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"id":"call_2"`)
	require.NotContains(t, body, "lost")
	require.NotContains(t, body, `"index":2`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayWritesOpenAIStreamParsesXMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"arguments":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

func TestQoderGatewayWritesOpenAIStreamParsesDSMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := WriteQoderOpenAIStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"arguments":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

func TestQoderGatewayWritesAnthropicStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"type":"thinking_delta"`)
	require.Contains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayWritesAnthropicStreamParsesXMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

func TestQoderGatewayWritesAnthropicStreamParsesDSMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"ls -la\",\"description\":\"List root files\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "DSML")
	require.NotContains(t, body, "invoke")
}

func TestQoderGatewayWritesAnthropicStreamParsesJSONTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"Print working directory\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, `"name":"{\"name\"`)
	require.NotContains(t, body, "No such tool")
}

func TestQoderGatewayWritesAnthropicToolUseStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"name":"bash"`)
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsSplitArgumentsInOneBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"cmd":`},
		{Type: "tool_call_delta", Arguments: `"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`))
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`))
	require.Contains(t, body, `"partial_json":"{\"cmd\":\"pwd\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamDoesNotFinalizeEmptyObjectPlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "toolu_1", ToolType: "function", ToolName: "Bash", Arguments: `{}`},
		{Type: "tool_call_delta", ToolType: "function", Arguments: `{"command":"printf cc-single-20260617","description":"Print single nonce"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 1, strings.Count(body, `"type":"tool_use"`), body)
	require.Equal(t, 1, strings.Count(body, `"type":"input_json_delta"`), body)
	streamEvents := qoderAnthropicStreamEventsForTest(t, body)
	var toolBlock map[string]any
	var partialJSON string
	for _, event := range streamEvents {
		if event.Event == "content_block_start" {
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolBlock = block
			}
		}
		if event.Event == "content_block_delta" {
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partialJSON = delta["partial_json"].(string)
			}
		}
	}
	require.Equal(t, "toolu_1", toolBlock["id"])
	require.Equal(t, "Bash", toolBlock["name"])
	require.JSONEq(t, `{"command":"printf cc-single-20260617","description":"Print single nonce"}`, partialJSON)
	require.NotContains(t, body, `"partial_json":"{}"`)
	require.NotContains(t, body, `"name":""`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamSkipsTypeOnlyPlaceholder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.NotContains(t, body, `"type":"tool_use"`)
	require.NotContains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"stop_reason":"end_turn"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, block["name"].(string))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := delta["partial_json"].(string)
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, block["name"].(string))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := delta["partial_json"].(string)
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK","description":"Print parallel OK string"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsSameIndexNewIDSplitArgumentsAligned(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "thinking"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"command":"pwd`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"command":"printf OPENCODE_`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `PARALLEL_OK"}`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_3", ToolType: "function", ToolName: "glob"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolType: "function"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"pattern":"docs/*.md`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "claude-opus-4-6", events)
	require.NoError(t, err)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, block["name"].(string))
				require.NotEmpty(t, block["id"], rec.Body.String())
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				inputDeltas = append(inputDeltas, delta["partial_json"].(string))
			}
		}
	}
	require.Equal(t, []string{"bash", "bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"printf OPENCODE_PARALLEL_OK"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"docs/*.md"}`, inputDeltas[2])
	require.NotContains(t, rec.Body.String(), `"name":""`)
	require.NotContains(t, rec.Body.String(), `}{`)
}

func TestQoderGatewayWritesAnthropicToolUseStreamKeepsParallelCallIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Equal(t, 2, strings.Count(body, `"type":"tool_use"`))
	require.Contains(t, body, `"id":"call_1"`)
	require.Contains(t, body, `"id":"call_2"`)
	streamEvents := qoderAnthropicStreamEventsForTest(t, body)
	inputDeltas := make(map[int]string)
	openToolBlocks := make(map[int]bool)
	for _, event := range streamEvents {
		if event.Event == "content_block_start" {
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				openToolBlocks[int(event.Data["index"].(float64))] = true
			}
			continue
		}
		if event.Event == "content_block_stop" {
			delete(openToolBlocks, int(event.Data["index"].(float64)))
			continue
		}
		if event.Event != "content_block_delta" {
			continue
		}
		delta, _ := event.Data["delta"].(map[string]any)
		if delta["type"] != "input_json_delta" {
			continue
		}
		index := int(event.Data["index"].(float64))
		require.True(t, openToolBlocks[index], "input delta must be inside an open tool_use block")
		inputDeltas[index] = delta["partial_json"].(string)
	}
	require.Equal(t, `{"path":"a"}`, inputDeltas[0])
	require.Equal(t, `{"path":"b"}`, inputDeltas[1])
	require.Empty(t, openToolBlocks)
}

func TestQoderGatewayAssemblesNonStreamingChatCompletion(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hel"},
		{Type: "text_delta", Text: "lo"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	choices := decoded["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "Hello", message["content"])
	usage := decoded["usage"].(map[string]any)
	require.Equal(t, float64(12), usage["prompt_tokens"])
	require.Equal(t, float64(34), usage["completion_tokens"])
	require.Equal(t, float64(46), usage["total_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionWithToolCalls(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.True(t, gjson.GetBytes(body, "choices.0.message.content").Exists())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionMapsToolNameToDeclaredOpenAITool(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "Bash", Arguments: `{"command":"pwd"}`},
		{IsDone: true},
	}
	tools := []any{map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":       "bash",
			"parameters": map[string]any{"type": "object"},
		},
	}}

	body, err := BuildQoderOpenAICompletion("auto", events, qoderDeclaredToolNameMapper(tools))
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionMergesIndexDriftForSameCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_1", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(1), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"cmd":"pwd"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsParallelCallIndexes(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `{"path":"a"}`},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, Arguments: `{"path":"b"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"path":"a"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.JSONEq(t, `{"path":"b"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
}

func TestQoderGatewayDoesNotAttachAmbiguousArgumentDeltaToParallelToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "read"},
		{Type: "tool_call_delta", ToolCallIndex: 1, HasToolCallIndex: true, ToolCallID: "call_2", ToolType: "function", ToolName: "write"},
		{Type: "tool_call_delta", Arguments: `{"path":"lost"}`},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, int64(2), gjson.GetBytes(body, "choices.0.message.tool_calls.#").Int())
	require.Equal(t, "call_1", gjson.GetBytes(body, "choices.0.message.tool_calls.0.id").String())
	require.Equal(t, "read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.Equal(t, "call_2", gjson.GetBytes(body, "choices.0.message.tool_calls.1.id").String())
	require.Equal(t, "write", gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.name").String())
	require.Empty(t, gjson.GetBytes(body, "choices.0.message.tool_calls.1.function.arguments").String())
	require.NotContains(t, string(body), "lost")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesJSONTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.NotContains(t, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String(), "{")
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"pwd","description":"Print working directory"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesDSMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, gjson.Null, gjson.GetBytes(body, "choices.0.message.content").Type)
	require.Equal(t, "Bash", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"command":"ls -la","description":"List root files"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionParsesSplitXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "<tool_"},
		{Type: "text_delta", Text: "call>Re"},
		{Type: "text_delta", Text: "ad<arg_value><arg_key>file_path</arg_key>"},
		{Type: "text_delta", Text: "<arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>"},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.JSONEq(t, `{"file_path":"/workspace/campus-navigation/README.md"}`, gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.arguments").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingChatCompletionKeepsMixedXMLToolText(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: "I will inspect it.\n"},
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{Type: "text_delta", Text: "\nWaiting for result."},
		{IsDone: true},
	}

	body, err := BuildQoderOpenAICompletion("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_calls", gjson.GetBytes(body, "choices.0.finish_reason").String())
	require.Equal(t, "I will inspect it.\n\nWaiting for result.", gjson.GetBytes(body, "choices.0.message.content").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "choices.0.message.tool_calls.0.function.name").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithThinking(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "reasoning_delta", Text: "hidden thought"},
		{Type: "text_delta", Text: "Hi"},
		{Type: "usage", PromptTokens: 12, CompletionTokens: 34, TotalTokens: 46, HasUsage: true},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(body, &decoded))
	content := decoded["content"].([]any)
	require.Len(t, content, 2)
	thinkingBlock := content[0].(map[string]any)
	require.Equal(t, "thinking", thinkingBlock["type"])
	require.Equal(t, "hidden thought", thinkingBlock["thinking"])
	textBlock := content[1].(map[string]any)
	require.Equal(t, "Hi", textBlock["text"])
	usage := decoded["usage"].(map[string]any)
	require.Equal(t, float64(12), usage["input_tokens"])
	require.Equal(t, float64(34), usage["output_tokens"])
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageWithToolUse(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{Type: "tool_call_delta", ToolCallIndex: 0, HasToolCallIndex: true, Arguments: `"pwd"}`},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("auto", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "call_1", gjson.GetBytes(body, "content.0.id").String())
	require.Equal(t, "bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.cmd").String())
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageRejectsMalformedToolArguments(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolType: "function", ToolName: "bash", Arguments: `{"cmd":`},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("auto", events)

	require.Error(t, err)
	require.Nil(t, body)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageSkipsTypeOnlyPlaceholder(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolType: "function"},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "end_turn", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, "text", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.False(t, gjson.GetBytes(body, `content.#(type=="tool_use")`).Exists(), string(body))
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", qoderNoIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, int64(3), gjson.GetBytes(body, "content.#").Int(), string(body))
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String(), string(body))
	require.Equal(t, "pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm", gjson.GetBytes(body, "content.0.input.command").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.1.name").String(), string(body))
	require.Equal(t, "ls -la", gjson.GetBytes(body, "content.1.input.command").String(), string(body))
	require.Equal(t, "glob", gjson.GetBytes(body, "content.2.name").String(), string(body))
	require.Equal(t, "**/*.md", gjson.GetBytes(body, "content.2.input.pattern").String(), string(body))
	require.False(t, gjson.GetBytes(body, "content.0.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.1.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.2.input.raw").Exists(), string(body))
	require.NotContains(t, string(body), `}{`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageKeepsRepeatedIndexNamedParallelToolCalls(t *testing.T) {
	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", qoderRepeatedIndexNamedParallelToolCallEventsForTest())
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String(), string(body))
	require.Equal(t, int64(3), gjson.GetBytes(body, "content.#").Int(), string(body))
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String(), string(body))
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.command").String(), string(body))
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.1.name").String(), string(body))
	require.Equal(t, "printf OPENCODE_PARALLEL_OK", gjson.GetBytes(body, "content.1.input.command").String(), string(body))
	require.Equal(t, "glob", gjson.GetBytes(body, "content.2.name").String(), string(body))
	require.Equal(t, "docs/*.md", gjson.GetBytes(body, "content.2.input.pattern").String(), string(body))
	require.False(t, gjson.GetBytes(body, "content.0.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.1.input.raw").Exists(), string(body))
	require.False(t, gjson.GetBytes(body, "content.2.input.raw").Exists(), string(body))
	require.NotContains(t, string(body), `}{`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesXMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderXMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Read", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "/workspace/campus-navigation/README.md", gjson.GetBytes(body, "content.0.input.file_path").String())
	require.NotContains(t, string(body), "<tool_call>")
	require.NotContains(t, string(body), "arg_key")
	require.NotContains(t, string(body), "arg_value")
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesJSONTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderJSONShellToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "pwd", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "Print working directory", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), `"name":"{\"name\"`)
}

func TestQoderGatewayAssemblesNonStreamingAnthropicMessageParsesDSMLTextToolCall(t *testing.T) {
	events := []qoder.SSEEvent{
		{Type: "text_delta", Text: qoderDSMLToolCallFixture},
		{IsDone: true},
	}

	body, err := BuildQoderAnthropicMessage("claude-opus-4-6", events)
	require.NoError(t, err)

	require.Equal(t, "tool_use", gjson.GetBytes(body, "stop_reason").String())
	require.Equal(t, "tool_use", gjson.GetBytes(body, "content.0.type").String())
	require.Equal(t, "Bash", gjson.GetBytes(body, "content.0.name").String())
	require.Equal(t, "ls -la", gjson.GetBytes(body, "content.0.input.command").String())
	require.Equal(t, "List root files", gjson.GetBytes(body, "content.0.input.description").String())
	require.NotContains(t, string(body), "DSML")
	require.NotContains(t, string(body), "invoke")
}

func TestQoderGatewayReadsWrappedSSE(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":3,\\\"completion_tokens\\\":4,\\\"total_tokens\\\":7}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Equal(t, "reasoning_delta", events[0].Type)
	require.Equal(t, "hidden thought", events[0].Text)
	require.Equal(t, "text_delta", events[1].Type)
	require.Equal(t, "Hi", events[1].Text)
	require.True(t, events[2].HasUsage)
	require.Equal(t, 3, events[2].PromptTokens)
	require.Equal(t, 4, events[2].CompletionTokens)
	require.True(t, events[3].IsDone)
}

func TestQoderGatewayScannerStopsWhenResultSendIsCanceled(t *testing.T) {
	line := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(strings.Repeat(line, 10)))}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan qoderEventResult, 1)
	done := make(chan struct{})

	go func() {
		scanQoderEvents(ctx, resp, results)
		close(done)
	}()

	select {
	case <-results:
	case <-time.After(time.Second):
		t.Fatal("scanner did not emit first event")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scanner goroutine did not exit after context cancellation")
	}
}

func TestQoderGatewayNonStreamingKeepaliveKeepsJSONParseable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	keepalive := qoderNonStreamingKeepalive(c)
	require.NotNil(t, keepalive)
	require.NoError(t, keepalive())
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(`{"ok":true}`))

	require.True(t, json.Valid(rec.Body.Bytes()))
	require.Equal(t, "\n{\"ok\":true}", rec.Body.String())
	require.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"))
}

func TestQoderGatewayNonStreamingReadDoesNotCommitResponseBeforeError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEventsContext(context.Background(), resp, nil)

	require.Error(t, err)
	require.Empty(t, events)
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Get("Cache-Control"))
	require.False(t, c.Writer.Written())
}

func TestQoderGatewayReadsWrappedSSEUpstreamError(t *testing.T) {
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	require.Error(t, err)
	require.Empty(t, events)
	var apiErr *qoder.APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 403, apiErr.StatusCode)
	require.Equal(t, "101", apiErr.Code)
	require.Equal(t, "Signature invalid", apiErr.Message)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", apiErr.Error())
}

func TestQoderGatewayAgentLimitSetsRateLimitedUntilReset(t *testing.T) {
	repo := &qoderRateLimitRepoStub{}
	svc := &QoderGatewayService{accountRepo: repo}
	account := &Account{ID: 77}
	err := &qoder.APIError{
		StatusCode:          http.StatusTooManyRequests,
		Code:                "115",
		AgentLimitResetTime: 1783841289162,
	}

	svc.applyUpstreamErrorPolicy(context.Background(), account, err)

	require.Equal(t, int64(77), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

func TestQoderGatewayNonStreamingSSEAgentLimitSetsRateLimited(t *testing.T) {
	account := &Account{ID: 78}
	repo := &qoderRateLimitRepoStub{}
	svc := &QoderGatewayService{accountRepo: repo}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"code\\\":\\\"115\\\",\\\"message\\\":\\\"{\\\\\\\"agentLimitResetTime\\\\\\\":1783841289162}\\\"}\",\"statusCodeValue\":429,\"statusCode\":\"TOO_MANY_REQUESTS\"}\n\n",
		)),
	}

	events, err := ReadQoderSSEEvents(resp)
	if err != nil {
		svc.applyUpstreamErrorPolicy(context.Background(), account, err)
	}

	require.Error(t, err)
	require.Empty(t, events)
	require.Equal(t, int64(78), repo.rateLimitedID)
	require.Equal(t, int64(1783841289162), repo.resetAt.UnixMilli())
}

func TestQoderGatewayRequestDoerUsesHTTPUpstreamProxyAndTLS(t *testing.T) {
	proxyID := int64(11)
	account := &Account{
		ID:          66,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 3,
		ProxyID:     &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	upstream := &qoderHTTPUpstreamRecorder{
		body: "data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &QoderGatewayService{
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	doer := svc.qoderRequestDoer(account)
	require.NotNil(t, doer)
	req := httptest.NewRequest(http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader("{}"))

	resp, err := doer(req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(66), upstream.accountID)
	require.True(t, upstream.profileSet)
}

func TestQoderGatewayRefreshAccountSessionPersistsCredentialsAndInvalidatesCache(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-1 * time.Hour) // 已过期
	account := Account{
		ID:       91,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"expires_at":           expiredAt.Format(time.RFC3339), // 设置过期时间让 NeedsRefresh 返回 true
		},
	}
	repo := &qoderRefreshAccountRepoStub{
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
	}
	provider := &QoderTokenProvider{
		sessions: map[int64]qoderSessionCacheEntry{
			account.ID: {
				credentialsHash: "old-hash",
				session: &qoder.SessionContext{
					Identity: &qoder.AuthIdentity{SecurityOauthToken: "old-token"},
					Machine:  &qoder.MachineIdentity{MachineID: "machine-1"},
				},
			},
		},
	}
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(_ context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		require.Equal(t, "old-refresh", refreshToken)
		require.Equal(t, "old-token", securityOauthToken)
		require.Equal(t, "machine-1", machine.MachineID)
		return &qoder.AuthIdentity{
			SecurityOauthToken: "new-token",
			RefreshToken:       "new-refresh",
			UID:                "user-1",
			AID:                "user-1",
			UserType:           "personal_standard",
		}, nil
	}
	svc := &QoderGatewayService{
		tokenProvider: provider,
		accountRepo:   repo,
		newRefresher:  func() *QoderTokenRefresher { return refresher },
		refreshAPI:    NewOAuthRefreshAPI(repo, nil),
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, "new-token", repo.updatedCredentials["security_oauth_token"])
	require.Equal(t, "new-refresh", repo.updatedCredentials["refresh_token"])
	require.NotNil(t, repo.updatedCredentials["_token_version"])
	_, cached := provider.sessions[account.ID]
	require.False(t, cached)
}

func TestNewQoderGatewayServiceUsesInjectedRefreshAPI(t *testing.T) {
	repo := &qoderRefreshAccountRepoStub{}
	refreshAPI := NewOAuthRefreshAPI(repo, nil)

	svc := NewQoderGatewayService(nil, repo, nil, nil, refreshAPI)

	require.Same(t, refreshAPI, svc.refreshAPI)
	require.NotNil(t, svc.tokenProvider)
}

func TestQoderGatewayRefreshAccountSessionRequiresInjectedRefreshAPI(t *testing.T) {
	svc := &QoderGatewayService{
		accountRepo:  &qoderRefreshAccountRepoStub{},
		newRefresher: func() *QoderTokenRefresher { return NewQoderTokenRefresher(nil) },
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &Account{ID: 1})

	require.Nil(t, refreshed)
	require.EqualError(t, err, "qoder refresh API is not configured")
}

func TestQoderGatewayRefreshAccountSessionRecoversRotatedRefreshTokenRace(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-1 * time.Hour) // 已过期
	account := Account{
		ID:       92,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"expires_at":           expiredAt.Format(time.RFC3339), // 设置过期时间
		},
	}
	racedAccount := account
	racedAccount.Credentials = map[string]any{
		"security_oauth_token": "new-token",
		"refresh_token":        "new-refresh",
		"machine_id":           "machine-1",
		"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339), // 新 token 未过期
	}
	repo := &qoderRefreshRaceRepoStub{
		qoderRefreshAccountRepoStub: qoderRefreshAccountRepoStub{
			stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
		},
		raceAccount: &racedAccount,
	}
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(_ context.Context, refreshToken, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		require.Equal(t, "old-refresh", refreshToken)
		return nil, errors.New("invalid_grant: refresh token has already been used")
	}
	svc := &QoderGatewayService{
		tokenProvider: NewQoderTokenProvider(),
		accountRepo:   repo,
		newRefresher:  func() *QoderTokenRefresher { return refresher },
		refreshAPI:    NewOAuthRefreshAPI(repo, nil),
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, "new-refresh", refreshed.GetCredential("refresh_token"))
	require.Equal(t, 0, repo.updateCalls)
	require.GreaterOrEqual(t, repo.getByIDCalls, 2)
}

func TestQoderGatewayRefreshAccountSessionWaitsForLockHolderRotation(t *testing.T) {
	now := time.Now()
	account := Account{
		ID:       94,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"uid":                  "user-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	rotatedAccount := account
	rotatedAccount.Credentials = map[string]any{
		"security_oauth_token": "new-token",
		"refresh_token":        "new-refresh",
		"machine_id":           "machine-1",
		"uid":                  "user-1",
		"expires_at":           now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	repo := &qoderRefreshRaceRepoStub{
		qoderRefreshAccountRepoStub: qoderRefreshAccountRepoStub{
			stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
		},
		raceAccount: &rotatedAccount,
	}
	provider := NewQoderTokenProvider()
	provider.sessions[account.ID] = qoderSessionCacheEntry{
		credentialsHash: qoderCredentialsHash(account.Credentials),
		session:         &qoder.SessionContext{},
	}
	svc := &QoderGatewayService{
		tokenProvider: provider,
		accountRepo:   repo,
		newRefresher:  func() *QoderTokenRefresher { return NewQoderTokenRefresher(nil) },
		refreshAPI:    NewOAuthRefreshAPI(repo, qoderRefreshLockCacheStub{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	refreshed, err := svc.RefreshAccountSession(ctx, &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, "new-token", refreshed.GetCredential("security_oauth_token"))
	require.Equal(t, "new-refresh", refreshed.GetCredential("refresh_token"))
	require.GreaterOrEqual(t, repo.getByIDCalls, 2)
	_, cached := provider.sessions[account.ID]
	require.False(t, cached)
}

func TestQoderGatewayRefreshAccountSessionLockHeldReturnsRefreshInProgressWithoutStaleAccount(t *testing.T) {
	now := time.Now()
	account := Account{
		ID:       95,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"uid":                  "user-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	repo := &qoderRefreshAccountRepoStub{
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
	}
	provider := NewQoderTokenProvider()
	provider.sessions[account.ID] = qoderSessionCacheEntry{
		credentialsHash: qoderCredentialsHash(account.Credentials),
		session:         &qoder.SessionContext{},
	}
	svc := &QoderGatewayService{
		tokenProvider: provider,
		accountRepo:   repo,
		newRefresher:  func() *QoderTokenRefresher { return NewQoderTokenRefresher(nil) },
		refreshAPI:    NewOAuthRefreshAPI(repo, qoderRefreshLockCacheStub{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	refreshed, err := svc.RefreshAccountSession(ctx, &account)

	require.Nil(t, refreshed)
	require.ErrorIs(t, err, ErrQoderRefreshInProgress)
	_, cached := provider.sessions[account.ID]
	require.True(t, cached)
	require.Equal(t, "old-token", repo.accounts[0].GetCredential("security_oauth_token"))
}

func TestQoderGatewayRefreshExecutorNeedsRefreshUsesFailedCredentialSnapshot(t *testing.T) {
	now := time.Now()
	failedAccount := Account{
		ID:       96,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "failed-token",
			"refresh_token":        "failed-refresh",
			"machine_id":           "machine-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	rotatedAccount := failedAccount
	rotatedAccount.Credentials = cloneCredentials(failedAccount.Credentials)
	rotatedAccount.Credentials["security_oauth_token"] = "rotated-token"
	rotatedAccount.Credentials["refresh_token"] = "rotated-refresh"

	executor := qoderGatewayRefreshExecutor{
		QoderTokenRefresher: NewQoderTokenRefresher(nil),
		failedCredentials:   qoderCredentialsHash(failedAccount.Credentials),
	}

	require.True(t, executor.NeedsRefresh(&failedAccount, 15*time.Minute))
	require.False(t, executor.NeedsRefresh(&rotatedAccount, 15*time.Minute))

	missingRefreshToken := failedAccount
	missingRefreshToken.Credentials = cloneCredentials(failedAccount.Credentials)
	delete(missingRefreshToken.Credentials, "refresh_token")
	require.False(t, executor.NeedsRefresh(&missingRefreshToken, 15*time.Minute))

	defaultExecutor := qoderGatewayRefreshExecutor{QoderTokenRefresher: NewQoderTokenRefresher(nil)}
	require.False(t, defaultExecutor.NeedsRefresh(&failedAccount, 15*time.Minute))
}

func TestQoderGatewayForwardChatCompletionsHonorsCanceledContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	account := &Account{
		ID:       93,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-token",
		},
	}
	provider := NewQoderTokenProvider()
	provider.exchangePAT = func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return &qoder.AuthIdentity{SecurityOauthToken: "token", UID: "uid"}, nil
		}
	}
	svc := &QoderGatewayService{tokenProvider: provider}

	_, err := svc.ForwardChatCompletions(ctx, c, account, []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`))

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, c.Writer.Written())
}

func TestQoderGatewayStreamsResponseWithoutPrebuffering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderOpenAIStreamResponse(context.Background(), c, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"delta":{"role":"assistant"}`)
	require.Contains(t, rec.Body.String(), `"delta":{"content":"Hi"}`)
	require.NotContains(t, rec.Body.String(), "hidden thought")
	require.Contains(t, rec.Body.String(), "data: [DONE]\n\n")
}

func TestQoderGatewayStreamsOpenAIResponseParsesXMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": qoderXMLToolCallFixture}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderOpenAIStreamResponse(context.Background(), c, "auto", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"arguments":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

func TestQoderGatewayStreamsOpenAIResponseMapsToolNameToDeclaredOpenAITool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"index": 0, "id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"pwd"}`}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}
	tools := []any{map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":       "bash",
			"parameters": map[string]any{"type": "object"},
		},
	}}

	result, err := WriteQoderOpenAIStreamResponse(context.Background(), c, "auto", resp, qoderOpenAIStreamToolNameMapper(qoderDeclaredToolNameMapper(tools)))
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"tool_calls"`)
	require.Contains(t, body, `"name":"bash"`)
	require.NotContains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"finish_reason":"tool_calls"`)
}

func TestQoderGatewayStreamsAnthropicResponseMapsThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_delta")
	require.Contains(t, body, `"type":"thinking"`)
	require.Contains(t, body, `"type":"thinking_delta"`)
	require.Contains(t, body, `"thinking":"hidden thought"`)
	require.Contains(t, body, `"text":"Hi"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayStreamsAnthropicResponseParsesXMLTextToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"content": "<tool_call>Re"}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"content": "ad<arg_value><arg_key>file_path</arg_key><arg_value>/workspace/campus-navigation/README.md</arg_value></tool_call>"}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Read"`)
	require.Contains(t, body, `"partial_json":"{\"file_path\":\"/workspace/campus-navigation/README.md\"}"`)
	require.Contains(t, body, `"stop_reason":"tool_use"`)
	require.NotContains(t, body, "<tool_call>")
	require.NotContains(t, body, "arg_key")
	require.NotContains(t, body, "arg_value")
}

func TestQoderGatewayStreamsAnthropicResponseMapsFlatToolCallInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"tool_call_id": "call_1", "name": "Bash", "arguments": map[string]any{"command": "pwd"}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	var toolStart map[string]any
	var partialJSON string
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolStart = block
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partialJSON += delta["partial_json"].(string)
			}
		}
	}
	require.NotNil(t, toolStart)
	require.Equal(t, "call_1", toolStart["id"])
	require.Equal(t, "Bash", toolStart["name"])
	require.JSONEq(t, `{"command":"pwd"}`, partialJSON)
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

func TestQoderGatewayWritesAnthropicResponseKeepsNoIndexNamedParallelToolCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(qoderNoIndexNamedParallelToolCallsWrappedSSEForTest(t))),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.True(t, result.HasOutput)

	streamEvents := qoderAnthropicStreamEventsForTest(t, rec.Body.String())
	toolNames := make([]string, 0)
	inputDeltas := make([]string, 0)
	for _, event := range streamEvents {
		switch event.Event {
		case "content_block_start":
			block, _ := event.Data["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				toolNames = append(toolNames, block["name"].(string))
			}
		case "content_block_delta":
			delta, _ := event.Data["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				partial := delta["partial_json"].(string)
				require.NotContains(t, partial, `}{`)
				inputDeltas = append(inputDeltas, partial)
			}
		}
	}
	require.Equal(t, []string{"Bash", "Bash", "glob"}, toolNames)
	require.Len(t, inputDeltas, 3)
	require.JSONEq(t, `{"command":"pwd && date \"+%Y-%m-%d %H:%M:%S\" && uname -srm","description":"Show current dir, time, system info"}`, inputDeltas[0])
	require.JSONEq(t, `{"command":"ls -la","description":"List files in current directory"}`, inputDeltas[1])
	require.JSONEq(t, `{"pattern":"**/*.md"}`, inputDeltas[2])
	require.Contains(t, rec.Body.String(), `"stop_reason":"tool_use"`)
}

func TestQoderGatewayStreamsAnthropicResponseReplacesEmptyToolArguments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": "{}"}},
				}}},
			}}) +
				qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
					map[string]any{"delta": map[string]any{"tool_calls": []any{
						map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"arguments": map[string]any{"command": "pwd", "description": "Print working directory"}}},
					}}},
				}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, ClaudeUsage{}, result.Usage)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"input_json_delta"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\",\"description\":\"Print working directory\"}"`)
	require.NotContains(t, body, `"partial_json":"{}{\"command\"`)
}

func TestQoderGatewayStreamsAnthropicResponseRejectsMalformedToolArguments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{
				map[string]any{"delta": map[string]any{"tool_calls": []any{
					map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"cmd":`}},
				}}},
			}}) +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)

	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "malformed qoder tool arguments")
}

func TestQoderGatewayStreamsAnthropicResponseCompletesEmptyContentBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"content_block":{"text":"","type":"text"}`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, `"stop_reason":"end_turn"`)
	require.Contains(t, body, "event: message_stop")
}

func TestQoderGatewayStreamsAnthropicResponseCompletesOnEOFWithoutDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":0,\\\"total_tokens\\\":8}}\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)
	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)

	body := rec.Body.String()
	require.Contains(t, body, "event: message_start")
	require.Contains(t, body, "event: content_block_start")
	require.Contains(t, body, `"content_block":{"text":"","type":"text"}`)
	require.Contains(t, body, "event: content_block_stop")
	require.Contains(t, body, "event: message_delta")
	require.Contains(t, body, `"stop_reason":"end_turn"`)
	require.Contains(t, body, "event: message_stop")
	require.Equal(t, 1, strings.Count(body, "event: message_stop"))
}

func TestQoderGatewayWritesAnthropicStreamNormalizesExecuteBashToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	events := []qoder.SSEEvent{
		{Type: "tool_call_delta", ToolCallID: "call_1", ToolName: "execute_bash", Arguments: `{"cmd":"pwd"}`},
		{IsDone: true},
	}

	err := WriteQoderAnthropicStream(c, "auto", events)
	require.NoError(t, err)

	body := rec.Body.String()
	require.Contains(t, body, `"type":"tool_use"`)
	require.Contains(t, body, `"name":"Bash"`)
	require.Contains(t, body, `"partial_json":"{\"command\":\"pwd\"}"`)
	require.NotContains(t, body, `"name":"execute_bash"`)
}

func TestQoderGatewayStreamsOpenAIUsageForBillingAndClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":6,\\\"total_tokens\\\":11}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderOpenAIStreamResponse(context.Background(), c, "auto", resp)

	require.NoError(t, err)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"prompt_tokens":5`)
	require.Contains(t, body, `"completion_tokens":6`)
	require.Contains(t, body, `"total_tokens":11`)
	require.Contains(t, body, "data: [DONE]\n\n")
}

func TestQoderGatewayStreamsAnthropicUsageForBillingAndClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"Hi\\\"}}]}\"}\n\n" +
				"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":8,\\\"completion_tokens\\\":9,\\\"total_tokens\\\":17}}\"}\n\n" +
				"data: {\"body\":\"[DONE]\"}\n\n",
		)),
	}

	result, err := WriteQoderAnthropicStreamResponse(context.Background(), c, "claude-opus-4-6", resp)

	require.NoError(t, err)
	require.Equal(t, 8, result.Usage.InputTokens)
	require.Equal(t, 9, result.Usage.OutputTokens)
	body := rec.Body.String()
	require.Contains(t, body, `"usage":`)
	require.Contains(t, body, `"input_tokens":8`)
	require.Contains(t, body, `"output_tokens":9`)
	require.Contains(t, body, "event: message_stop")
}

func qoderOpenAIStreamChunksForTest(t *testing.T, stream string) [][]byte {
	t.Helper()
	chunks := make([][]byte, 0)
	for _, block := range strings.Split(stream, "\n\n") {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "" || data == "[DONE]" {
				continue
			}
			require.True(t, gjson.Valid(data), "invalid SSE JSON data: %s", data)
			chunks = append(chunks, []byte(data))
		}
	}
	return chunks
}

func qoderWrappedSSELineForTest(t *testing.T, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	wrapper, err := json.Marshal(map[string]string{"body": string(body)})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}

func qoderPayloadPromptForTest(t *testing.T, payload map[string]any) string {
	t.Helper()
	chatContext := payload["chat_context"].(map[string]any)
	text := chatContext["text"].(map[string]any)
	return text["text"].(string)
}

func qoderPayloadMessageTextForTest(msg map[string]any) string {
	if msg == nil {
		return ""
	}
	contents, _ := msg["contents"].([]any)
	for _, raw := range contents {
		block, ok := raw.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		if text, ok := block["text"].(string); ok {
			return text
		}
	}
	if text, ok := msg["content"].(string); ok {
		return text
	}
	return ""
}

func newQoderGatewayForwardTestService() (*Account, *QoderGatewayService, *qoderAccountTestClientStub) {
	account := &Account{
		ID:          8801,
		Name:        "qoder",
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{},
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":1,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":2}}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	provider := &QoderTokenProvider{}
	provider.sessions = map[int64]qoderSessionCacheEntry{
		account.ID: {
			credentialsHash: qoderCredentialsHash(account.Credentials),
			session:         &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
	}
	return account, &QoderGatewayService{
		tokenProvider: provider,
		client:        client,
		conversations: newQoderConversationStore(qoderConversationTTL),
	}, client
}

type qoderForwardTestHeader struct {
	key   string
	value string
}

func qoderHeader(key, value string) qoderForwardTestHeader {
	return qoderForwardTestHeader{key: key, value: value}
}

func qoderForwardChatCompletionsForTest(t *testing.T, svc *QoderGatewayService, account *Account, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	_, _ = qoderForwardChatCompletionsResultAndBodyForTest(t, svc, account, sessionID, body, headers...)
	return qoderLastUpstreamPayloadForTest(t, svc.client.(*qoderAccountTestClientStub))
}

func qoderForwardChatCompletionsResultAndBodyForTest(t *testing.T, svc *QoderGatewayService, account *Account, sessionID string, body []byte, headers ...qoderForwardTestHeader) (*ForwardResult, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	if sessionID != "" {
		c.Request.Header.Set("session_id", sessionID)
	}
	for _, header := range headers {
		c.Request.Header.Set(header.key, header.value)
	}
	result, err := svc.ForwardChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderForwardMessagesForTest(t *testing.T, svc *QoderGatewayService, account *Account, sessionID string, body []byte, headers ...qoderForwardTestHeader) map[string]any {
	t.Helper()
	result := qoderForwardMessagesResultForTest(t, svc, account, body, append([]qoderForwardTestHeader{qoderHeader("session_id", sessionID)}, headers...)...)
	require.NotNil(t, result)
	client, ok := svc.client.(interface {
		bodyAt(int) []byte
		bodyCount() int
	})
	require.True(t, ok)
	return qoderPayloadAtForTest(t, client, client.bodyCount()-1)
}

func qoderForwardMessagesResultForTest(t *testing.T, svc *QoderGatewayService, account *Account, body []byte, headers ...qoderForwardTestHeader) *ForwardResult {
	t.Helper()
	result, _ := qoderForwardMessagesResultAndBodyForTest(t, svc, account, body, headers...)
	return result
}

func qoderForwardMessagesResultAndBodyForTest(t *testing.T, svc *QoderGatewayService, account *Account, body []byte, headers ...qoderForwardTestHeader) (*ForwardResult, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	if strings.EqualFold(strings.TrimSpace(c.Request.Header.Get("X-Test-Claude-Code-Context")), "true") {
		c.Request = c.Request.WithContext(SetClaudeCodeClient(c.Request.Context(), true))
	}
	result, err := svc.ForwardMessages(context.Background(), c, account, body)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderForwardResponsesResultAndBodyForTest(t *testing.T, svc *QoderGatewayService, account *Account, body []byte, headers ...qoderForwardTestHeader) (*ForwardResult, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	for _, header := range headers {
		if strings.TrimSpace(header.value) != "" {
			c.Request.Header.Set(header.key, header.value)
		}
	}
	result, err := svc.ForwardResponses(context.Background(), c, account, body)
	require.NoError(t, err)
	return result, rec.Body.String()
}

func qoderResponsesStreamEventsForTest(t *testing.T, body string) []gjson.Result {
	t.Helper()
	var events []gjson.Result
	for _, frame := range strings.Split(body, "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" || strings.HasPrefix(frame, ":") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if data == "" || data == "[DONE]" {
				continue
			}
			require.True(t, gjson.Valid(data), "invalid Responses SSE JSON data: %s", data)
			events = append(events, gjson.Parse(data))
		}
	}
	return events
}

func qoderResponsesCompletedEventForTest(t *testing.T, body string) gjson.Result {
	t.Helper()
	for _, event := range qoderResponsesStreamEventsForTest(t, body) {
		if event.Get("type").String() == "response.completed" {
			return event
		}
	}
	t.Fatalf("response.completed event not found in %s", body)
	return gjson.Result{}
}

func qoderLastUpstreamPayloadForTest(t *testing.T, client *qoderAccountTestClientStub) map[string]any {
	t.Helper()
	require.NotNil(t, client)
	require.NotZero(t, client.bodyCount())
	return qoderPayloadAtForTest(t, client, client.bodyCount()-1)
}

func qoderPayloadAtForTest(t *testing.T, client interface{ bodyAt(int) []byte }, index int) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(client.bodyAt(index), &payload))
	return payload
}

func (s *qoderAccountTestClientStub) bodyAt(index int) []byte {
	if s == nil || index < 0 || index >= len(s.bodies) {
		return nil
	}
	return append([]byte(nil), s.bodies[index]...)
}

func (s *qoderAccountTestClientStub) bodyCount() int {
	if s == nil {
		return 0
	}
	return len(s.bodies)
}

type blockingQoderClientStub struct {
	t           *testing.T
	mu          sync.Mutex
	cond        *sync.Cond
	bodies      [][]byte
	headers     map[string]string
	firstWriter *io.PipeWriter
	firstDone   bool
	nextError   bool
}

func newBlockingQoderClientStub(t *testing.T) *blockingQoderClientStub {
	t.Helper()
	client := &blockingQoderClientStub{t: t}
	client.cond = sync.NewCond(&client.mu)
	return client
}

func (s *blockingQoderClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string) (*http.Response, error) {
	s.mu.Lock()
	callNumber := len(s.bodies) + 1
	s.bodies = append(s.bodies, append([]byte(nil), bodyJSON...))
	s.headers = headers
	s.cond.Broadcast()
	s.mu.Unlock()

	if callNumber == 1 {
		reader, writer := io.Pipe()
		s.mu.Lock()
		s.firstWriter = writer
		s.mu.Unlock()
		go func() {
			<-ctx.Done()
			_ = writer.Close()
		}()
		return &http.Response{StatusCode: http.StatusOK, Body: reader}, nil
	}
	s.mu.Lock()
	nextError := s.nextError
	s.nextError = false
	s.mu.Unlock()
	if nextError {
		body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\""
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
	body := "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
		"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":5,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":6}}\"}\n\n" +
		"data: {\"body\":\"[DONE]\"}\n\n"
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (s *blockingQoderClientStub) waitForCalls(count int) {
	s.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.bodies) < count {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out waiting for %d qoder calls, got %d", count, len(s.bodies))
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		s.mu.Lock()
	}
}

func (s *blockingQoderClientStub) finishFirst() {
	s.t.Helper()
	s.mu.Lock()
	writer := s.firstWriter
	if s.firstDone {
		writer = nil
	}
	s.firstDone = true
	s.mu.Unlock()
	require.NotNil(s.t, writer)
	_, err := io.WriteString(writer,
		"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n"+
			"data: {\"body\":\"{\\\"usage\\\":{\\\"prompt_tokens\\\":10,\\\"completion_tokens\\\":1,\\\"total_tokens\\\":11}}\"}\n\n"+
			"data: {\"body\":\"[DONE]\"}\n\n")
	require.NoError(s.t, err)
	require.NoError(s.t, writer.Close())
}

func (s *blockingQoderClientStub) bodyAt(index int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.bodies) {
		return nil
	}
	return append([]byte(nil), s.bodies[index]...)
}

func (s *blockingQoderClientStub) bodyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

func qoderLargeToolsJSONForTest() string {
	description := strings.Repeat("large schema field used by Claude Code. ", 80)
	body, err := json.Marshal([]map[string]any{
		{
			"name":        "Read",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{
						"type":        "string",
						"description": description,
					},
					"offset": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"file_path"},
			},
		},
		{
			"name":        "Bash",
			"description": description,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": description,
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": description,
					},
				},
				"required": []string{"command"},
			},
		},
	})
	if err != nil {
		panic(err)
	}
	return string(body)
}

type qoderAnthropicStreamEventForTest struct {
	Event string
	Data  map[string]any
}

func qoderAnthropicStreamEventsForTest(t *testing.T, stream string) []qoderAnthropicStreamEventForTest {
	t.Helper()
	events := make([]qoderAnthropicStreamEventForTest, 0)
	for _, block := range strings.Split(stream, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		event := qoderAnthropicStreamEventForTest{}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event.Data))
			}
		}
		if event.Event != "" {
			events = append(events, event)
		}
	}
	return events
}
