package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_test_execution.go executes COSY connection probes using the selected
// Qoder site and model route. PAT records can invalidate cached sessions
// before the probe, and Global tests query user info before streaming; CN
// tests use their site session directly. The public test_start model remains
// the requested spelling while the payload and x-model-key use the resolved
// upstream route.
//
// An injected client can supply fixture behavior, while platform Client values
// are rebuilt for the selected site. Shared transport is used when the client
// supports the doer-aware interface, and user-info queries can fill missing
// UID or name on the obtained session identity. The stream adapter closes the
// successful response body after emitting typed test events.

// qoderProviderTestSessionProvider obtains the provider session used by
// connection probes.
type qoderProviderTestSessionProvider interface {
	// GetSession returns a session for the selected provider or an acquisition
	// error.
	// Implementations may cache and share the returned pointer; the probe can fill
	// missing identity hints during its Global user-info check.
	GetSession(ctx context.Context, provider *providercore.Record) (*qoder.SessionContext, error)
}

// qoderProviderTestSessionInvalidator evicts a provider session before a fresh
// PAT probe.
type qoderProviderTestSessionInvalidator interface {
	// Invalidate evicts the provider session before a PAT probe rebuild.
	// Implementations arrange cache invalidation for that provider ID; the method
	// returns no result, so acquisition failures are reported by the later
	// GetSession.
	Invalidate(providerID int64)
}

// qoderProviderTestOAuthClient reads user identity for a prepared Global probe
// token.
type qoderProviderTestOAuthClient interface {
	// GetUserInfo queries identity with the supplied token and context.
	// Implementations return an identity or query error; the probe fills only
	// missing
	// UID and name hints in its obtained session when data is available.
	GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error)
}

// QoderProviderTest runs upstream probes using the injected shared session
// provider.
type QoderProviderTest struct {
	// Attributes resolves the final model for routed group probes. A nil callback
	// skips profile lookup, leaving declared output and sampling permissions unknown.
	Attributes ModelAttributeLookup
	// Sessions supplies cached or built provider identity. Nil constructs a local
	// default token provider for this call; PAT probes invalidate it when
	// supported.
	Sessions qoderProviderTestSessionProvider
	// Client can supply an injected stream implementation. A platform Client or
	// nil selection is rebuilt from the provider site by qoderTestClient.
	Client qoder.StreamClient
	// UserInfo optionally supplies the Global identity query. Nil selects the
	// site client and provider doer, while CN probes skip this query.
	UserInfo qoderProviderTestOAuthClient
	// Transport and Profiles prepare a doer for user-info and stream queries.
	// A stream client lacking the doer-aware method uses its own
	// StreamRequestContext.
	Transport QoderTransport             // shared probe request transport
	Profiles  *egressadapter.TLSProfiles // optional TLS profile resolver
}

// qoderTestClientWithDoer accepts provider transport for a streaming Qoder
// probe request.
type qoderTestClientWithDoer interface {
	// StreamRequestContextWithDoer sends the prepared payload using the
	// supplied doer.
	// The caller passes session and model headers, propagates request errors, and
	// transfers successful response-body closing to the test stream output adapter.
	StreamRequestContextWithDoer(context.Context, *qoder.SessionContext, string, []byte, map[string]string, qoder.RequestDoer) (*http.Response, error)
}

