package qoder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/requestcontext"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// execute.go executes Qoder requests with conversation planning, credential
// acquisition, protocol conversion, and completion evidence.
// Qoder execution converts three client protocols into one signed upstream
// stream while retaining conversation identity. The original request context
// controls preparation and credential waits. Streaming inference uses a
// detached context with a separate execution budget so trailing usage can
// arrive after client disconnection.
//
// Transport acceptance publishes a versioned history snapshot. Stream or
// conversion failures roll it back and return partial evidence when output and
// usage were both observed. Completed non-streaming work and streams reporting
// output commit history after conversion. The output observer records semantic timing before
// writes and marks disconnects, letting completion distinguish upstream
// service from downstream delivery. The request owner releases provider and
// user leases.

// StreamClient sends the prepared inference request and returns its live
// response body.
type StreamClient interface {
	// StreamRequestContext sends an inference request using the supplied context
	// and session. An empty path selects the generation endpoint in Client; callers
	// consume and close successful response bodies through their stream reader.
	StreamRequestContext(context.Context, *SessionContext, string, []byte, map[string]string) (*http.Response, error)
}

// streamClientWithDoer supports provider-specific transport selection for an
// inference request.
type streamClientWithDoer interface {
	// StreamRequestContextWithDoer sends inference through a caller-selected
	// request transport. Callers consume and close successful response bodies; the
	// supplied doer carries the provider proxy and TLS selection for this attempt.
	StreamRequestContextWithDoer(context.Context, *SessionContext, string, []byte, map[string]string, RequestDoer) (*http.Response, error)
}

// Target binds site metadata and provider callbacks needed by one Qoder attempt.
type Target struct {
	// Attributes resolves the mapped route against its captured account catalog.
	// Nil retains execution defaults without inferring site-wide model capabilities.
	Attributes func(context.Context, string, protocol.ProtocolID) (modelrules.Resolution, error) // route attribute resolver
	ProviderID int64                                                                             // selected provider identifier
	Site       Site                                                                              // selected credential site
	Metadata   RequestMetadata                                                                   // inbound session metadata
	// Session obtains credentials with the original request context. The callback
	// is required and cancellation can stop the wait before inference starts.
	Session func(context.Context) (*SessionContext, error) // credential session getter
	// Client constructs the inference transport after credentials are ready. The
	// required callback returns a usable StreamClient on success; its error
	// prevents upstream dispatch.
	Client func() (StreamClient, error) // inference client factory
	// Doer supplies the provider transport when Client implements
	// streamClientWithDoer. Other clients use StreamRequestContext and the
	// transport retained by their client implementation.
	Doer RequestDoer // provider request transport
}

// ExecuteOptions supplies shared conversation storage, timeout, and attempt
// activity registration.
type ExecuteOptions struct {
	// Conversations is shared across attempts to match full-history fingerprints. Nil in NewExecutor allocates a store with the default TTL.
	Conversations *QoderConversationStore // shared conversation store
	// Timeout bounds the execution context created before protocol parsing.
	// Credential waits still use the original request context, so preparation can
	// consume this upstream budget before dispatch.
	Timeout time.Duration // upstream execution duration
	// Enter registers active execution. A successful call supplies a nonnil
	// release function that Execute invokes on exit; a nil Enter callback skips
	// activity registration.
	Enter func() (func(), error) // attempt activity registrar
}

// Executor converts client requests and coordinates upstream output with
// conversation state.
type Executor struct {
	// conversations retains the store selected by NewExecutor. Concurrent requests
	// coordinate fingerprint and usage observations through that store's mutex and snapshot rules.
	conversations *QoderConversationStore // shared conversation store
	timeout       time.Duration           // upstream execution duration
	// enter retains the activity callback selected at construction. Execute invokes
	// it before preparation and releases the returned activity registration when
	// the attempt exits.
	enter func() (func(), error) // attempt activity registrar
}

