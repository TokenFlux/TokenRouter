# Qoder / opencode / Claude Code handoff - 2026-06-16

## Goal

Fix TokenRouter Qoder gateway behavior until real opencode and real Claude Code usage are confirmed usable end-to-end. The finish line is not unit tests alone: opencode and Claude Code must actually work through TokenRouter without tool-call failures and without Claude Code cache being systematically missed.

## Current user-reported problems

1. Claude Code + `claude-opus-4-6` / Qoder `ultimate` still completely misses cache in real usage.
   - The user suspects this is a Claude Code client characteristic that must be handled at the gateway layer.
   - Other upstream entries in this project have special Claude Code handling to avoid cache invalidation. Study those paths and port the relevant behavior into the Qoder gateway instead of assuming generic prompt-cache-key/session handling is enough.

2. opencode now works with OpenAI Chat Completions protocol, but still has problems with:
   - Anthropic Messages protocol.
   - OpenAI Responses protocol.

3. End condition for the next work session:
   - opencode works through all intended protocols, especially Anthropic and OpenAI Responses, without tool-call format/name/streaming issues.
   - Claude Code works through Qoder/ultimate and cache can actually be created/read in real usage.
   - The agent can explain the root causes, the fix, and the evidence.

## Repository and working tree

Repo root: `/home/ycyc/projects/TokenRouter`

At handoff time the working tree is already dirty with many user/session changes. Do not reset, clean, commit, or overwrite unrelated files. Re-run `git status --short` before starting.

Relevant files likely include:

- `backend/internal/service/qoder_gateway_service.go`
- `backend/internal/service/qoder_gateway_service_test.go`
- `backend/internal/pkg/apicompat/types.go`
- OpenAI Responses / compatibility code under `backend/internal/pkg/apicompat` and `backend/internal/service`
- Existing Claude Code / cache special handling in non-Qoder paths. Search for:
  - `Claude Code`
  - `claude-code`
  - `x-anthropic-billing-header`
  - `cch=`
  - `cache_control`
  - `cache_read_input_tokens`
  - `prompt_cache_key`
  - `enable_anthropic_cache_ttl_1h_injection`
  - `rewrite_message_cache_control`
  - `SettingKeyEnableCCHSigning`

Existing Qoder doc:

- `docs/qoder-native-upstream.md`

A previous session added Qoder tool-name remapping for OpenAI Chat, but do not assume it is sufficient for Anthropic or Responses.

## Required workflow

1. Research current state first.
   - Read the relevant files before editing.
   - Compare Qoder gateway behavior with the existing Anthropic/OpenAI gateway behavior for Claude Code cache preservation.
   - Trace protocol conversions: OpenAI Chat, Anthropic Messages, OpenAI Responses.
   - Identify where tool names, tool results, tool IDs, streaming deltas, cache_control, system prompts, and session keys are transformed.

2. Write a short plan before coding.
   - Include hypotheses for Claude Code cache miss.
   - Include hypotheses for opencode Anthropic/Responses tool-call failures.
   - Include concrete tests and real-client probes.

3. Fix incrementally.
   - Prefer targeted tests that reproduce the exact protocol problem.
   - Keep changes scoped to Qoder gateway / protocol compatibility unless evidence requires wider changes.
   - Preserve marketplace/UI and unrelated WIP.

4. Validate with both automated tests and real usage.
   - Automated tests should include protocol-level regression tests for:
     - opencode-style OpenAI Chat tool calls.
     - opencode-style Anthropic Messages tool calls.
     - opencode-style OpenAI Responses tool calls.
     - Claude Code cache-stable multi-turn requests through Qoder/ultimate.
   - Real usage validation is required before claiming done.
   - If needed, run minimal opencode / Claude Code live probes against a local TokenRouter instance.
   - If results are inconclusive, capture minimal sanitized request/response traces or redo packet capture.

5. Report clearly.
   - Root cause(s).
   - Files changed.
   - Verification commands and real-client evidence.
   - Remaining risks or configuration needed.

## External coding agents / coordination rules

The user allows Codex as an assistant, but avoid context blowups and CLI deadlocks.

If using Codex / opencode / Claude Code for implementation, review, or probes:

- Use bounded prompts.
- Prefer one-shot commands.
- Keep stdout minimal.
- Require the worker to write full results to a document file, not stdout.
- Example output contract:
  - stdout: only `DONE path/to/result.md` or `FAILED path/to/result.md`.
  - detailed findings, logs, diffs, and reasoning go into the document.
- Run workers in a controlled workspace and inspect their output yourself.
- Do not trust worker self-reports; verify diffs and tests from Hermes.
- Avoid letting opencode / Claude Code / Codex print huge model transcripts into Hermes context.
- Kill or clean up background processes after probes.

Possible scratch result paths:

