package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// creative_catalog.go applies execution model rules to creative image
// catalogue entries.
// Creative image catalogues apply the same provider mapping and final-model
// support rules as execution. Preparing the policy once gives the catalogue
// reusable model rules, while attribute lookup still describes the supplied
// provider and selected upstream model.

// creativeCatalogProvider adds execution model policy to the creative provider
// catalogue adapter.
type creativeCatalogProvider struct {
	creative.CatalogProvider // underlying creative catalogue adapter
	// policy retains the supplied record and prepared rules for this catalogue
	// query; callers keep the source record stable while methods use it.
	policy ModelPolicy
}

// AttributeTarget describes the supplied model for the provider's attribute
// lookup service.
func (a creativeCatalogProvider) AttributeTarget(model string) modelrules.Target {
	return a.policy.Record.ModelAttributeTarget(model, "")
}

// CreativeCatalogProvider prepares execution model rules and binds them to the
// creative catalogue adapter. Nil input returns nil; the prepared policy
// retains the supplied record, so callers keep configuration stable while
// querying.
func CreativeCatalogProvider(value *provider.Record) creative.CatalogProvider {
	if value == nil {
		return nil
	}
	policy := (ModelPolicy{Record: value}).Prepared()
	return creativeCatalogProvider{CatalogProvider: creativeprovider.CatalogProviderWithRules(value, policy.models), policy: policy}
}

// ResolveMappedModel resolves the upstream name through execution policy and
// reports whether it differs from model. Resolution uses Background for
// catalogue lookup and can return an empty name when platform rules reject the
// route.
func (a creativeCatalogProvider) ResolveMappedModel(model string) (string, bool) {
	mapped := a.policy.UpstreamModel(context.Background(), model)
	return mapped, mapped != model
}

// IsModelSupported applies execution model support rules to the creative
// catalogue candidate. It evaluates with Background and the policy prepared at
// adapter construction, so callers keep the bound provider configuration
// stable.
func (a creativeCatalogProvider) IsModelSupported(model string) bool {
	return a.policy.Supports(context.Background(), model)
}
