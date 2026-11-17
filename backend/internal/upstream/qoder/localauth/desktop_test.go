package localauth

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Desktop live suites share a single consumed credential pipe during one test
// process; its records remain private and are never serialized into diagnostics.
var (
	desktopInputOnce   sync.Once
	desktopInputValues map[string]desktopTestInput
	desktopInputError  error
)

// desktopTestInput transfers an already authorized desktop login over a pipe.
// Live tests never require credential files to be copied into the checkout.
type desktopTestInput struct {
	Data      desktopCredential `json:"data"`      // in-memory desktop login
	MachineID string            `json:"machineID"` // saved desktop machine
}

// desktopProbeSink retains synthetic test output without logging response text.
type desktopProbeSink struct {
	body     bytes.Buffer // retained synthetic response bytes
	bytes    int          // observed response bytes
	semantic bool         // observed semantic output
}

// TestDecodeDesktopCredential verifies the actual macOS v10 format with a
// synthetic credential. It rejects wrong keys, malformed padding, and a
// cross-site login without exposing any credential text in errors.
func TestDecodeDesktopCredential(t *testing.T) {
	saved := desktopCredential{SchemaVersion: 1, AuthenticationContext: `["https://qoder.com","https://openapi.qoder.sh","client","prod"]`, Token: "test-access", RefreshToken: "test-refresh", ExpiresAt: "2026-12-17T14:56:51Z"}
	saved.User.ID = "test-user"
	body, err := json.Marshal(saved)
	require.NoError(t, err)
	key, err := pbkdf2.Key(sha1.New, "test-password", []byte("saltysalt"), 1003, 16)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	padding := aes.BlockSize - len(body)%aes.BlockSize
	body = append(body, bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(body)+3)
	copy(encrypted, "v10")
	cipher.NewCBCEncrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(encrypted[3:], body)
	info, err := decodeDesktopCredential(encrypted, []byte("test-password"), "test-machine", qoder.SiteGlobal)
	require.NoError(t, err)
	require.Equal(t, saved.Token, info.SecurityOauthToken)
	require.Equal(t, saved.User.ID, info.UID)
	require.Positive(t, info.ExpireTime)
	_, err = decodeDesktopCredential(encrypted, []byte("test-password"), "test-machine", qoder.SiteCN)
	require.ErrorContains(t, err, "selected site")
	_, err = decodeDesktopCredential(encrypted, []byte("wrong-password"), "test-machine", qoder.SiteGlobal)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "test-access")
	_, err = decodeDesktopCredential(encrypted[:len(encrypted)-1], []byte("test-password"), "test-machine", qoder.SiteGlobal)
	require.ErrorContains(t, err, "encoding")
}

// TestDesktopModelCatalog compares both authenticated desktop accounts with
// the native Go model query. Explicit opt-in is required, and an inherited
// pipe can supply credentials without repeating interactive Keychain access.
func TestDesktopModelCatalog(t *testing.T) {
	inputs := desktopInputs(t)
	for _, site := range []qoder.Site{qoder.SiteGlobal, qoder.SiteCN} {
		t.Run(string(site), func(t *testing.T) {
			session, client, models := desktopAccount(t, site, inputs)
			for _, model := range models {
				t.Logf("route=%s alias=%s enabled=%t default_effort=%s input=%d", model.Key, model.Alias, model.Enabled, model.DefaultEffort, model.InputLimit)
				if selected := os.Getenv("QODER_DESKTOP_INFERENCE"); selected == "all" || selected == model.Key {
					t.Run(model.Key, func(t *testing.T) { desktopInference(t, model, site, session, client) })
				}
			}
		})
	}
}