- `tmp/qoder-opencode-probe-results.md`
- `tmp/qoder-claude-code-cache-probe-results.md`
- `tmp/codex-qoder-review.md`

Do not write secrets or raw tokens into docs. Redact credentials as `[REDACTED]`.

## Real-client probe guidance

Only run live probes after reading project startup/config conventions. If a local server or real credentials are needed, inspect existing scripts/config first and avoid printing secrets.

For opencode:

- Test OpenAI Chat, Anthropic Messages, and OpenAI Responses protocol variants separately.
- Use a tiny task that forces at least one tool call.
- Confirm the returned tool name, tool id, argument shape, and tool result round-trip match the protocol expected by opencode.
- If opencode supports debug logs, write them to a file and summarize only sanitized evidence.

For Claude Code:

- Use the same model alias route (`claude-opus-4-6` -> Qoder `ultimate`) and same account/key scope.
- Run at least two turns with identical stable prefix and an appended follow-up.
- Observe upstream/client-visible usage for `cache_read_input_tokens` or equivalent cached-token field.
- If cache is still zero, inspect what actually changed between turns:
  - system blocks and hidden Claude Code metadata;
  - tool definitions and ordering;
  - cache_control placement / TTL;
  - session/conversation key;
  - message IDs, content block IDs, tool IDs;
  - billing header / `cch` / other volatile fields.

## Likely investigation points

Claude Code cache miss may be caused by more than `cch`:

- System prompt contains other volatile Claude Code metadata.
- Tool definitions include volatile descriptions, order, IDs, or generated schema details.
- Gateway replays too much or too little of the prior Qoder conversation.
- Cache control is missing, attached to the wrong block, or uses TTL/placement different from other working gateways.
- Qoder session key differs because Claude Code does not send stable `prompt_cache_key` in the way expected.
- The Qoder gateway converts Anthropic/Responses into Qoder messages in a way that changes the prefix every turn.

opencode Anthropic/Responses tool issues may be caused by:

- Tool names not remapped in Anthropic/Responses paths.
- Tool call IDs not preserved or protocol-specific IDs missing.
- Streaming event type/order mismatch.
- Tool result messages not converted into Qoder-compatible form and then back into client-compatible form.
- Responses API output items requiring `function_call` / `function_call_output` shapes rather than Chat-style `tool_calls`.
- Anthropic requiring `tool_use` / `tool_result` blocks with exact `id` and `name` semantics.

## Verification commands already useful

From repo root or backend:

```bash
cd /home/ycyc/projects/TokenRouter/backend
go test ./internal/service -run '^TestQoder' -count=1
go test ./internal/pkg/qoder -run 'Qoder|SSE|Usage|Cache|Encode|Decode' -count=1
```

Full gates:

```bash
cd /home/ycyc/projects/TokenRouter
make test
make build
```

At the prior handoff, `make build` passed. `make test` ran all Go tests successfully but failed afterward because `golangci-lint` was not installed on the machine. Re-check current environment before relying on that.

## Acceptance criteria

Do not mark complete until all are true:

- The root cause for Claude Code cache misses is identified with evidence.
- The Qoder gateway has a fix or documented required config for Claude Code cache stability.
- Real Claude Code usage through TokenRouter/Qoder/ultimate shows cache read on repeated stable-prefix turns, or there is a precise upstream/client limitation with sanitized evidence.
- opencode works with OpenAI Chat, Anthropic Messages, and OpenAI Responses protocol paths for tool calls.
- Protocol regressions are covered by tests.
- Relevant docs are updated.
- Hermes has run the final verification commands and inspected real-client evidence.

## 2026-06-16 completion notes

Root causes fixed in this pass:

1. Qoder gateway did not apply the same real Claude Code request-context detection used by other gateway paths before selecting its conversation/cache key. Production Claude Code requests without an explicit session/header key could fall back to per-request keys, so stable prefixes were not reused consistently. The Qoder Messages handler now marks Claude Code context before forwarding, and the service derives an API-key-isolated `claude_code_prefix` key from normalized model/system/tools/first-user prefix only when no explicit body/header/metadata key exists.

2. Qoder upstream may emit tool calls as declared-tool aliases, XML text, or DSML text. The Chat path had partial remapping, but Anthropic Messages and OpenAI Responses needed protocol-specific output conversion and stream handling so opencode receives exact `tool_use` / `tool_result` or Responses `function_call` / `function_call_output` shapes with stable tool IDs and declared tool names.

3. `/v1/responses` was not wired through the Qoder handler/service path. The Qoder handler now has a Responses endpoint path and `ForwardResponses` uses the same Qoder native stream with Responses-specific conversion.

Key changed files:

- `backend/internal/handler/qoder_gateway_handler.go`
  - Adds Qoder Responses routing.
  - Calls `prepareQoderRequestContext` for Messages so real Claude Code detection reaches the service layer.
