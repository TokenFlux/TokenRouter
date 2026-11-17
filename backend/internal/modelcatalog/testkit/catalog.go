package testkit

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// catalog.go provides an embedded catalog view and editable model fixtures for
// tests.
// Tests can share the embedded catalog when they need real metadata, or use
// an editable fixture for controlled model identities. The shared view is
// built once with attribute supplements, while fixture entries and versions
// remain under each test's control.

// OfflineView returns a fixed test view built once from embedded catalog and
// official attribute supplements, panicking on invalid inputs.
var OfflineView = sync.OnceValue(func() modelrules.View {
	body, err := modelcatalog.Offline()
	if err != nil {
		panic(err)
	}
	catalog, err := modelcatalog.Parse(body)
	if err != nil {
		panic(err)
	}
	supplements := make(map[string]json.RawMessage)
	if err := json.Unmarshal(modelcatalog.Supplements(), &supplements); err != nil {
		panic(err)
	}
	// Apply exact supplement keys before equivalent first party aliases, giving
	// each explicitly named entry priority before missing fields on its aliases
	// are filled.
	for model, value := range supplements {
		if err := catalog.ApplyAttributeSupplement(model, value); err != nil {
			panic(err)
		}
	}
	for model, value := range supplements {
		for _, alias := range catalog.FirstPartyAliases(model) {
			if err := catalog.ApplyAttributeSupplement(alias, value); err != nil {
				panic(err)
			}
		}
	}
	return modelrules.NewView(catalog.Publication())
})

// Catalog supplies test entries and a replaceable version for catalog consumers.
type Catalog struct {
	Entries map[string]modelcatalog.Entry // editable test model metadata
	Version string                        // test controlled publication version
}

// New constructs an editable fixture containing the supplied exact IDs.
// Each ID also supplies its initial display name, and the initial version is
// test; callers can replace entries or the version afterward.
func New(ids ...string) *Catalog {
	c := &Catalog{Entries: map[string]modelcatalog.Entry{}, Version: "test"}
	for _, id := range ids {
		name := id
		c.Entries[id] = modelcatalog.Entry{Model: id, Attributes: modelcatalog.Attributes{DisplayName: &name}}
	}
	return c
}

// ModelIDs returns a fresh list of fixture identities sorted by ID.
// Callers can mutate that slice independently, and its strings reflect the
// fixture entries present when this method runs.
func (c *Catalog) ModelIDs() []string {
	ids := []string{}
	for id := range c.Entries {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// ModelEntry returns the exact fixture entry or a zero entry when missing.
// Mutable values are shared with Entries, so tests that change returned data
// also change the stored fixture.
func (c *Catalog) ModelEntry(id string) modelcatalog.Entry { return c.Entries[id] }

// ModelVersion returns the catalog publication version controlled by the test.
func (c *Catalog) ModelVersion() string { return c.Version }