// desktopInputs reads opt-in credentials once for the current test process.
// An inherited pipe is consumed and closed only once, allowing separately
// selected live suites to share its in-memory records without writing secrets.
func desktopInputs(t *testing.T) map[string]desktopTestInput {
	t.Helper()
	if os.Getenv("QODER_RUN_REAL_API_TESTS") != "1" {
		t.Skip("set QODER_RUN_REAL_API_TESTS=1 to query desktop accounts")
	}
	desktopInputOnce.Do(func() {
		desktopInputValues = map[string]desktopTestInput{}
		if descriptor := os.Getenv("QODER_DESKTOP_AUTH_FD"); descriptor != "" {
			fd, err := strconv.Atoi(descriptor)
			if err != nil || fd < 3 {
				desktopInputError = errors.New("invalid desktop credential descriptor")
				return
			}
			pipe := os.NewFile(uintptr(fd), "qoder-desktop-auth")
			if pipe == nil {
				desktopInputError = errors.New("desktop credential pipe is unavailable")
				return
			}
			defer func() { _ = pipe.Close() }()
			desktopInputError = json.NewDecoder(pipe).Decode(&desktopInputValues)
		}
	})
	require.NoError(t, desktopInputError)
	return desktopInputValues
}

// desktopAccount prepares one site's native credentials and current catalog.
// Supplied in-memory records avoid another Keychain prompt. Userinfo and plan
// metadata are queried through the same production OpenAPI implementation used
// by desktop authorization, and returned clients belong to this test suite.
func desktopAccount(t *testing.T, site qoder.Site, inputs map[string]desktopTestInput) (*qoder.SessionContext, *qoder.Client, []qodermodel.Model) {
	t.Helper()
	var info *qoder.AuthInfo
	var err error
	if input, found := inputs[string(site)]; found {
		info, err = input.Data.authInfo(input.MachineID, site)
	} else {
		info, err = ReadDesktopAuth(t.Context(), site)
	}
	require.NoError(t, err)
	profile := qoder.MustProfileForSite(site)
	oauth := qoder.NewOAuthClientForProfile(profile, nil)
	user, err := oauth.GetUserInfo(t.Context(), info.SecurityOauthToken)
	require.NoError(t, err)
	identity := qoder.BuildIdentityFromDeviceToken(user, &qoder.DeviceTokenResponse{Token: info.SecurityOauthToken, RefreshToken: info.RefreshToken})
	accountType, err := oauth.GetAccountType(t.Context(), info.SecurityOauthToken)
	require.NoError(t, err)
	if accountType != "" {
		identity.UserType = accountType
	}
	session, err := qoder.NewSessionForProfileWithKey(identity, &qoder.MachineIdentity{MachineID: info.MachineID}, profile, nil)
	require.NoError(t, err)
	client := qoder.NewClientForProfile(profile)
	client.HTTPClient.Timeout = 75 * time.Second
	models, err := client.ListModels(t.Context(), session, nil)
	require.NoError(t, err)
	require.NotEmpty(t, models)
	return session, client, models
}

// Begin accepts metadata for a diagnostic response without exposing headers.
func (s *desktopProbeSink) Begin(upstream.OutputHead) error { return nil }

// Emit records response size and semantic commitment from the real converter.
func (s *desktopProbeSink) Emit(event upstream.OutputEvent) error {
	_, _ = s.body.Write(event.Data)
	s.bytes += len(event.Data)
	s.semantic = s.semantic || event.Semantic
	return nil
}