// Execute prepares a Qoder COSY connection probe and emits its stream events.
// It rejects non-COSY records, defaults blank model and prompt, and obtains a
// site session. PAT input invalidates a supporting session source before the
// query; session failures become test errors before streaming starts.
//
// Saved model mapping selects the final allowed account route before payload
// conversion, while the public start event retains the requested test name.
// Global probes query user info before streaming. A doer-aware client receives
// provider transport; another injected client uses its own stream method.
// Successful response closing belongs to TestStreamOutput.Qoder. Callers
// serialize access to shared session identities while Global probes may fill
// missing user hints.
func (s *QoderProviderTest) Execute(c *TestRun, value *providercore.Record, modelID string, prompt string) error {
	if value.Type != capability.ProviderTypeCosy {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Unsupported provider type: %s", value.Type))
	}

	ctx := c.Context
	testModelID := strings.TrimSpace(modelID)
	snapshot, err := value.QoderCatalog(time.Now())
	if err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	if testModelID == "" {
		for _, model := range snapshot.Models {
			if model.Enabled && (testModelID == "" || model.Default) && value.IsModelSupported(model.Key, ModelDefaults(), ModelRules(value)) {
				testModelID = model.Key
				if model.Default {
					break
				}
			}
		}
		if testModelID == "" {
			return (TestStreamOutput{}).Error(c, "The Qoder account has no enabled models")
		}
	}
	if !value.IsModelSupported(testModelID, ModelDefaults(), ModelRules(value)) {
		return (TestStreamOutput{}).Error(c, "The selected model is unavailable for this Qoder provider")
	}
	model := value.NormalizeQoderModel(mappedTestModel(value, testModelID))
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "Write a Python function add(a, b) that returns a + b."
	}

	if c.ProtocolRoute.ClientProtocol != "" {
		if _, err := resolveProbeType(ctx, s.Attributes, value, c.GroupID, model, "qoder_chat", providercore.ProviderTestTypeText); err != nil {
			return (TestStreamOutput{}).Error(c, err.Error())
		}
	}

	sessionProvider := s.Sessions
	if sessionProvider == nil {
		sessionProvider = NewQoderTokenProvider(qoder.SessionBuilder{})
	}
	// A PAT probe is also an authorization check. Evict a supporting cache before
	// acquisition so the test observes current PAT validity rather than a
	// cached token.
	if strings.TrimSpace(value.GetCredential("pat")) != "" {
		if invalidator, ok := sessionProvider.(qoderProviderTestSessionInvalidator); ok {
			invalidator.Invalidate(value.ID)
		}
	}
	session, err := sessionProvider.GetSession(ctx, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Qoder session failed: %s", err.Error()))
	}
	site, err := qoderSiteForRecord(value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}

	// The short coding task can spend its output budget on reasoning before text.
	requestBody, err := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": testPrompt}},
		"max_tokens": 256,
		"stream":     true,
	})
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to encode Qoder test payload")
	}
	request, err := qoder.ParseQoderChatCompletionsPayload(requestBody)
	if err != nil {
		return (TestStreamOutput{}).Error(c, fmt.Sprintf("Failed to build Qoder test payload: %s", err.Error()))
	}
	request.Site = site
	var resolution modelrules.Resolution
	if s.Attributes != nil {
		resolution, err = s.Attributes(ctx, value, c.GroupID, request.Model, string(capability.ProtocolQoderChat))
		if err != nil {
			return (TestStreamOutput{}).Error(c, err.Error())
		}
	} else {
		client := c.ProtocolRoute.ClientProtocol
		if client == "" {
			client = capability.ProtocolOpenAIChatCompletions
		}
		target := value.ModelAttributeRoute(request.Model, string(client), string(capability.ProtocolQoderChat))
		resolution = modelrules.View{}.Resolve(target, modelcatalog.Attributes{})
	}
	request.Capabilities = &resolution
	payload, modelKey, err := qoder.BuildQoderPayloadWithOptions(request, "", request.Messages, true, true)
	if err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	payloadBody, err := json.Marshal(payload)
	if err != nil {
		return (TestStreamOutput{}).Error(c, "Failed to encode Qoder test payload")
	}

	c.Begin(true)

	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "test_start", Model: testModelID})
	// Global tests check the user-info endpoint before inference; CN follows its
	// prepared session path and does not issue this Global compatibility query.
	if site == qoder.SiteGlobal {
		if err := s.probeQoderUserInfo(ctx, value, session); err != nil {
			return (TestStreamOutput{}).Error(c, err.Error())
		}
	}
	(TestStreamOutput{}).SendEvent(c, providercore.TestEvent{Type: "status", Text: "Testing connection through Qoder COSY"})

	client, err := qoderTestClient(s.Client, value)
	if err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}
	headers := map[string]string{
		"x-model-key":    modelKey,
		"x-model-source": "system",
	}
	if doer := QoderRequestDoer(value, s.Transport, s.Profiles); doer != nil {
		if doerClient, ok := client.(qoderTestClientWithDoer); ok {
			resp, err := doerClient.StreamRequestContextWithDoer(ctx, session, "", payloadBody, headers, doer)
			if err != nil {
				return (TestStreamOutput{}).Error(c, err.Error())
			}
			return (TestStreamOutput{}).Qoder(c, resp.Body)
		}
	}
	// A custom client can lack the doer-aware extension. Use its existing stream
	// method in that case, retaining the injected implementation as the
	// transport owner.
	resp, err := client.StreamRequestContext(ctx, session, "", payloadBody, headers)
	if err != nil {
		return (TestStreamOutput{}).Error(c, err.Error())
	}

	return (TestStreamOutput{}).Qoder(c, resp.Body)
}