- `backend/internal/service/qoder_gateway_service.go`
  - Adds `ForwardResponses`.
  - Adds protocol-specific Anthropic/Responses/OpenAI stream response conversion, tool-name mapping, XML/DSML text tool-call parsing, and accepted/rollback conversation reservation.
  - Adds `claude_code_prefix` conversation key fallback guarded by `IsClaudeCodeClient` and still isolated by API key.
- `backend/internal/service/qoder_gateway_service_test.go`
  - Adds/extends regression coverage for OpenAI Chat, Anthropic Messages, OpenAI Responses tool-call mapping/streaming and Claude Code stable-prefix keying.

Automated verification run in this pass:

- `cd backend && go test ./internal/service -run '^(TestQoderGateway(ChatCompletionsMapsUpstreamToolNameToDeclaredOpenAITool|MessagesMapsUpstreamToolNameToDeclaredAnthropicTool|ResponsesMapsUpstreamToolNameToDeclaredFunctionCall|ClaudeCodeContextWithoutSessionUsesStablePrefixKey)|TestQoderGatewayWrites(OpenAIToolCallsStream|AnthropicStreamParsesDSMLTextToolCall|ResponsesStreamWritesFunctionCall))' -count=1`
  - passed.
- `cd backend && go test ./internal/handler ./internal/service -run 'Qoder|ClaudeCode|Responses|Tool' -count=1`
  - passed.
- `cd backend && go test ./...`
  - passed.
- `make build-backend`
  - passed.
- `make build`
  - passed for backend and frontend; frontend emitted only Vite/chunk-size warnings.
- `make test`
  - all Go tests passed, then the command failed because this machine lacks `golangci-lint` (`golangci-lint: 没有那个文件或目录`). This is an environment/tooling gap, not a Go test failure.
- `git diff --check -- backend/internal/service/qoder_gateway_service.go backend/internal/service/qoder_gateway_service_test.go backend/internal/handler/qoder_gateway_handler.go docs/qoder-opencode-claude-code-handoff-20260616.md docs/qoder-native-upstream.md`
  - passed.

Live evidence captured against local Qoder dev stack (`http://127.0.0.1:19080`) with a temporary local probe API key that was deleted afterward:

- `tmp/qoder-direct-protocol-probe-results.md`
  - OpenAI Chat: first and second requests 200, `tool_name=bash`, tool id present, final output contained tool result.
  - Anthropic Messages: first and second requests 200, `tool_name=bash`, tool id present, final output contained tool result, usage included cache fields.
  - OpenAI Responses: first and second requests 200, `tool_name=bash`, `call_id` present, final output contained tool result.
- `tmp/opencode-live-openai-compatible-report.md`
  - real `opencode` 1.17.4 with isolated XDG config, provider `qoder-openai-compatible`, model `claude-opus-4-6`, exit 0, tool probe OK.
- `tmp/opencode-live-anthropic-v1-report.md`
  - real `opencode` 1.17.4 with isolated XDG config, provider `qoder-anthropic`, model `claude-opus-4-6`, exit 0, tool probe OK.
  - Important config detail: for opencode's Anthropic provider, the base URL must be `http://127.0.0.1:19080/v1`; without `/v1`, opencode posts to `/messages` and misses the TokenRouter `/v1/messages` route.
- `tmp/qoder-live-evidence-summary.md`
  - consolidated sanitized evidence.
- `tmp/claude-live-rootb-report.md`
  - real Claude Code 2.1.178 using `ANTHROPIC_BASE_URL=http://127.0.0.1:19080`, model `claude-opus-4-6`, exit 0, tool probe OK.
  - Important config detail: Claude Code should receive the base URL without `/v1`; if configured as `http://127.0.0.1:19080/v1`, Claude Code posts to `/v1/v1/messages` and receives 404.
- `tmp/claude-rootb-server-qoder.log` and `tmp/claude-rootb-output-summary.md`
  - Qoder server logs show repeated `/v1/messages` requests, `reused=true` on later turns, and previous upstream usage present.
  - Claude Code output summary includes `cache_read_input_tokens` / `cacheReadInputTokens` fields and final `CLAUDE_TOOL_OK` result. In the root-base run, cache-read values were present but 0 for that short file-reading task; Qoder reuse was confirmed by server-side `reused=true` entries and very small later upstream input-token counts.
- `tmp/qoder-live-cleanup-report.md`
  - temporary local DB user/key deletion verified: counts `['0', '0']`, secret file removed.

Current caveat:

- Direct Responses protocol and Qoder service tests pass for tool-call round trips. opencode 1.17.4 did not expose an obvious CLI/provider switch to force an OpenAI Responses transport; the real Responses validation was therefore done with a direct `/v1/responses` protocol probe rather than the opencode CLI. Keep the direct protocol probe as the Responses acceptance evidence unless/until opencode exposes a Responses transport selector.
