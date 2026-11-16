package requeststate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// These bootstrap text and delegation envelope fixtures provide supported
// injected-user input for candidate conversion and malformed-shape comparisons.
const (
	automationBootstrapPrompt = "Review the project and report any important changes."
	delegationEnvelope        = `<codex_delegation><source_thread_id>thread-1</source_thread_id><input>do the work</input></codex_delegation>`
)

// TestNormalizeCodexAutomationBootstrapSupportedLastRunValues supplies never,
// matching timestamp/epoch, and CRLF header forms. Conversion must retain exact
// output text in a user message and remove the tool-output call-ID field.
func TestNormalizeCodexAutomationBootstrapSupportedLastRunValues(t *testing.T) {
	tests := []struct {
		name    string // case label for diagnostics
		lastRun string // last-run header fixture text
		crlf    bool   // use CRLF header framing
	}{
		{name: "never", lastRun: "never"},
		{name: "timestamp", lastRun: "2026-09-01T02:06:34.536Z (1788228394536)"},
		{name: "crlf", lastRun: "never", crlf: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := codexAutomationBootstrap("wiki-maintenance", tt.lastRun, automationBootstrapPrompt)
			if tt.crlf {
				output = strings.ReplaceAll(output, "\n", "\r\n")
			}
			got, changed := NormalizeCodexAutomationBootstrap(codexAutomationBootstrapBody(t, output, ""))
			require.True(t, changed)
			require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
			require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
			require.Equal(t, output, gjson.GetBytes(got, "input.0.content.0.text").String())
			require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())
		})
	}
}

// TestNormalizeCodexAutomationBootstrapHeartbeat converts one supported
// heartbeat envelope and processes the result again. The first conversion must
// retain user text, while the already normalized message must remain unchanged
// on the second pass.
func TestNormalizeCodexAutomationBootstrapHeartbeat(t *testing.T) {
	output := `<heartbeat><automation_id>wiki</automation_id></heartbeat>`
	got, changed := NormalizeCodexAutomationBootstrap(codexAutomationBootstrapBody(t, output, ""))
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
	require.Equal(t, output, gjson.GetBytes(got, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())

	again, changedAgain := NormalizeCodexAutomationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

// TestNormalizeCodexAutomationBootstrapRejectsUnsafeShapes mixes wrong tool
// metadata, historical call context, malformed headers and mismatched
// identities. Every rejected fixture must retain its original bytes and false
// conversion flag.
func TestNormalizeCodexAutomationBootstrapRejectsUnsafeShapes(t *testing.T) {
	validOutput := codexAutomationBootstrap("wiki", "never", automationBootstrapPrompt)
	tests := []struct {
		name string // case label for diagnostics
		body []byte // JSON request fixture bytes
	}{
		{
			name: "ordinary missing call id",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"other","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "tui namespace",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_tui","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "valid call id",
			body: codexAutomationBootstrapBody(t, validOutput, `,"call_id":"call-1"`),
		},
		{
			name: "previous response",
			body: []byte(`{"model":"gpt-5","previous_response_id":"resp-1","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `}]}`),
		},
		{
			name: "real call context",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `},{"type":"function_call","call_id":"call-1"}]}`),
		},
		{
			name: "mismatched memory id",
			body: codexAutomationBootstrapBody(t, strings.Replace(validOutput, "/wiki/memory.md", "/other/memory.md", 1), ""),
		},
		{
			name: "unsafe automation id",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("../wiki", "never", automationBootstrapPrompt), ""),
		},
		{
			name: "invalid timestamp",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "yesterday", automationBootstrapPrompt), ""),
		},
		{
			name: "mismatched timestamp epoch",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "2026-09-01T02:06:34.536Z (1)", automationBootstrapPrompt), ""),
		},
		{
			name: "missing separator",
			body: codexAutomationBootstrapBody(t, strings.Replace(validOutput, "\n\n"+automationBootstrapPrompt, "\n"+automationBootstrapPrompt, 1), ""),
		},
		{
			name: "empty prompt",
			body: codexAutomationBootstrapBody(t, codexAutomationBootstrap("wiki", "never", " \n"), ""),
		},
		{
			name: "mixed missing call id output",
			body: []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, validOutput) + `},{"type":"computer_call_output","output":"done"}]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := NormalizeCodexAutomationBootstrap(tt.body)
			require.False(t, changed)
			require.Equal(t, tt.body, got)
		})
	}
}

