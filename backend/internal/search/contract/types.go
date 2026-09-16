package contract

// SearchResult represents a single web search result.
type SearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	PageAge string `json:"page_age,omitempty"`
}

// SearchRequest describes a web search to perform.
type SearchRequest struct {
	Query      string
	MaxResults int    // defaults to defaultMaxResults if <= 0
	ProxyURL   string // optional HTTP proxy URL
}

// SearchResponse holds the results of a web search.
type SearchResponse struct {
	Results []SearchResult
	Query   string // the query that was actually executed
}

const DefaultMaxResults = 5

// Provider type identifiers.
const (
	ProviderTypeBrave  = "brave"
	ProviderTypeTavily = "tavily"
)

// ProviderConfig holds the configuration for a single search provider.
type ProviderConfig struct {
	Type         string `json:"type"`                    // ProviderTypeBrave | ProviderTypeTavily
	APIKey       string `json:"api_key"`                 // secret
	QuotaLimit   int64  `json:"quota_limit"`             // 0 = unlimited
	SubscribedAt *int64 `json:"subscribed_at,omitempty"` // subscription start (unix seconds); quota resets monthly from this date
	ProxyURL     string `json:"-"`                       // resolved proxy URL (not persisted)
	ProxyID      int64  `json:"-"`                       // resolved proxy ID for unavailability tracking
	ExpiresAt    *int64 `json:"expires_at,omitempty"`    // optional expiration (unix seconds)
}
