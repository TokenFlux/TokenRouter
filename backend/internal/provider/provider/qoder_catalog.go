package provider

import (
	"context"
	"errors"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_catalog.go binds native Qoder model discovery to provider credentials
// and egress. The core synchronization owner controls freshness, concurrency,
// and conditional publication; this adapter uses the same session cache, proxy,
// and TLS configuration as inference for the selected account.

// QoderCatalogFetcher returns authenticated model discovery for saved accounts.
// Missing session or transport dependencies are reported when invoked, and
// each call resolves its site before constructing the native query client.
func QoderCatalogFetcher(tokens *QoderTokenProvider, transport QoderTransport, profiles *egressprovider.TLSProfiles) func(context.Context, *provider.Record) ([]qodermodel.Model, error) {
	return func(ctx context.Context, record *provider.Record) ([]qodermodel.Model, error) {
		if tokens == nil || transport == nil {
			return nil, errors.New("Qoder model transport is not configured")
		}
		site, err := qoder.ParseSite(record.GetCredential("site"))
		if err != nil {
			return nil, err
		}
		profile, err := qoder.ProfileForSite(site)
		if err != nil {
			return nil, err
		}
		session, err := tokens.GetSession(ctx, record)
		if err != nil {
			return nil, err
		}
		return qoder.NewClientForProfile(profile).ListModels(ctx, session, QoderRequestDoer(record, transport, profiles))
	}
}
