package modelcatalog

import (
	"maps"
	"slices"
	"sort"
	"strings"
)

// index.go builds a searchable model attribute index and returns copied result
// pages.
// Catalog searches count every matching identity before returning a page,
// including qualified names and their bare aliases. Attribute text and provider
// lists are prepared once for the publication, while each query copies the
// selected entries for callers that edit display or price values.

// Index fixes supplemented catalog rows for repeated attribute searches.
type Index struct {
	data *attributeIndex // immutable prepared attribute search data
}

// attributeIndex stores sorted rows and parallel search text for one publication.
type attributeIndex struct {
	// entries, names, and search share model ordering so searches can test text
	// before copying selected rows. providers is the sorted distinct list across
	// the entire publication, including providers outside one query's matches.
	entries                  []Entry  // catalog rows sorted by identity
	names, providers, search []string // ordered model lookup names, sorted known provider names, normalized row search text
}

// AttributeQuery uses a zero-based Offset. A zero Limit returns all remaining matches.
type AttributeQuery struct {
	// Search matches trimmed, case-insensitive model and display-name text.
	// Provider uses an exact service identity, and Capability requires a known
	// true flag from Supports. Empty strings leave their corresponding filters
	// unrestricted; supplied filters are combined for each catalog row.
	// Search: model/display-name substring; Provider: exact service identity;
	// Capability: required true capability name.
	Search, Provider, Capability string
	// Offset and Limit select a page after filtering. Negative offsets act as
	// zero, and nonpositive limits include every match from the selected offset.
	Offset, Limit int // zero based offset and maximum item count
}

// AttributePage contains copied query rows, publication providers, and the match count.
type AttributePage struct {
	Items     []Entry  // selected matching model entries
	Providers []string // complete publication provider list
	Total     int      // matching rows before pagination
}

// NewIndex prepares rows and search text for repeated attribute queries.
// A nil catalog produces an empty index; the supplied catalog's price data
// remains read-only after construction because prepared rows can share it.
func NewIndex(catalog *Catalog) Index {
	data := &attributeIndex{names: []string{}, providers: []string{}}
	if catalog == nil {
		return Index{data: data}
	}
	data.entries = catalog.Rows()
	providers := map[string]bool{}
	for _, entry := range data.entries {
		data.names = append(data.names, entry.Model)
		text := entry.Model
		if entry.Attributes.DisplayName != nil {
			text += " " + *entry.Attributes.DisplayName
		}
		data.search = append(data.search, strings.ToLower(text))
		providers[entry.Provider] = true
	}
	for provider := range providers {
		data.providers = append(data.providers, provider)
	}
	sort.Strings(data.providers)
	return Index{data: data}
}

// ModelIDs returns a copied, sorted list of prepared lookup identities.
// The zero-value index returns an empty slice, and callers can modify the
// returned list independently of later searches.
func (index Index) ModelIDs() []string {
	if index.data == nil {
		return []string{}
	}
	return slices.Clone(index.data.names)
}

// Search filters the fixed index and returns an independently mutable page.
// Total counts every match before pagination, and Providers lists every
// provider in the publication even when the query selects one service.
// Nonpositive limits return all matches from the clamped offset.
func (index Index) Search(query AttributeQuery) AttributePage {
	result := AttributePage{Items: []Entry{}, Providers: []string{}}
	if index.data == nil {
		return result
	}
	result.Providers = slices.Clone(index.data.providers)
	search := strings.ToLower(strings.TrimSpace(query.Search))
	for i, entry := range index.data.entries {
		if query.Provider != "" && entry.Provider != query.Provider || search != "" && !strings.Contains(index.data.search[i], search) || query.Capability != "" && !entry.Attributes.Supports(query.Capability) {
			continue
		}
		// Count every filtered match before applying page limits so Total still
		// describes the complete query when some matching rows are omitted from Items.
		matched := result.Total
		result.Total++
		if matched < max(0, query.Offset) || query.Limit > 0 && len(result.Items) >= query.Limit {
			continue
		}
		entry.Cost = cloneCost(entry.Cost)
		entry.VideoPrices = maps.Clone(entry.VideoPrices)
		entry.ImagePrices = maps.Clone(entry.ImagePrices)
		entry.ServicePrices = slices.Clone(entry.ServicePrices)
		for i := range entry.ServicePrices {
			rate := &entry.ServicePrices[i]
			rate.Factor = clonePointer(rate.Factor)
			rate.TextFactor = clonePointer(rate.TextFactor)
			if rate.Cost != nil {
				rate.Cost = new(cloneCost(*rate.Cost))
			}
			if rate.Factors != nil {
				rate.Factors = new(cloneCost(*rate.Factors))
			}
		}
		entry.Fast = clonePointer(entry.Fast)
		if entry.Fast != nil {
			*entry.Fast = cloneCost(*entry.Fast)
		}
		entry.Tiers = slices.Clone(entry.Tiers)
		for i := range entry.Tiers {
			entry.Tiers[i].Cost = cloneCost(entry.Tiers[i].Cost)
		}
		entry.Attributes = entry.Attributes.Clone()
		entry.AttributeSources = maps.Clone(entry.AttributeSources)
		result.Items = append(result.Items, entry)
	}
	return result
}

// Supports reports whether the named capability has an explicit true
// declaration. Unknown capability names, missing declarations, and explicit
// false values all fail the filter used by Index.Search.
func (a Attributes) Supports(capability string) bool {
	var value *bool
	switch capability {
	case "reasoning":
		value = a.Reasoning
	case "temperature":
		value = a.Temperature
	case "top_p":
		value = a.TopP
	case "attachment":
		value = a.Attachment
	case "tool_call":
		value = a.ToolCall
	case "structured_output":
		value = a.StructuredOutput
	}
	return value != nil && *value
}

// Len returns the number of prepared identities in this index.
func (index Index) Len() int {
	if index.data == nil {
		return 0
	}
	return len(index.data.entries)
}

// cloneCost copies populated price pointers for independently mutable query results.
func cloneCost(cost Cost) Cost {
	cost.Input = clonePointer(cost.Input)
	cost.Output = clonePointer(cost.Output)
	cost.CacheRead = clonePointer(cost.CacheRead)
	cost.CacheWrite = clonePointer(cost.CacheWrite)
	cost.CacheWrite5m = clonePointer(cost.CacheWrite5m)
	cost.CacheWrite1h = clonePointer(cost.CacheWrite1h)
	cost.CacheWrite30m = clonePointer(cost.CacheWrite30m)
	cost.CacheWrite12h = clonePointer(cost.CacheWrite12h)
	cost.ImageInput = clonePointer(cost.ImageInput)
	cost.ImageRead = clonePointer(cost.ImageRead)
	cost.ImageOutput = clonePointer(cost.ImageOutput)
	cost.PerImage = clonePointer(cost.PerImage)
	return cost
}