// desktopInference sends a minimal synthetic request through the native executor.
// The observed account model controls request defaults; entitlement failures are
// reported separately from protocol errors, and no prompt contains project data.
func desktopInference(t *testing.T, model qodermodel.Model, site qoder.Site, session *qoder.SessionContext, client *qoder.Client) {
	t.Helper()
	client.HTTPClient.Timeout = 75 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	resolution := modelrules.View{}.Resolve(modelrules.Target{Platform: "qoder", Site: string(site), Model: model.Key, ProviderModels: qodermodel.NewCatalog(string(site), []qodermodel.Model{model}), ClientProtocol: "openai_chat_completions"}, modelcatalog.Attributes{})
	body, err := json.Marshal(map[string]any{"model": model.Key, "messages": []map[string]string{{"role": "user", "content": "Write a Python function add(a, b) that returns a + b. Output only the function."}}, "max_tokens": 256, "stream": true})
	require.NoError(t, err)
	target := &qoder.Target{
		Site: site, ProviderID: 1,
		Session: func(context.Context) (*qoder.SessionContext, error) { return session, nil },
		Client:  func() (qoder.StreamClient, error) { return client, nil },
		Attributes: func(context.Context, string, protocol.ProtocolID) (modelrules.Resolution, error) {
			return resolution, nil
		},
	}
	sink := &desktopProbeSink{}
	result, err := qoder.NewExecutor(qoder.ExecuteOptions{Timeout: 75 * time.Second}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolOpenAIChatCompletions, Body: body, ResponseModel: model.Key, Stream: true, Target: target}, sink)
	if err != nil {
		if apiErr, ok := errors.AsType[*qoder.APIError](err); ok {
			t.Logf("live_result site=%s route=%s enabled=%t status=%d code=%s served=%t usage=%t", site, model.Key, model.Enabled, apiErr.StatusCode, apiErr.Code, result.Served, result.HasUsage)
			if apiErr.IsEntitlementDenied() || apiErr.IsAgentLimit() || apiErr.StatusCode == 429 {
				t.Skip("upstream account entitlement or quota prevents inference verification")
			}
		}
		require.NoError(t, err)
	}
	t.Logf("live_result site=%s route=%s enabled=%t status=success served=%t usage=%t bytes=%d", site, model.Key, model.Enabled, result.Served, result.HasUsage, sink.bytes)
	require.True(t, result.Served)
	require.True(t, result.HasUsage)
	require.Positive(t, sink.bytes)
}

// TestDesktopProtocolScenarios exercises all public text protocols with current
// account capabilities. It verifies streaming and buffered output, explicit
// reasoning choices, and a parallel tool-result continuation using synthetic tools.
func TestDesktopProtocolScenarios(t *testing.T) {
	if os.Getenv("QODER_DESKTOP_INFERENCE") == "" {
		t.Skip("select a model with QODER_DESKTOP_INFERENCE for live protocol scenarios")
	}
	inputs := desktopInputs(t)
	for _, site := range []qoder.Site{qoder.SiteGlobal, qoder.SiteCN} {
		t.Run(string(site), func(t *testing.T) {
			session, client, models := desktopAccount(t, site, inputs)
			var model qodermodel.Model
			for _, entry := range models {
				if entry.Key == "qfmodel" {
					model = entry
				}
			}
			require.NotEmpty(t, model.Key)
			for _, wire := range []protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolOpenAIResponses, protocol.ProtocolAnthropicMessages} {
				for _, stream := range []bool{false, true} {
					name := string(wire) + "/buffered"
					if stream {
						name = string(wire) + "/stream"
					}
					t.Run(name, func(t *testing.T) {
						body := desktopProtocolBody(wire, model.Key, stream, "Write a Python function add(a,b) that returns a+b.")
						if wire == protocol.ProtocolOpenAIResponses {
							body["reasoning"] = map[string]any{"effort": "none"}
						}
						if wire == protocol.ProtocolAnthropicMessages {
							body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": 1}
						}
						if wire == protocol.ProtocolOpenAIChatCompletions {
							body["reasoning_effort"] = "high"
						}
						output := desktopProtocolCall(t, qoder.NewExecutor(qoder.ExecuteOptions{}), model, site, session, client, wire, body)
						require.Contains(t, string(output), "add")
					})
				}
				t.Run(string(wire)+"/parallel_tools", func(t *testing.T) { desktopToolContinuation(t, model, site, session, client, wire, false) })
				if wire == protocol.ProtocolOpenAIResponses {
					t.Run(string(wire)+"/streamed_tools", func(t *testing.T) { desktopToolContinuation(t, model, site, session, client, wire, true) })
				}
			}
		})
	}
}

// desktopProtocolBody creates a minimal programming request in one public format.
// Each caller receives independent maps and can add reasoning or tool controls;
// the request contains only synthetic text and a bounded output budget.
func desktopProtocolBody(wire protocol.ProtocolID, model string, stream bool, prompt string) map[string]any {
	body := map[string]any{"model": model, "stream": stream, "max_tokens": 256}
	if wire == protocol.ProtocolOpenAIResponses {
		delete(body, "max_tokens")
		body["max_output_tokens"] = 256
		body["input"] = []any{map[string]any{"role": "user", "content": prompt}}
	} else {
		body["messages"] = []any{map[string]any{"role": "user", "content": prompt}}
	}
	return body
}