// probeQoderUserInfo validates the Global probe token and fills missing
// identity hints.
// Nil session identity or missing token returns an error. It prefers the session
// token, then the stored token, and mutates only absent UID and name when
// user-info succeeds. Returned session identity may be shared by its session
// source.
func (s *QoderProviderTest) probeQoderUserInfo(ctx context.Context, value *providercore.Record, session *qoder.SessionContext) error {
	if session == nil || session.Identity == nil {
		return errors.New("qoder session identity is empty")
	}
	token := strings.TrimSpace(session.Identity.SecurityOauthToken)
	if token == "" {
		token = strings.TrimSpace(value.GetCredential("security_oauth_token"))
	}
	if token == "" {
		return errors.New("qoder security_oauth_token is empty")
	}
	client := s.UserInfo
	var userInfo *qoder.UserInfo
	var err error
	if client != nil {
		userInfo, err = client.GetUserInfo(ctx, token)
	} else {
		userInfo, err = s.getQoderUserInfoForProvider(ctx, value, token)
	}
	if err != nil {
		return fmt.Errorf("qoder userinfo probe failed: %w", err)
	}
	if userInfo != nil {
		if session.Identity.UID == "" && strings.TrimSpace(userInfo.ID) != "" {
			session.Identity.UID = strings.TrimSpace(userInfo.ID)
		}
		if session.Identity.Name == "" && strings.TrimSpace(userInfo.Name) != "" {
			session.Identity.Name = strings.TrimSpace(userInfo.Name)
		}
	}
	return nil
}

// getQoderUserInfoForProvider queries the selected site's user-info endpoint.
// A provider doer receives the request when available; otherwise a site OAuth
// client uses its own transport. The explicit doer path closes its response,
// caps error-body reading at 4096 bytes, and redacts its diagnostic text.
func (s *QoderProviderTest) getQoderUserInfoForProvider(ctx context.Context, value *providercore.Record, token string) (*qoder.UserInfo, error) {
	profile, err := qoderTestProfile(value)
	if err != nil {
		return nil, err
	}
	if doer := QoderRequestDoer(value, s.Transport, s.Profiles); doer != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, profile.OpenAPIBaseURL+qoder.UserInfoPath, nil)
		if err != nil {
			return nil, fmt.Errorf("qoder: create userinfo request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		req.Header.Set("User-Agent", profile.OpenAPIUserAgent())

		resp, err := doer(req)
		if err != nil {
			return nil, fmt.Errorf("qoder: userinfo request: %w", err)
		}
		// This adapter consumes the direct doer response for identity parsing.
		// Close it on every status, decode, or successful return so that
		// transport body ownership ends with this query.
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			// Keep the status diagnostic even if its bounded error read fails.
			// Redact the partial body before returning text to the test output
			// consumer.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return nil, fmt.Errorf("qoder: userinfo failed with status %d: %s", resp.StatusCode, qoder.RedactSensitiveText(string(body)))
		}

		var info qoder.UserInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return nil, fmt.Errorf("qoder: parse userinfo response: %w", err)
		}
		return &info, nil
	}
	return qoder.NewOAuthClientForProfile(profile, nil).GetUserInfo(ctx, token)
}

// qoderTestProfile returns the supported profile for the provider's stored site.
// A nil provider or unsupported site returns an error. Empty site input resolves
// to Global through the platform's legacy site default.
func qoderTestProfile(value *providercore.Record) (qoder.Profile, error) {
	site, err := qoderSiteForRecord(value)
	if err != nil {
		return qoder.Profile{}, err
	}
	return qoder.ProfileForSite(site)
}

// qoderTestClient selects a stream client for the provider's site.
// An injected implementation other than platform Client is retained directly.
// Nil or platform Client input resolves the site profile and constructs a
// matching client, returning any unsupported-site error.
func qoderTestClient(configured qoder.StreamClient, value *providercore.Record) (qoder.StreamClient, error) {
	if configured != nil {
		if _, production := configured.(*qoder.Client); !production {
			return configured, nil
		}
	}
	profile, err := qoderTestProfile(value)
	if err != nil {
		return nil, err
	}
	return qoder.NewClientForProfile(profile), nil
}
