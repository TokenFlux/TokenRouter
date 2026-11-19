package provider

import "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"

// usage_catalog.go lists supported upstream usage adapters and filters
// administrator-selectable formats. Usage queries expose
// administrator-selectable relay formats alongside platform-selected Kimi,
// Zhipu, and DeepSeek and OpenCode Go formats. The catalog keeps those
// categories together so diagnostics can name every adapter while administrator
// choices remain limited to the manual formats.

// Adapter identifiers select fixed registered usage formats and identify
// normalized results returned by manual queries or upstream usage monitoring.
const (
	UpstreamUsageAdapterOpenCodeGo      = usageview.UpstreamUsageAdapterOpenCodeGo      // OpenCode Go allowance format
	UpstreamUsageAdapterSub2API         = usageview.UpstreamUsageAdapterSub2API         // Sub2API relay usage format
	UpstreamUsageAdapterNewAPI          = usageview.UpstreamUsageAdapterNewAPI          // New API usage format
	UpstreamUsageAdapterDerouter        = usageview.UpstreamUsageAdapterDerouter        // derouter.ai account balance format
	UpstreamUsageAdapterZivv            = usageview.UpstreamUsageAdapterZivv            // Zivv wallet usage format
	UpstreamUsageAdapterKimiCoding      = usageview.UpstreamUsageAdapterKimiCoding      // Kimi coding percentage format
	UpstreamUsageAdapterZhipuCoding     = usageview.UpstreamUsageAdapterZhipuCoding     // Zhipu coding percentage format
	UpstreamUsageAdapterKimiBalance     = usageview.UpstreamUsageAdapterKimiBalance     // Kimi wallet balance format
	UpstreamUsageAdapterDeepseekBalance = usageview.UpstreamUsageAdapterDeepseekBalance // DeepSeek multicurrency balance format
	// Wallet credential keys supply optional New API user authentication when
	// an API Key cannot read its wallet balance.
	NewAPIUserAccessTokenCredentialKey = "new_api_user_access_token" // wallet access token key
	NewAPIUserIDCredentialKey          = "new_api_user_id"           // wallet user ID key
	UpstreamUsageDefaultAdapter        = UpstreamUsageAdapterSub2API // default relay query format

	// Default management roots apply when the resolved adapter queries a host
	// separate from model forwarding and no query address override is saved.
	DerouterUsageDefaultBaseURL   = "https://cf-api.derouter.ai" // derouter account management root
	OpenCodeGoUsageDefaultBaseURL = "https://opencode.ai"        // OpenCode Go allowance root
)

// usageAdapterCatalog associates each supported format with its display name and
// automatic selection rule for catalog and administrator-option consumers.
var usageAdapterCatalog = []UsageAdapterSpec{
	{Name: UpstreamUsageAdapterOpenCodeGo, Label: "OpenCode Go", Automatic: true, DefaultBaseURL: OpenCodeGoUsageDefaultBaseURL},
	{Name: UpstreamUsageAdapterSub2API, Label: "Sub2API / TokenRouter", Automatic: false},
	{Name: UpstreamUsageAdapterNewAPI, Label: "New API", Automatic: false},
	{Name: UpstreamUsageAdapterZivv, Label: "Zivv", Automatic: false},
	{Name: UpstreamUsageAdapterDerouter, Label: "derouter.ai (Account Key)", Automatic: false, DefaultBaseURL: DerouterUsageDefaultBaseURL},
	{Name: UpstreamUsageAdapterKimiCoding, Label: "Kimi Coding Plan", Automatic: true},
	{Name: UpstreamUsageAdapterZhipuCoding, Label: "Zhipu Coding Plan", Automatic: true},
	{Name: UpstreamUsageAdapterKimiBalance, Label: "Kimi Balance", Automatic: true},
	{Name: UpstreamUsageAdapterDeepseekBalance, Label: "DeepSeek Balance", Automatic: true},
}

// UsageAdapterSpec names a usage format, its selection category, and its
// optional dedicated management root.
type UsageAdapterSpec struct {
	Name, Label string // registered adapter name and administrator display label
	// DefaultBaseURL supplies the official management root when an adapter queries
	// a host separate from model forwarding and no override is configured.
	DefaultBaseURL string
	// Automatic excludes the adapter from administrator choices because
	// PlatformUsageAdapter selects it from platform and provider mode.
	Automatic bool // platform selects adapter
}

// DefaultUsageAdapterBaseURL returns the catalog's default management root for
// name, or empty when the adapter shares the provider's forwarding host.
func DefaultUsageAdapterBaseURL(name string) string {
	for _, registration := range usageAdapterCatalog {
		if registration.Name == name {
			return registration.DefaultBaseURL
		}
	}
	return ""
}

// UpstreamUsageAdapterCatalog returns a new slice containing every catalog entry
// in registration order. Entries contain strings and a boolean, so callers may
// edit the returned values independently.
func UpstreamUsageAdapterCatalog() []UsageAdapterSpec {
	return append([]UsageAdapterSpec(nil), usageAdapterCatalog...)
}

// UpstreamUsageAdapterOptions returns administrator choices in catalog order,
// excluding adapters selected automatically by platform and mode. Each call
// builds a new slice of name and label values.
func UpstreamUsageAdapterOptions() []UpstreamUsageAdapterOption {
	options := make([]UpstreamUsageAdapterOption, 0, len(usageAdapterCatalog))
	for _, registration := range usageAdapterCatalog {
		if registration.Automatic {
			continue
		}
		options = append(options, UpstreamUsageAdapterOption{Name: registration.Name, Label: registration.Label})
	}
	return options
}
