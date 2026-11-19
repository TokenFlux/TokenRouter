package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// settings_oidc_discovery.go retrieves OIDC discovery metadata for effective
// provider configuration.
// Missing OIDC endpoint settings can be filled from the provider discovery
// document. This reader obtains the document through the shared HTTP client
// pool with a bounded request budget and decodes endpoint metadata; OAuthSettings
// decides which missing values to fill and validates the effective URLs before
// authorization starts.

// oidcDiscoveryMaxResponseBytes bounds OIDC metadata document reads to 2 MiB.
const oidcDiscoveryMaxResponseBytes = 2 << 20

// NewSettingsOIDCMetadataResolver returns a metadata resolver bound to the
// deployment's resolved-IP and private-host transport policy.
func NewSettingsOIDCMetadataResolver(validateResolvedIP, allowPrivateHosts bool) func(context.Context, string) (*identity.OAuthProviderMetadata, error) {
	return func(ctx context.Context, discoveryURL string) (*identity.OAuthProviderMetadata, error) {
		return resolveSettingsOIDCMetadataWithPolicy(ctx, discoveryURL, validateResolvedIP, allowPrivateHosts)
	}
}

// ResolveSettingsOIDCMetadata fetches and decodes endpoints from a trimmed,
// nonempty discovery URL using a fifteen-second request budget. Non-success HTTP
// responses and malformed JSON fail; the identity settings reader validates the
// returned endpoint values.
func ResolveSettingsOIDCMetadata(ctx context.Context, discoveryURL string) (*identity.OAuthProviderMetadata, error) {
	return resolveSettingsOIDCMetadataWithPolicy(ctx, discoveryURL, false, true)
}

// resolveSettingsOIDCMetadataWithPolicy executes the discovery GET through the
// shared HTTP client pool configured with the supplied DNS validation flags.
func resolveSettingsOIDCMetadataWithPolicy(ctx context.Context, discoveryURL string, validateResolvedIP, allowPrivateHosts bool) (*identity.OAuthProviderMetadata, error) {
	discoveryURL = strings.TrimSpace(discoveryURL)
	if discoveryURL == "" {
		return nil, fmt.Errorf("discovery url is empty")
	}

	client, err := httpclient.GetClient(httpclient.Options{
		Timeout:            15 * time.Second,
		ValidateResolvedIP: validateResolvedIP,
		AllowPrivateHosts:  allowPrivateHosts,
	})
	if err != nil {
		return nil, fmt.Errorf("create discovery http client: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build discovery request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("discovery request failed: status=%d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, oidcDiscoveryMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read discovery document: %w", err)
	}

	metadata := &identity.OAuthProviderMetadata{}
	if err := json.Unmarshal(body, metadata); err != nil {
		return nil, fmt.Errorf("parse discovery document: %w", err)
	}
	return metadata, nil
}
