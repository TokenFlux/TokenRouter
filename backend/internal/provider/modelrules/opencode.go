package modelrules

import (
	"maps"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// opencode.go translates service SDK declarations from the catalog into OpenCode
// protocol suggestions. The service model remains the outbound route, while its
// canonical author supplies attributes independently of that service's protocol.

// embeddedOpenCodeProtocols pins the bundled defaults used before a saved
// provider has a live observation. Only the two protocol maps remain in memory.
var embeddedOpenCodeProtocols = sync.OnceValue(func() map[string]map[string]string {
	raw, err := modelcatalog.Offline()
	if err != nil {
		return nil
	}
	catalog, err := modelcatalog.ParseV2(raw)
	if err != nil {
		return nil
	}
	view := NewView(catalog.Publication())
	return map[string]map[string]string{"payg": view.OpenCodeProtocols("payg"), "go": view.OpenCodeProtocols("go")}
})

// OpenCodeProtocols copies bundled defaults so callers can apply manual changes
// without modifying another account or mode. Unknown modes yield an empty table.
func OpenCodeProtocols(mode string) map[string]string {
	routes := embeddedOpenCodeProtocols()[mode]
	if routes == nil {
		return map[string]string{}
	}
	return maps.Clone(routes)
}

// openCodeService identifies the catalog service for the selected subscription mode.
func openCodeService(mode string) string {
	switch mode {
	case "payg":
		return "opencode"
	case "go":
		return "opencode-go"
	}
	return ""
}

// OpenCodeProtocols builds protocol suggestions for one retained catalog publication.
// Decision models use SystemOne irrespective of the service SDK. Unknown SDKs
// supply no suggestion, allowing explicit administrator overrides to select the wire.
func (v View) OpenCodeProtocols(mode string) map[string]string {
	routes := map[string]string{}
	offers := v.catalog.ServiceOfferings(openCodeService(mode))
	if offers == nil {
		return nil
	}
	for id, offering := range offers {
		switch {
		case offering.Operation == "decision":
			routes[id] = "systemone"
		case offering.Protocol != "":
			routes[id] = offering.Protocol
		case offering.SDK == "@ai-sdk/openai":
			routes[id] = "openai_responses"
		case offering.SDK == "@ai-sdk/openai-compatible":
			routes[id] = "openai_chat_completions"
		case offering.SDK == "@ai-sdk/anthropic":
			routes[id] = "anthropic_messages"
		case offering.SDK == "@ai-sdk/google":
			routes[id] = "gemini_generate_content"
		}
	}
	return routes
}