// TargetID returns the provider identifier carried by this attempt target.
// A nil target returns zero so diagnostic formatting can identify absent
// configuration before execution validates it.
func (t *Target) TargetID() int64 {
	if t == nil {
		return 0
	}
	return t.ProviderID
}

// String formats the target's provider identifier for diagnostic output and
// logging. A nil receiver uses TargetID's zero result, allowing failed
// configuration paths to format the target safely.
func (t *Target) String() string { return fmt.Sprintf("qoder target provider=%d", t.TargetID()) }

// NewExecutor creates an executor with conversation storage and an execution
// timeout. Nil storage allocates the default store, nonpositive timeout selects
// QoderStreamTimeout, and the optional Enter callback is retained for attempt
// activity registration.
func NewExecutor(options ExecuteOptions) *Executor {
	if options.Conversations == nil {
		options.Conversations = NewQoderConversationStore(QoderConversationTTL)
	}
	if options.Timeout <= 0 {
		options.Timeout = QoderStreamTimeout
	}
	return &Executor{conversations: options.Conversations, timeout: options.Timeout, enter: options.Enter}
}

// PrepareConversation builds a payload and a conversation plan for one selected
// target. Target site replaces request metadata before session-key
// selection. Clients send complete input history with each request. The caller publishes acceptance and completion
// after transport and output outcomes are known.
func (e *Executor) PrepareConversation(target *Target, wireProtocol protocol.ProtocolID, request QoderPayloadRequest) (map[string]any, string, *QoderConversationPlan, error) {
	request.Site = target.Site
	request, err := request.withEffectiveControls()
	if err != nil {
		return nil, "", nil, err
	}
	key, source := QoderConversationKey(target.Metadata, target.ProviderID, request)
	plan := e.conversations.Plan(key, request.System, request.Tools, request.Messages)
	payload, model := buildQoderPayload(request, plan.SessionID, plan.MessagesToSend, plan.IncludeSystem, plan.IncludeTools)
	plan.Log(target.Metadata, target.ProviderID, string(wireProtocol), request.Model, source, request, payload)
	return payload, model, plan, nil
}