// desktopProtocolCall executes one synthetic request and retains its wire output.
// Account entitlement and quota errors produce an explicit skipped scenario;
// other failures fail the test. The supplied executor can retain conversation
// identity across a tool continuation, while each call owns its output buffer.
func desktopProtocolCall(t *testing.T, executor *qoder.Executor, model qodermodel.Model, site qoder.Site, session *qoder.SessionContext, client *qoder.Client, wire protocol.ProtocolID, body map[string]any) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()
	resolution := modelrules.View{}.Resolve(modelrules.Target{Platform: "qoder", Site: string(site), Model: model.Key, ProviderModels: qodermodel.NewCatalog(string(site), []qodermodel.Model{model}), ClientProtocol: string(wire)}, modelcatalog.Attributes{})
	target := &qoder.Target{ProviderID: 1, Site: site, Session: func(context.Context) (*qoder.SessionContext, error) { return session, nil }, Client: func() (qoder.StreamClient, error) { return client, nil }, Attributes: func(context.Context, string, protocol.ProtocolID) (modelrules.Resolution, error) {
		return resolution, nil
	}}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	sink := &desktopProbeSink{}
	stream, _ := body["stream"].(bool)
	result, err := executor.Execute(ctx, upstream.AttemptInput{Protocol: wire, Body: raw, ResponseModel: model.Key, Stream: stream, Target: target}, sink)
	if err != nil {
		if apiErr, ok := errors.AsType[*qoder.APIError](err); ok && (apiErr.IsEntitlementDenied() || apiErr.IsAgentLimit() || apiErr.StatusCode == 429) {
			t.Logf("live_protocol site=%s protocol=%s status=%d code=%s", site, wire, apiErr.StatusCode, apiErr.Code)
			t.Skip("upstream entitlement or quota prevents this scenario")
		}
		require.NoError(t, err)
	}
	require.True(t, result.Served)
	require.True(t, result.HasUsage)
	require.Positive(t, sink.bytes)
	t.Logf("live_protocol site=%s protocol=%s stream=%t status=success usage=true bytes=%d", site, wire, stream, sink.bytes)
	return append([]byte(nil), sink.body.Bytes()...)
}