// TestNormalizeCodexAutomationBootstrapRejectsUnsafeHeartbeatShapes supplies
// extra, nested, attributed, namespaced and malformed heartbeat envelopes.
// Bootstrap conversion must decline each shape and return the exact supplied
// body.
func TestNormalizeCodexAutomationBootstrapRejectsUnsafeHeartbeatShapes(t *testing.T) {
	tests := []struct {
		name   string // case label for diagnostics
		output string // tool output envelope fixture text
	}{
		{name: "arbitrary tool output", output: `<result><automation_id>wiki</automation_id></result>`},
		{name: "root attribute", output: `<heartbeat status="ok"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "namespaced root", output: `<heartbeat xmlns="urn:codex"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "extra child", output: `<heartbeat><automation_id>wiki</automation_id><status>ok</status></heartbeat>`},
		{name: "nested id content", output: `<heartbeat><automation_id><value>wiki</value></automation_id></heartbeat>`},
		{name: "padded id", output: `<heartbeat><automation_id> wiki </automation_id></heartbeat>`},
		{name: "unsafe id", output: `<heartbeat><automation_id>../wiki</automation_id></heartbeat>`},
		{name: "comment", output: `<heartbeat><!-- ok --><automation_id>wiki</automation_id></heartbeat>`},
		{name: "trailing content", output: `<heartbeat><automation_id>wiki</automation_id></heartbeat>extra`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := codexAutomationBootstrapBody(t, tt.output, "")
			got, changed := NormalizeCodexAutomationBootstrap(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}

// TestNormalizeCodexAutomationBootstrapPreservesOrderAndIsIdempotent surrounds
// a candidate with ordinary user messages. Conversion must retain their
// positions and text, then leave the normalized request unchanged on another
// pass.
func TestNormalizeCodexAutomationBootstrapPreservesOrderAndIsIdempotent(t *testing.T) {
	output := codexAutomationBootstrap("wiki", "never", automationBootstrapPrompt)
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"before"},{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSON(t, output) + `},{"type":"message","role":"user","content":"after"}]}`)

	got, changed := NormalizeCodexAutomationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "before", gjson.GetBytes(got, "input.0.content").String())
	require.Equal(t, output, gjson.GetBytes(got, "input.1.content.0.text").String())
	require.Equal(t, "after", gjson.GetBytes(got, "input.2.content").String())

	again, changedAgain := NormalizeCodexAutomationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

// TestNormalizeCodexDelegationBootstrapSupportedTools supplies both supported
// namespaces and tools with missing, empty or padded-blank call IDs. Conversion
// must produce a user message retaining the envelope text and removing call-ID
// metadata.
func TestNormalizeCodexDelegationBootstrapSupportedTools(t *testing.T) {
	tests := []struct {
		name      string // case label for diagnostics
		namespace string // exact tool namespace fixture
		tool      string // exact tool-name fixture spelling
		callID    string // raw candidate call-ID member fragment
	}{
		{name: "app create missing call id", namespace: "codex_app", tool: "create_thread"},
		{name: "app send empty call id", namespace: "codex_app", tool: "send_message_to_thread", callID: `,"call_id":""`},
		{name: "tui create whitespace call id", namespace: "codex_tui", tool: "create_thread", callID: `,"call_id":"  "`},
		{name: "tui send missing call id", namespace: "codex_tui", tool: "send_message_to_thread"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"` + tt.namespace + `","name":"` + tt.tool + `","output":"` + delegationEnvelope + `"` + tt.callID + `}]}`)
			got, changed := NormalizeCodexDelegationBootstrap(body)
			require.True(t, changed)
			require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
			require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
			require.Equal(t, delegationEnvelope, gjson.GetBytes(got, "input.0.content.0.text").String())
			require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())
		})
	}
}

// TestNormalizeCodexDelegationBootstrapRejectsUnsafeShapes combines conflicting
// call identity, ambiguous sibling context and padded discriminator fields.
// Conversion must decline every represented case and retain its exact original
// byte payload.
func TestNormalizeCodexDelegationBootstrapRejectsUnsafeShapes(t *testing.T) {
	tests := []struct {
		name  string // case label for diagnostics
		item  string // raw input-item JSON fragment
		extra string // raw request-level member fragment
	}{
		{name: "ordinary missing call id", item: `{"type":"function_call_output","name":"other","namespace":"codex_app","output":"` + delegationEnvelope + `"}`},
		{name: "valid call id", item: `{"type":"function_call_output","call_id":"call-1","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"}`},
		{name: "ambiguous call context", item: `{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"},{"type":"function_call"}`},
		{name: "ambiguous built-in call context", item: `{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"},{"type":"computer_call"}`},
		{name: "mixed missing call id output", item: `{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"},{"type":"computer_call_output","output":"done"}`},
		{name: "mixed tool search output", item: `{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"},{"type":"tool_search_output","output":"done"}`},
		{name: "mixed invalid delegation output", item: `{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"},{"type":"function_call_output","name":"create_thread","namespace":"codex_app","output":"not an envelope"}`},
		{name: "non string call id", item: `{"type":"function_call_output","call_id":42,"name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"}`},
		{name: "padded type", item: `{"type":" function_call_output ","name":"create_thread","namespace":"codex_app","output":"` + delegationEnvelope + `"}`},
		{name: "padded name", item: `{"type":"function_call_output","name":" create_thread ","namespace":"codex_app","output":"` + delegationEnvelope + `"}`},
		{name: "padded namespace", item: `{"type":"function_call_output","name":"create_thread","namespace":" codex_app ","output":"` + delegationEnvelope + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5","input":[` + tt.item + `]` + tt.extra + `}`)
			got, changed := NormalizeCodexDelegationBootstrap(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}

// TestNormalizeCodexDelegationBootstrapWithHistoricalContext mixes an injected
// candidate with identified call outputs and item references. Conversion must
// retain previous-response and historical identifiers while reclassifying the
// candidate at its original position.
func TestNormalizeCodexDelegationBootstrapWithHistoricalContext(t *testing.T) {
	body := []byte(`{"model":"gpt-5","previous_response_id":"resp-1","input":[{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":"` + delegationEnvelope + `"},{"type":"function_call","call_id":"call-1"},{"type":"function_call_output","call_id":"call-1","output":"done"},{"type":"item_reference","id":"item-1"},{"type":"computer_call","call_id":"call-2"}]}`)

	got, changed := NormalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
	require.Equal(t, "resp-1", gjson.GetBytes(got, "previous_response_id").String())
	require.Equal(t, "call-1", gjson.GetBytes(got, "input.2.call_id").String())
	require.Equal(t, "item-1", gjson.GetBytes(got, "input.3.id").String())
}

// TestNormalizeCodexDelegationBootstrapPreviousResponseID supplies omitted,
// blank, nonblank and nonstring continuation evidence. Delegation conversion
// must accept the string/omitted forms and retain original bytes for
// incompatible field types.
func TestNormalizeCodexDelegationBootstrapPreviousResponseID(t *testing.T) {
	tests := []struct {
		name    string // case label for diagnostics
		value   string // raw previous-response member fragment
		changed bool   // expected bootstrap conversion flag
	}{
		{name: "absent", changed: true},
		{name: "blank string", value: `,"previous_response_id":"  "`, changed: true},
		{name: "non-empty string", value: `,"previous_response_id":"resp-1"`, changed: true},
		{name: "null", value: `,"previous_response_id":null`},
		{name: "number", value: `,"previous_response_id":42`},
		{name: "boolean", value: `,"previous_response_id":false`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"` + delegationEnvelope + `"}]` + tt.value + `}`)
			got, changed := NormalizeCodexDelegationBootstrap(body)
			require.Equal(t, tt.changed, changed)
			if !tt.changed {
				require.Equal(t, body, got)
			}
		})
	}
}

// TestNormalizeCodexDelegationBootstrapRejectsDuplicateMembers supplies
// duplicate continuation, discriminator, call-ID and output members. The
// uniqueness guard must decline conversion before object rewriting can select
// one conflicting value.
func TestNormalizeCodexDelegationBootstrapRejectsDuplicateMembers(t *testing.T) {
	tests := []struct {
		name string // case label for diagnostics
		body string // JSON request fixture bytes
	}{
		{
			name: "top-level previous response id",
			body: `{"model":"gpt-5","previous_response_id":"","previous_response_id":"resp-1","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"` + delegationEnvelope + `"}]}`,
		},
		{
			name: "candidate discriminator",
			body: `{"model":"gpt-5","input":[{"type":"message","type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"` + delegationEnvelope + `"}]}`,
		},
		{
			name: "candidate call id",
			body: `{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","call_id":"call-1","call_id":"","output":"` + delegationEnvelope + `"}]}`,
		},
		{
			name: "candidate output",
			body: `{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"not a delegation","output":"` + delegationEnvelope + `"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.body)
			got, changed := NormalizeCodexDelegationBootstrap(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}

// TestNormalizeCodexDelegationBootstrapPreservesExactNumbers converts a
// supported candidate beside an integer exceeding exact float64 precision. The
// reencoded request must retain that integer's source numeric spelling through
// json.Number decoding.
func TestNormalizeCodexDelegationBootstrapPreservesExactNumbers(t *testing.T) {
	body := []byte(`{"model":"gpt-5","metadata":{"integer":9007199254740993},"input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"` + delegationEnvelope + `"}]}`)
	got, changed := NormalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "metadata.integer").Raw)
}

// TestNormalizeCodexDelegationBootstrapRequiresCompleteEnvelope supplies
// missing/empty children, outside text, extra elements and namespace variants.
// Every incomplete or adorned delegation shape must remain unchanged with
// conversion declined.
func TestNormalizeCodexDelegationBootstrapRequiresCompleteEnvelope(t *testing.T) {
	malformed := []string{
		`<codex_delegation><source_thread_id>thread-1</source_thread_id></codex_delegation>`,
		`<codex_delegation><source_thread_id></source_thread_id><input>work</input></codex_delegation>`,
		`<codex_delegation><source_thread_id>thread-1</source_thread_id><input> </input></codex_delegation>`,
		`prefix<codex_delegation><source_thread_id>thread-1</source_thread_id><input>work</input></codex_delegation>`,
		`<codex_delegation><source_thread_id>thread-1</source_thread_id><input>work</input></codex_delegation>suffix`,
		`<codex_delegation><source_thread_id>thread-1</source_thread_id><input>work</input><extra>x</extra></codex_delegation>`,
		`<codex_delegation><source_thread_id>thread-1</source_thread_id><input><nested>work</nested></input></codex_delegation>`,
		`<x:codex_delegation xmlns:x="urn:test"><source_thread_id>thread-1</source_thread_id><input>work</input></x:codex_delegation>`,
		`<x:codex_delegation><source_thread_id>thread-1</source_thread_id><input>work</input></x:codex_delegation>`,
		`<codex_delegation xmlns="urn:test"><source_thread_id>thread-1</source_thread_id><input>work</input></codex_delegation>`,
		`<codex_delegation xmlns:x="urn:test"><x:source_thread_id>thread-1</x:source_thread_id><input>work</input></codex_delegation>`,
		`<codex_delegation><x:source_thread_id>thread-1</x:source_thread_id><input>work</input></codex_delegation>`,
		`<codex_delegation xmlns=""><source_thread_id>thread-1</source_thread_id><input>work</input></codex_delegation>`,
	}
	for _, envelope := range malformed {
		body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSON(t, envelope) + `}]}`)
		got, changed := NormalizeCodexDelegationBootstrap(body)
		require.False(t, changed, envelope)
		require.Equal(t, body, got)
	}
}

// TestNormalizeCodexDelegationBootstrapPreservesOrderAndIsIdempotent surrounds
// a delegation candidate with ordinary messages. Their ordering and text must
// survive conversion, and a second normalization must retain the already
// converted bytes.
func TestNormalizeCodexDelegationBootstrapPreservesOrderAndIsIdempotent(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":"before"},{"type":"function_call_output","namespace":"codex_tui","name":"send_message_to_thread","output":"` + delegationEnvelope + `"},{"type":"message","role":"user","content":"after"}]}`)
	got, changed := NormalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "before", gjson.GetBytes(got, "input.0.content").String())
	require.Equal(t, delegationEnvelope, gjson.GetBytes(got, "input.1.content.0.text").String())
	require.Equal(t, "after", gjson.GetBytes(got, "input.2.content").String())

	again, changedAgain := NormalizeCodexDelegationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

// TestNormalizeCodexBootstrapsCombinedPassAndZeroAllocClean supplies automation
// and delegation candidates, escaped keys, duplicate keys, and a clean body.
// Combined normalization preserves automation's bootstrap-only history rule
// before delegation runs, while clean bodies allocate zero heap objects.
func TestNormalizeCodexBootstrapsCombinedPassAndZeroAllocClean(t *testing.T) {
	autoOut := codexAutomationBootstrap("wiki", "never", automationBootstrapPrompt)
	autoBody := codexAutomationBootstrapBody(t, autoOut, "")
	autoGot, autoChanged, delegChanged := NormalizeCodexBootstraps(autoBody)
	require.True(t, autoChanged)
	require.False(t, delegChanged)
	require.Equal(t, "message", gjson.GetBytes(autoGot, "input.0.type").String())

	mixed := []byte(`{"model":"gpt-5","input":[` +
		`{"type":"function_call_output","call_id":"call-1","output":"ok"},` +
		`{"type":"function_call_output","namespace":"codex_tui","name":"send_message_to_thread","output":` + mustJSON(t, delegationEnvelope) + `}` +
		`]}`)
	got, mixedAutoChanged, mixedDelegChanged := NormalizeCodexBootstraps(mixed)
	require.False(t, mixedAutoChanged)
	require.True(t, mixedDelegChanged)
	require.Equal(t, "function_call_output", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "message", gjson.GetBytes(got, "input.1.type").String())

	escaped := []byte(`{"model":"gpt-5","input":[{"type":"function_\u0063all_output","namespace":"codex_tui","name":"send_message_to_thread","output":` + mustJSON(t, delegationEnvelope) + `}]}`)
	escapedGot, escapedAuto, escapedDeleg := NormalizeCodexBootstraps(escaped)
	require.False(t, escapedAuto)
	require.True(t, escapedDeleg)
	require.Equal(t, "message", gjson.GetBytes(escapedGot, "input.0.type").String())

	dupKey := []byte(`{"model":"gpt-5","model":"gpt-5-mini","input":[{"type":"function_call_output","namespace":"codex_tui","name":"send_message_to_thread","output":` + mustJSON(t, delegationEnvelope) + `}]}`)
	dupGot, dupAuto, dupDeleg := NormalizeCodexBootstraps(dupKey)
	require.False(t, dupAuto)
	require.False(t, dupDeleg)
	require.Equal(t, dupKey, dupGot)

	clean := buildSyntheticBootstrapHistory(64 * 1024)
	allocs := testing.AllocsPerRun(20, func() {
		out, a, d := NormalizeCodexBootstraps(clean)
		if a || d || len(out) != len(clean) {
			t.Fatal("unexpected bootstrap change on clean payload")
		}
	})
	require.Zero(t, allocs)
}

// BenchmarkNormalizeCodexBootstraps_NoCandidate measures combined bootstrap
// screening across 1 MiB, 5 MiB, 20 MiB, and 32 MiB clean Responses payloads.
// Each sub-benchmark reports byte throughput and confirms zero heap allocations.
func BenchmarkNormalizeCodexBootstraps_NoCandidate(b *testing.B) {
	for _, sizeMiB := range []int{1, 5, 20, 32} {
		payload := buildSyntheticBootstrapHistory(sizeMiB << 20)
		b.Run(strings.TrimSpace(jsonNumberLabel(sizeMiB))+"MiB", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for range b.N {
				_, _, _ = NormalizeCodexBootstraps(payload)
			}
		})
	}
}

// buildSyntheticBootstrapHistory constructs a valid multi-turn Responses JSON
// payload of approximately targetBytes with paired function calls and outputs.
func buildSyntheticBootstrapHistory(targetBytes int) []byte {
	out := make([]byte, 0, targetBytes+4096)
	out = append(out, `{"model":"gpt-5.4","stream":true,"input":[`...)
	chunk := strings.Repeat("code review context line with identifiers and parameters ", 32)
	turn := 0
	for len(out) < targetBytes {
		if turn > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"type":"message","role":"user","content":[{"type":"input_text","text":"`...)
		out = append(out, chunk...)
		out = append(out, `"}]},{"type":"function_call","call_id":"call_`...)
		out = append(out, jsonNumberLabel(turn)...)
		out = append(out, `","name":"read_file","arguments":"{\"path\":\"main.go\"}"},{"type":"function_call_output","call_id":"call_`...)
		out = append(out, jsonNumberLabel(turn)...)
		out = append(out, `","output":"`...)
		out = append(out, chunk...)
		out = append(out, `"}`...)
		turn++
	}
	out = append(out, `]}`...)
	return out
}

// jsonNumberLabel formats a small nonnegative integer for synthetic fixture IDs.
func jsonNumberLabel(v int) string {
	encoded, _ := json.Marshal(v)
	return string(encoded)
}

// codexAutomationBootstrap builds header text using the supplied ID, last-run
// value, and prompt. It validates none of them, allowing tests to construct
// both supported headers and deliberate identity/date framing failures.
func codexAutomationBootstrap(automationID, lastRun, prompt string) string {
	return "Automation: Scheduled project review\n" +
		"Automation ID: " + automationID + "\n" +
		"Automation memory: $CODEX_HOME/automations/" + automationID + "/memory.md\n" +
		"Last run: " + lastRun + "\n\n" + prompt
}

// codexAutomationBootstrapBody builds a function-call-output request around
// JSON-encoded output text and a caller-supplied raw call-ID fragment. It marks
// itself as a helper and relies on mustJSON to encode the text safely.
func codexAutomationBootstrapBody(t *testing.T, output, callID string) []byte {
	t.Helper()
	return []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` +
		mustJSON(t, output) + callID + `}]}`)
}

// mustJSON serializes a fixture string and fails the test when encoding fails.
// It marks itself as a helper and returns the quoted JSON spelling for
// insertion into raw request fixtures.
func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}