// Execute serves one Qoder attempt and returns observed service and usage
// evidence. The caller supplies a configured Target and usable synchronous
// sink. Preparation and credential waits observe the original context;
// streaming inference detaches parent cancellation while retaining request
// values and the internal abort signal. The execution timeout starts before
// parsing and bounds the upstream context.
//
// A returned response publishes conversation acceptance. Stream failures roll
// it back and return partial evidence when the converter observed both output
// and usage. Completed non-streaming work stays served when the sink fails.
// History commits for non-streaming completion or when a stream converter
// reports output; Responses IDs identify the current result. The execution owner closes the response through
// stream reading, while the request owner releases provider and user leases.
// @project-doc docs/interfaces/qoder_upstream.md#qoder_execution_boundary
func (e *Executor) Execute(ctx context.Context, input upstream.AttemptInput, sink upstream.OutputSink) (result upstream.AttemptResult, executionErr error) {
	defer func() { result.Finish(ctx, executionErr) }()
	if e.enter != nil {
		done, err := e.enter()
		if err != nil {
			return upstream.AttemptResult{}, err
		}
		defer done()
	}
	start := time.Now()
	observed := &executionOutput{sink: sink}
	sink = observed
	defer func() {
		result.ClientDisconnect = result.ClientDisconnect || observed.disconnected

		if ctx != nil && ctx.Err() != nil {
			result.Cancelled = true
			result.ClientDisconnect = result.ClientDisconnect || result.Stream
		}
		if executionErr != nil {
			switch {
			case IsRequestError(executionErr):
				result.FailureClass = "invalid_request"
			case errors.Is(executionErr, context.Canceled):
				result.FailureClass = "cancelled"
			case errors.Is(executionErr, context.DeadlineExceeded):
				result.FailureClass = "timeout"
			default:
				result.FailureClass = "upstream"
			}
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return upstream.AttemptResult{}, err
	}
	target, ok := input.Target.(*Target)
	if !ok || target == nil || target.Session == nil || target.Client == nil {
		return upstream.AttemptResult{}, errors.New("qoder gateway service is not configured")
	}
	executionCtx := ctx
	// A disconnected client can precede the final usage event. Detach its stop
	// signal while retaining the server abort signal, then bound collection with
	// this timeout.
	if input.Stream {
		executionCtx = requestcontext.Detach(ctx)
	}
	executionCtx, cancel := context.WithTimeout(executionCtx, e.timeout)
	defer cancel()
	var request QoderPayloadRequest
	var err error
	switch input.Protocol {
	case protocol.ProtocolOpenAIChatCompletions:
		request, err = ParseQoderChatCompletionsPayload(input.Body)
	case protocol.ProtocolOpenAIResponses:
		request, err = ParseQoderResponsesPayload(input.Body)
	case protocol.ProtocolAnthropicMessages:
		request, err = ParseQoderAnthropicMessagesPayload(input.Body)
	default:
		return upstream.AttemptResult{}, errors.New("unsupported qoder client protocol")
	}
	if err != nil {
		// Parser failures precede session acquisition and network dispatch. Preserve
		// specific validation reasons and classify malformed client input the same way.
		if !IsRequestError(err) {
			err = &RequestError{Code: "invalid_request", Message: err.Error()}
		}
		return upstream.AttemptResult{}, err
	}
	responseModel := input.ResponseModel
	if responseModel == "" {
		responseModel = request.Model
	}
	if target.Attributes != nil {
		attributes, resolveErr := target.Attributes(ctx, request.Model, input.Protocol)
		if resolveErr != nil {
			return upstream.AttemptResult{}, resolveErr
		}
		request.Capabilities = &attributes
		if attributes.Target.Model != "" {
			request.Model = attributes.Target.Model
		}
	}
	payload, model, plan, err := e.PrepareConversation(target, input.Protocol, request)
	if err != nil {
		return upstream.AttemptResult{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return upstream.AttemptResult{}, fmt.Errorf("marshal qoder payload: %w", err)
	}
	// The original context controls credential waits and pre-dispatch checks.
	// Recheck it before starting inference so detached stream collection cannot
	// launch work for a request already cancelled during preparation.
	if err := ctx.Err(); err != nil {
		return upstream.AttemptResult{}, err
	}
	session, err := target.Session(ctx)
	if err != nil {
		return upstream.AttemptResult{}, fmt.Errorf("get qoder session: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return upstream.AttemptResult{}, err
	}
	client, err := target.Client()
	if err != nil {
		return upstream.AttemptResult{}, err
	}
	headers := map[string]string{"x-model-key": model, "x-model-source": "system"}
	if err := ctx.Err(); err != nil {
		return upstream.AttemptResult{}, err
	}
	var response *http.Response
	if doerClient, ok := client.(streamClientWithDoer); ok && target.Doer != nil {
		response, err = doerClient.StreamRequestContextWithDoer(executionCtx, session, "", body, headers, target.Doer)
	} else {
		response, err = client.StreamRequestContext(executionCtx, session, "", body, headers)
	}
	if err != nil {
		return upstream.AttemptResult{}, err
	}
	// Transport acceptance can later fail during SSE decoding or protocol output.
	// Publish a versioned accepted state now so those paths can roll back this
	// attempt.
	plan.CommitAccepted()
	result = upstream.AttemptResult{Model: responseModel, UpstreamModel: model, Stream: input.Stream, UpstreamHeaders: response.Header.Clone()}
	if input.Protocol == protocol.ProtocolOpenAIResponses {
		result.RequestID = request.ResponseID
	}
	output := upstream.NewOutputContext(sink)
	mapper := QoderDeclaredToolNameMapper(QoderAnySlice(payload["tools"]))
	var nativeUsage upstream.TokenUsage
	commitComplete := true
	if input.Stream {
		var stream *QoderStreamResult
		switch input.Protocol {
		case protocol.ProtocolOpenAIChatCompletions:
			stream, err = WriteQoderOpenAIStreamResponse(executionCtx, output, responseModel, response, QoderOpenAIStreamUsageMapper(plan.RecordUsage), QoderOpenAIStreamToolNameMapper(mapper), QoderOpenAIStreamIncludeUsage(GjsonBool(input.Body, "stream_options.include_usage")))
		case protocol.ProtocolOpenAIResponses:
			stream, err = WriteQoderResponsesStreamResponse(executionCtx, output, responseModel, response, QoderResponsesStreamUsageMapper(plan.RecordUsage), QoderResponsesStreamToolNameMapper(mapper), QoderResponsesStreamResponseID(request.ResponseID))
		case protocol.ProtocolAnthropicMessages:
			stream, err = WriteQoderAnthropicStreamResponse(executionCtx, output, responseModel, response, QoderAnthropicStreamUsageMapper(plan.RecordUsage), QoderAnthropicStreamToolNameMapper(mapper))
		}
		if stream != nil {
			nativeUsage = stream.Usage
			result.Usage = plan.RecordUsage(nativeUsage)
			result.HasUsage = stream.HasUsage
			result.Served = stream.HasOutput
			commitComplete = stream.HasOutput
		}
		if err != nil {
			// Failed stream conversion makes this attempt unsuitable for continuation.
			// Revert its accepted history while returning the already observed evidence
			// to the completion owner.
			plan.RollbackAccepted()
			result.Duration = time.Since(start)
			return result, err
		}
	} else {
		events, readErr := ReadQoderSSEEventsContext(executionCtx, response)
		if readErr != nil {
			// SSE reading failed before a complete protocol response was built. Revert
			// this accepted attempt before returning the read failure to the request owner.
			plan.RollbackAccepted()
			return upstream.AttemptResult{}, readErr
		}
		nativeUsage = QoderUsageFromEvents(events)
		result.Usage = plan.RecordUsage(nativeUsage)
		for _, event := range events {
			result.HasUsage = result.HasUsage || event.HasUsage
		}
		var responseBody []byte
		switch input.Protocol {
		case protocol.ProtocolOpenAIChatCompletions:
			responseBody, err = BuildQoderOpenAICompletion(responseModel, QoderEventsWithUsage(events, nativeUsage), mapper)
		case protocol.ProtocolOpenAIResponses:
			responseBody, err = BuildQoderResponsesResponseWithID(responseModel, request.ResponseID, QoderEventsWithUsage(events, result.Usage), mapper)
		case protocol.ProtocolAnthropicMessages:
			responseBody, err = BuildQoderAnthropicMessage(responseModel, QoderEventsWithUsage(events, nativeUsage), mapper)
		}
		if err != nil {
			// Client response construction failed after upstream collection. Roll back
			// this acceptance so a subsequent continuation cannot treat the failed
			// conversion as completed history.
			plan.RollbackAccepted()
			return upstream.AttemptResult{}, err
		}
		result.Served = true
		// Upstream generation and usage collection have completed before client
		// output. Keep Served and Usage when the sink fails so completion can account
		// for that work.
		if writeErr := sink.Begin(upstream.OutputHead{Status: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}}); writeErr != nil {
			result.ClientDisconnect = true
		} else if writeErr = sink.Emit(upstream.OutputEvent{Data: responseBody, Semantic: true, CommitForRetry: true, Terminal: true}); writeErr != nil {
			result.ClientDisconnect = true
		}
	}
	plan.LogUsage(target.Metadata, target.ProviderID, nativeUsage, result.Usage)
	// Commit completed history after generation and output handling. The store
	// records native usage with that history for later session diagnostics.
	if commitComplete {
		plan.Commit(nativeUsage)
	}
	result.Duration = time.Since(start)
	return result, nil
}