// desktopToolContinuation asks for two synthetic tool calls and replays both
// results in the same public protocol. Distinct nonempty IDs and valid argument
// objects are required before continuing, and no real tool or project file is
// accessed. The final response must contain the requested programming result.
func desktopToolContinuation(t *testing.T, model qodermodel.Model, site qoder.Site, session *qoder.SessionContext, client *qoder.Client, wire protocol.ProtocolID, stream bool) {
	t.Helper()
	prompt := "Before writing a Python function concat(), call diagnostic_echo twice in parallel, with value left and value right. After both tool results, write a Python function that returns their concatenation."
	body := desktopProtocolBody(wire, model.Key, stream, prompt)
	parameters := map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}, "additionalProperties": false}
	function := map[string]any{"name": "diagnostic_echo", "description": "Return a synthetic string constant for a Python coding test.", "parameters": parameters}
	switch wire {
	case protocol.ProtocolOpenAIChatCompletions:
		body["tools"] = []any{map[string]any{"type": "function", "function": function}}
		body["tool_choice"] = "required"
	case protocol.ProtocolOpenAIResponses:
		body["tools"] = []any{map[string]any{"type": "function", "name": "diagnostic_echo", "description": function["description"], "parameters": parameters}}
		body["tool_choice"] = "required"
	case protocol.ProtocolAnthropicMessages:
		body["tools"] = []any{map[string]any{"name": "diagnostic_echo", "description": function["description"], "input_schema": parameters}}
		body["tool_choice"] = map[string]any{"type": "any"}
	}
	executor := qoder.NewExecutor(qoder.ExecuteOptions{})
	first := desktopProtocolCall(t, executor, model, site, session, client, wire, body)
	if stream {
		var completed json.RawMessage
		for line := range strings.SplitSeq(string(first), "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var event struct {
				Type     string          `json:"type"`     // Responses event kind
				Response json.RawMessage `json:"response"` // complete response object
			}
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) == nil && event.Type == "response.completed" {
				completed = event.Response
			}
		}
		require.NotEmpty(t, completed)
		first = completed
	}
	body["stream"] = false
	var response map[string]any
	require.NoError(t, json.Unmarshal(first, &response))
	ids := map[string]bool{}
	results := []any{}
	switch wire {
	case protocol.ProtocolOpenAIChatCompletions:
		choices := desktopFixtureValue[[]any](t, response["choices"])
		message := desktopFixtureValue[map[string]any](t, desktopFixtureValue[map[string]any](t, choices[0])["message"])
		calls, _ := message["tool_calls"].([]any)
		require.Len(t, calls, 2)
		for _, rawCall := range calls {
			call := desktopFixtureValue[map[string]any](t, rawCall)
			id := desktopFixtureValue[string](t, call["id"])
			require.NotEmpty(t, id)
			require.False(t, ids[id])
			ids[id] = true
			args := desktopFixtureValue[string](t, desktopFixtureValue[map[string]any](t, call["function"])["arguments"])
			var value map[string]any
			require.NoError(t, json.Unmarshal([]byte(args), &value))
			results = append(results, map[string]any{"role": "tool", "tool_call_id": id, "content": value["value"]})
		}
		body["messages"] = append(append(desktopFixtureValue[[]any](t, body["messages"]), message), results...)
		body["tool_choice"] = "none"
	case protocol.ProtocolOpenAIResponses:
		items := desktopFixtureValue[[]any](t, response["output"])
		for _, rawItem := range items {
			item := desktopFixtureValue[map[string]any](t, rawItem)
			if item["type"] != "function_call" {
				continue
			}
			id := desktopFixtureValue[string](t, item["call_id"])
			require.NotEmpty(t, id)
			require.False(t, ids[id])
			ids[id] = true
			var value map[string]any
			require.NoError(t, json.Unmarshal([]byte(desktopFixtureValue[string](t, item["arguments"])), &value))
			results = append(results, map[string]any{"type": "function_call_output", "call_id": id, "output": value["value"]})
		}
		require.Len(t, ids, 2)
		body["input"] = append(append(desktopFixtureValue[[]any](t, body["input"]), items...), results...)
		body["store"] = false
		body["tool_choice"] = "none"
	case protocol.ProtocolAnthropicMessages:
		items := desktopFixtureValue[[]any](t, response["content"])
		for _, rawItem := range items {
			item := desktopFixtureValue[map[string]any](t, rawItem)
			if item["type"] != "tool_use" {
				continue
			}
			id := desktopFixtureValue[string](t, item["id"])
			require.NotEmpty(t, id)
			require.False(t, ids[id])
			ids[id] = true
			value := desktopFixtureValue[map[string]any](t, item["input"])
			results = append(results, map[string]any{"type": "tool_result", "tool_use_id": id, "content": value["value"]})
		}
		require.Len(t, ids, 2)
		body["messages"] = append(desktopFixtureValue[[]any](t, body["messages"]), map[string]any{"role": "assistant", "content": items}, map[string]any{"role": "user", "content": results})
		body["tool_choice"] = map[string]any{"type": "none"}
	}
	output := desktopProtocolCall(t, executor, model, site, session, client, wire, body)
	if !strings.Contains(string(output), "concat") && !strings.Contains(string(output), "leftright") {
		t.Logf("synthetic_continuation_output=%s", qoder.TruncateRunes(string(output), 2048))
	}
	require.True(t, strings.Contains(string(output), "concat") || strings.Contains(string(output), "leftright"))
	t.Logf("live_tools site=%s protocol=%s calls=%d continuation=success", site, wire, len(ids))
}

// desktopFixtureValue requires one decoded response field to have its expected
// wire type. A mismatch stops the scenario before it constructs another request
// from malformed model output or associates tool results with invalid identities.
func desktopFixtureValue[T any](t *testing.T, value any) T {
	t.Helper()
	result, ok := value.(T)
	require.True(t, ok, "unexpected diagnostic response type %T", value)
	return result
}
