package modelcatalog

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A bare identity can have ambiguous service prices and still have reliable
// public attributes. Fixtures vary canonical authorship and original endpoints
// to verify metadata lookup, first party preference, explicit zero prices, and
// missing price values independently.

// ambiguousImageCatalog supplies different service modalities and prices with
// one shared public image model identity.
const ambiguousImageCatalog = `{
	"models":{"openai/gpt-image-2.5-flare":{
		"name":"GPT Image 2.5 Flare",
		"modalities":{"input":["text","image"],"output":["image"]}
	}},
	"providers":{
		"google-vertex":{"models":{"gpt-image-2.5-flare":{
			"canonical_model_id":"openai/gpt-image-2.5-flare",
			"modalities":{"input":["text"],"output":["image"]},
			"cost":{"input":1,"output":2}
		}}},
		"ollama-cloud":{"models":{"gpt-image-2.5-flare":{
			"canonical_model_id":"openai/gpt-image-2.5-flare",
			"cost":{"input":3,"output":4}
		}}}
	}
}`

// TestLookupAttributesCanonicalFallback uses service entries that agree on a
// public author identity. Bare metadata must use copied public attributes while
// prices remain qualified and exact service metadata retains its own omissions.
func TestLookupAttributesCanonicalFallback(t *testing.T) {
	for _, variant := range []string{"flare", "sunburst"} {
		t.Run(variant, func(t *testing.T) {
			model := "gpt-image-2.5-" + variant
			catalog, err := Parse([]byte(strings.ReplaceAll(ambiguousImageCatalog, "flare", variant)))
			require.NoError(t, err)
			attributes, found := catalog.LookupAttributes(model, nil)
			require.True(t, found)
			require.Equal(t, []string{"text", "image"}, *attributes.InputModalities)
			require.Equal(t, []string{"image"}, *attributes.OutputModalities)

			// Canonical agreement makes public attributes available for this bare
			// identity, while independent price lookup still requires a
			// provider-qualified record.
			require.True(t, catalog.RequiresExact(model))
			_, found = catalog.Lookup([]string{model})
			require.False(t, found)
			(*attributes.InputModalities)[0] = "audio"
			again, found := catalog.LookupAttributes(model, nil)
			require.True(t, found)
			require.Equal(t, []string{"text", "image"}, *again.InputModalities)

			qualified, found := catalog.LookupAttributes("google-vertex/"+model, []string{model})
			require.True(t, found)
			require.Equal(t, []string{"text"}, *qualified.InputModalities)
			price, found := catalog.Lookup([]string{"google-vertex/" + model})
			require.True(t, found)
			require.Equal(t, 1.0, *price.Cost.Input)
			// The exact service entry has no input modalities declared. Its known
			// identity takes precedence over public metadata, leaving that
			// capability unknown.
			qualified, found = catalog.LookupAttributes("ollama-cloud/"+model, []string{model})
			require.True(t, found)
			require.Nil(t, qualified.InputModalities)
			_, found = catalog.LookupAttributes("google-vertex/missing", nil)
			require.False(t, found)
		})
	}
}

// TestLookupAttributesRejectsUncertainCanonical changes canonical agreement
// and public metadata availability. An unknown author, conflicting identity,
// or missing public record must leave bare attributes unresolved.
func TestLookupAttributesRejectsUncertainCanonical(t *testing.T) {
	for _, canonical := range []string{"", "other/model", "openai/missing"} {
		t.Run(canonical, func(t *testing.T) {
			// Change one provider's authorship while retaining the other record
			// and public metadata, so failure depends on canonical agreement
			// across both candidates.
			body := strings.Replace(ambiguousImageCatalog,
				`"canonical_model_id":"openai/gpt-image-2.5-flare"`,
				`"canonical_model_id":"`+canonical+`"`, 1)
			catalog, err := Parse([]byte(body))
			require.NoError(t, err)
			_, found := catalog.LookupAttributes("gpt-image-2.5-flare", nil)
			require.False(t, found)
		})
	}
	// Both providers now agree on an identity absent from the public collection.
	// Agreement alone cannot supply attributes when their canonical model has
	// no metadata.
	body := strings.ReplaceAll(ambiguousImageCatalog,
		`"canonical_model_id":"openai/gpt-image-2.5-flare"`,
		`"canonical_model_id":"openai/missing"`)
	catalog, err := Parse([]byte(body))
	require.NoError(t, err)
	_, found := catalog.LookupAttributes("gpt-image-2.5-flare", nil)
	require.False(t, found)
}

// TestLookupAttributesKeepsOriginalEndpoint replaces one service with the
// original author endpoint. Its exact bare entry must keep endpoint modalities
// even when the public collection declares additional input support.
func TestLookupAttributesKeepsOriginalEndpoint(t *testing.T) {
	body := strings.Replace(ambiguousImageCatalog, `"google-vertex":`, `"openai":`, 1)
	catalog, err := Parse([]byte(body))
	require.NoError(t, err)
	attributes, found := catalog.LookupAttributes("gpt-image-2.5-flare", nil)
	require.True(t, found)
	require.Equal(t, []string{"text"}, *attributes.InputModalities)
}

// TestOfflineEmbeddingOrigin reads embedded models whose records omit
// canonical identities. Known OpenAI endpoint recognition must still make
// their bare names unambiguous and retain the qualified entry's price values.
func TestOfflineEmbeddingOrigin(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	for _, model := range []string{"text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002"} {
		t.Run(model, func(t *testing.T) {
			entry, found := catalog.Lookup([]string{model})
			require.True(t, found)
			require.True(t, entry.FirstParty)
			require.Equal(t, "openai", entry.Provider)
			require.False(t, catalog.Ambiguous[model])
			qualified, found := catalog.Lookup([]string{"openai/" + model})
			require.True(t, found)
			require.Equal(t, qualified.Cost, entry.Cost)
		})
	}
}

// TestKnownOriginWithoutCanonicalAndExplicitForeignOrigin combines known
// endpoints, foreign authorship, and competing unknown relays. Endpoint
// recognition must select its own embedding record, explicit authorship wins
// for foreign models, and unrecognized relay records are excluded.
func TestKnownOriginWithoutCanonicalAndExplicitForeignOrigin(t *testing.T) {
	catalog, err := Parse([]byte(`{
		"providers": {
			"openai": {"models": {
				"embedding": {"cost":{"input":1,"output":0}},
				"foreign": {"canonical_model_id":"author/foreign","cost":{"input":9,"output":9}}
			}},
			"azure": {"models":{"embedding":{"cost":{"input":2,"output":0}}}},
			"author": {"models":{"foreign":{"canonical_model_id":"author/foreign","cost":{"input":3,"output":4}}}},
			"relay-a": {"models":{"unknown":{"cost":{"input":5,"output":6}}}},
			"relay-b": {"models":{"unknown":{"cost":{"input":7,"output":8}}}}
		}
	}`))
	require.NoError(t, err)
	entry, found := catalog.Lookup([]string{"embedding"})
	require.True(t, found)
	require.Equal(t, "openai", entry.Provider)
	_, found = catalog.Lookup([]string{"azure/embedding"})
	require.False(t, found)
	entry, found = catalog.Lookup([]string{"foreign"})
	require.True(t, found)
	require.Equal(t, "author", entry.Provider)
	require.NotContains(t, catalog.Entries, "unknown")
	require.NotContains(t, catalog.Ambiguous, "unknown")
}

// TestMistralOriginRespectsForeignCanonical gives Mistral a local model and
// another author's model. Bare lookup must prefer Mistral for its own record
// and the declared author for the foreign identity.
func TestMistralOriginRespectsForeignCanonical(t *testing.T) {
	catalog, err := Parse([]byte(`{"providers":{
		"mistral":{"models":{
			"devstral-latest":{"cost":{"input":0.4,"output":2}},
			"glm-test":{"canonical_model_id":"zhipuai/glm-test","cost":{"input":9,"output":9}}
		}},
		"requesty":{"models":{"devstral-latest":{"cost":{"input":0.44,"output":2.2}}}},
		"zai":{"models":{"glm-test":{"canonical_model_id":"zhipuai/glm-test","cost":{"input":1,"output":2}}}}
	}}`))
	require.NoError(t, err)
	entry, found := catalog.Lookup([]string{"devstral-latest"})
	require.True(t, found)
	require.Equal(t, "mistral", entry.Provider)
	entry, found = catalog.Lookup([]string{"glm-test"})
	require.True(t, found)
	require.Equal(t, "zai", entry.Provider)
	_, found = catalog.Lookup([]string{"mistral/glm-test"})
	require.False(t, found)
}

// TestOfflineCatalog decompresses and parses the embedded models.dev
// snapshot. A registered Claude identity must expose its Anthropic provider,
// known input price, and declared context limit for offline startup.
func TestOfflineCatalog(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	catalog, err := Parse(body)
	require.NoError(t, err)
	entry, ok := catalog.Lookup([]string{"claude-sonnet-4-5"})
	require.True(t, ok)
	require.Equal(t, "anthropic", entry.Provider)
	require.Equal(t, 3.0, *entry.Cost.Input)
	require.NotNil(t, entry.Attributes.Context)
}

// TestCatalogIdentityAndUnpricedAttributes combines an origin's zero input
// rate, service prices, and attribute-only records. Exact qualification must
// retain each provider's price, while ambiguous bare names and missing prices
// remain distinct from declared zero prices.
func TestCatalogIdentityAndUnpricedAttributes(t *testing.T) {
	catalog, err := Parse([]byte(`{"models":{"origin/m":{"name":"Original"}},"providers":{"google-vertex":{"models":{"m":{"name":"Relay","cost":{"input":9,"output":9}},"unpriced":{"name":"No price","reasoning":false}}},"origin":{"models":{"m":{"name":"Original","cost":{"input":0,"output":2}}}},"ollama-cloud":{"models":{"unpriced":{"name":"Other"}}}}}`))
	require.NoError(t, err)
	original, ok := catalog.Lookup([]string{"m"})
	require.True(t, ok)
	require.Equal(t, 0.0, *original.Cost.Input)
	service, ok := catalog.Lookup([]string{"google-vertex/m"})
	require.True(t, ok)
	require.Equal(t, 9.0, *service.Cost.Input)
	_, ambiguous := catalog.Lookup([]string{"unpriced"})
	require.False(t, ambiguous)
	noPrice, ok := catalog.Lookup([]string{"google-vertex/unpriced"})
	require.True(t, ok)
	require.Nil(t, noPrice.Cost.Input)
	require.NotNil(t, noPrice.Attributes.Reasoning)
	require.False(t, *noPrice.Attributes.Reasoning)
}

// TestCatalogFiltersProviderOfferings combines public metadata, an author's
// zero price, and relay-only offerings. Removed records must disappear from
// searches and exact lookup while public metadata and provider qualifiers remain.
func TestCatalogFiltersProviderOfferings(t *testing.T) {
	catalog, err := Parse([]byte(`{
		"models":{"openai/public-only":{"name":"Public","limit":{"context":64}}},
		"providers":{
			"openai":{"models":{"original":{"cost":{"input":0,"output":2}}}},
			"openrouter":{"models":{
				"original":{"canonical_model_id":"openai/original","cost":{"input":9,"output":9}},
				"relay-only":{"cost":{"input":1,"output":2}},
				"public-only":{"canonical_model_id":"openai/public-only","limit":{"context":2}}
			}}
		}
	}`))
	require.NoError(t, err)
	original, found := catalog.Lookup([]string{"original"})
	require.True(t, found)
	require.True(t, original.FirstParty)
	require.Zero(t, *original.Cost.Input)
	public, found := catalog.Lookup([]string{"public-only"})
	require.True(t, found)
	require.Equal(t, 64, *public.Attributes.Context)
	require.Nil(t, public.Cost.Input)
	require.True(t, catalog.Providers["openrouter"])
	for _, model := range []string{"relay-only", "openrouter/relay-only", "openrouter/original", "openrouter/public-only"} {
		_, found = catalog.Lookup([]string{model})
		require.False(t, found, model)
		require.NotContains(t, NewIndex(catalog).ModelIDs(), model)
	}
	attrs, found := catalog.LookupAttributes("openrouter/original", []string{"original"})
	require.False(t, found)
	require.Nil(t, attrs.Context)
	resolved := catalog.Publication().LookupRoute("", "", "openrouter/public-only")
	require.Nil(t, resolved.Attributes.Context)
	require.True(t, catalog.RequiresExact("openrouter/original"))
	page := NewIndex(catalog).Search(AttributeQuery{})
	require.Equal(t, []string{"openai"}, page.Providers)
	require.Empty(t, NewIndex(catalog).Search(AttributeQuery{Provider: "openrouter"}).Items)
}

// TestCatalogAuthorRecognition checks missing canonical links and mixed-host
// author namespaces. Explicit foreign authorship must override endpoint and
// namespace recognition, while recognized authors retain prices without public metadata.
func TestCatalogAuthorRecognition(t *testing.T) {
	tests := []struct {
		provider  string // source provider identity
		model     string // complete requested model
		canonical string // declared author identity
		retained  bool   // expected author recognition
	}{
		{"openai", "embedding", "", true},
		{"alibaba", "qwen-mt-turbo", "", true},
		{"stepfun", "stepaudio-2.5-tts", "", true},
		{"upstage", "solar-mini", "", true},
		{"nvidia", "nvidia/nv-embed-v1", "", true},
		{"vispark", "vispark/vision-small", "", true},
		{"nvidia", "meta/llama", "", false},
		{"vispark", "openai/model", "", false},
		{"nvidia", "nvidia/", "", false},
		{"nvidia", "bare-model", "", false},
		{"nvidia", "bare-model", "nvidia/author-model", true},
		{"nvidia", "nvidia/model", "meta/model", false},
		{"vispark", "vispark/model", "openai/model", false},
		{"alibaba", "foreign", "deepseek/model", false},
		{"openrouter", "gpt-test", "openai/gpt-test", false},
		{"unknown", "unknown", "", false},
		{"newlab", "new-model", "newlab/new-model", true},
		{"author", "public-model", "", true},
	}
	for _, test := range tests {
		t.Run(test.provider+"/"+test.model+"/"+test.canonical, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"models":{"author/public-model":{"name":"Author model"}},
				"providers":{%q:{"models":{%q:{"canonical_model_id":%q,"cost":{"input":1,"output":2}}}}}
			}`, test.provider, test.model, test.canonical)
			catalog, err := Parse([]byte(body))
			require.NoError(t, err)
			entry, found := catalog.Lookup([]string{test.provider + "/" + test.model})
			require.Equal(t, test.retained, found)
			if found {
				require.True(t, entry.FirstParty)
				require.Equal(t, 1.0, *entry.Cost.Input)
			}
		})
	}
}

// TestCatalogRetainsRuntimeServiceRecords supplies service-specific limits,
// controls, and prices beside an author's record. Each registered service must
// remain searchable and resolve its own attributes without winning author preference.
func TestCatalogRetainsRuntimeServiceRecords(t *testing.T) {
	for _, provider := range []string{
		"amazon-bedrock", "google-vertex", "google-vertex-anthropic", "ollama-cloud",
		"kimi-code-plan-cn", "kimi-code-plan-global", "zhipuai-coding-plan", "zai-coding-plan",
	} {
		t.Run(provider, func(t *testing.T) {
			body := fmt.Sprintf(`{"providers":{
				"openai":{"models":{"runtime-model":{"limit":{"context":128},"cost":{"input":1,"output":2}}}},
				%q:{"models":{"runtime-model":{
					"canonical_model_id":"openai/runtime-model","limit":{"context":32},
					"reasoning":true,"reasoning_options":[{"type":"effort","values":["low","high"]}],
					"temperature":false,"modalities":{"input":[]},"cost":{"input":0,"output":8}
				}}}
			}}`, provider)
			catalog, err := Parse([]byte(body))
			require.NoError(t, err)
			entry, found := catalog.Lookup([]string{provider + "/runtime-model"})
			require.True(t, found)
			require.False(t, entry.FirstParty)
			require.Zero(t, *entry.Cost.Input)
			require.Equal(t, 8.0, *entry.Cost.Output)
			require.False(t, *entry.Attributes.Temperature)
			require.NotNil(t, entry.Attributes.InputModalities)
			require.Empty(t, *entry.Attributes.InputModalities)
			result := catalog.Publication().LookupRoute(provider, provider, "runtime-model")
			require.Equal(t, 32, *result.Attributes.Context)
			require.Equal(t, []string{"low", "high"}, result.Attributes.Efforts())
			author, found := catalog.Lookup([]string{"runtime-model"})
			require.True(t, found)
			require.Equal(t, "openai", author.Provider)
			page := NewIndex(catalog).Search(AttributeQuery{Provider: provider})
			require.Contains(t, page.Providers, provider)
			require.Len(t, page.Items, 1)
			require.Equal(t, provider+"/runtime-model", page.Items[0].Model)
		})
	}
}

// TestOfflineCatalogFiltering compares the parsed publication with its raw
// embedded snapshot. Public model identities and runtime services must survive,
// while known relay rows disappear without relying on fixed catalog counts.
func TestOfflineCatalogFiltering(t *testing.T) {
	body, err := Offline()
	require.NoError(t, err)
	var source map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &source))
	catalog, err := ParseV2(body)
	require.NoError(t, err)
	page := NewIndex(catalog).Search(AttributeQuery{})
	require.Less(t, len(page.Providers), len(source))
	for _, provider := range []string{"openrouter", "302ai", "requesty"} {
		require.Contains(t, source, provider)
		require.True(t, catalog.Providers[provider])
		require.NotContains(t, page.Providers, provider)
		for _, entry := range page.Items {
			require.NotEqual(t, provider, entry.Provider)
		}
	}
	require.NotContains(t, catalog.Entries, "azure/gpt-5.4")
	for _, id := range []string{
		"volcengine/doubao-seed-2-0-pro-260215",
		"volcengine-coding-plan/doubao-seed-2.0-lite",
		"minimax-cn/MiniMax-M3", "minimax-coding-plan/MiniMax-M3",
		"minimax-cn-coding-plan/MiniMax-M3",
		"alibaba-cn/qwen3-max",
		"alibaba-coding-plan/qwen3-coder-next", "alibaba-coding-plan-cn/qwen3-coder-next",
		"alibaba-token-plan/happyhorse-1.1-t2v", "alibaba-token-plan-cn/happyhorse-1.1-t2v",
		"stepfun-ai/step-3.5-flash", "stepfun-ai-step-plan/step-3.5-flash", "stepfun-step-plan/step-3.5-flash",
		"xiaomi-token-plan-cn/mimo-v2.6-pro", "xiaomi-token-plan-ams/mimo-v2.6-pro", "xiaomi-token-plan-sgp/mimo-v2.6-pro",
		"tencent-coding-plan/hunyuan-2.0-thinking", "tencent-tokenhub/hy3", "tencent-token-plan/hy3",
		"longcat/LongCat-2.0", "nova/nova-2-lite-v1",
		"arcee/trinity-large-thinking", "watsonx/ibm/granite-4-h-small",
		"azure/phi-4", "azure-cognitive-services/phi-4", "github-copilot/mai-code-1.1-flash",
		"inception/mercury-2", "morph/morph-v3-large", "sensenova/sensenova-6.8-flash-lite",
		"fireworks-ai/accounts/fireworks/models/ember-1", "pioneer/fastino/gliner2-base-v1",
		"cloudflare-workers-ai/@cf/cloudflare/clef", "clarifai/clarifai/main/models/mm-poly-8b",
		"agnes/agnes-2.5-flash", "abliteration-ai/abliterated-model-large", "lucidquery/lucidnova-rf1-100b",
	} {
		entry, found := catalog.Lookup([]string{id})
		require.True(t, found, id)
		require.True(t, entry.FirstParty, id)
	}
	for _, provider := range []string{"amazon-bedrock", "google-vertex", "google-vertex-anthropic", "ollama-cloud"} {
		require.Contains(t, page.Providers, provider)
	}
	_, err = Parse([]byte(`{"providers":{"relay":{"models":{"unknown":{}}}}}`))
	require.ErrorContains(t, err, "empty model catalog")
}

// TestCatalogRetainsFutureMediaModels supplies video-only public metadata and
// official offerings through independent service brands. Media attributes must
// survive before gateway support exists, while matching relay offerings remain filtered.
func TestCatalogRetainsFutureMediaModels(t *testing.T) {
	catalog, err := Parse([]byte(`{
		"models":{
			"bytedance-seed/seedance-2.5":{"type":"video","modalities":{"output":["video","audio"]},"limit":{"context":0,"output":0}},
			"minimax/MiniMax-H3":{"type":"video","modalities":{"output":["video","audio"]}},
			"runway/gen-4.5":{"type":"video","modalities":{"output":["video"]}}
		},
		"providers":{
			"volcengine":{"models":{"doubao-seedance-2-5-260628":{
				"canonical_model_id":"bytedance-seed/seedance-2.5","type":"video","modalities":{"output":["video","audio"]}
			}}},
			"minimax-cn":{"models":{"MiniMax-H3":{"type":"video","modalities":{"output":["video","audio"]}}}},
			"vercel":{"models":{"seedance-2.5":{"canonical_model_id":"bytedance-seed/seedance-2.5"}}}
		}
	}`))
	require.NoError(t, err)
	for _, id := range []string{
		"bytedance-seed/seedance-2.5", "minimax/MiniMax-H3", "runway/gen-4.5",
		"volcengine/doubao-seedance-2-5-260628", "minimax-cn/MiniMax-H3",
	} {
		entry, found := catalog.Lookup([]string{id})
		require.True(t, found, id)
		require.Contains(t, *entry.Attributes.OutputModalities, "video", id)
		require.Nil(t, entry.Attributes.Context)
		require.Nil(t, entry.Cost.Input)
	}
	require.NotContains(t, catalog.Entries, "vercel/seedance-2.5")
	require.Contains(t, NewIndex(catalog).ModelIDs(), "minimax-cn/minimax-h3")
}

// TestCatalogOrdinaryAPIPreference compares ordinary, subscription, and regional
// offerings for one model. Service class determines preference independently
// of zero prices, while equally preferred sources remain ambiguous.
func TestCatalogOrdinaryAPIPreference(t *testing.T) {
	tests := []struct {
		name      string  // named source selection scenario
		model     string  // shared complete model identity
		providers string  // source provider JSON object
		provider  string  // expected bare-name provider
		input     float64 // expected source-unit input price
	}{
		{"ordinary before free plan", "glm-test", `{
			"zai":{"models":{"glm-test":{"cost":{"input":2,"output":3}}}},
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}}
		}`, "zai", 2},
		{"free ordinary before paid plan", "glm-test", `{
			"zai":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}},
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":2,"output":3}}}}
		}`, "zai", 0},
		{"original ordinary endpoint", "glm-test", `{
			"zai":{"models":{"glm-test":{"cost":{"input":2,"output":3}}}},
			"zhipuai":{"models":{"glm-test":{"cost":{"input":1,"output":2}}}},
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}}
		}`, "zhipuai", 1},
		{"single plan remains queryable", "glm-test", `{
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}}
		}`, "zai-coding-plan", 0},
		{"two plans remain ambiguous", "glm-test", `{
			"zhipuai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}},
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}}
		}`, "", 0},
		{"ordinary peers remain ambiguous", "qwen-test", `{
			"alibaba-cn":{"models":{"qwen-test":{"cost":{"input":1,"output":2}}}},
			"modelscope":{"models":{"qwen-test":{"cost":{"input":1,"output":2}}}},
			"alibaba-coding-plan":{"models":{"qwen-test":{"cost":{"input":0,"output":0}}}}
		}`, "", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := Parse([]byte(`{"providers":` + test.providers + `}`))
			require.NoError(t, err)
			entry, found := catalog.Lookup([]string{test.model})
			if test.provider == "" {
				require.False(t, found)
				require.True(t, catalog.Ambiguous[test.model])
			} else {
				require.True(t, found)
				require.Equal(t, test.provider, entry.Provider)
				require.Equal(t, test.input, *entry.Cost.Input)
			}
			for provider := range catalog.Providers {
				qualified, found := catalog.Lookup([]string{provider + "/" + test.model})
				require.True(t, found)
				require.True(t, qualified.FirstParty)
				require.NotNil(t, qualified.Cost.Input)
			}
		})
	}
	t.Run("ordinary metadata remains unpriced", func(t *testing.T) {
		catalog, err := Parse([]byte(`{"providers":{
			"zai":{"models":{"glm-test":{"limit":{"context":200000}}}},
			"zai-coding-plan":{"models":{"glm-test":{"cost":{"input":0,"output":0}}}}
		}}`))
		require.NoError(t, err)
		entry, found := catalog.Lookup([]string{"glm-test"})
		require.True(t, found)
		require.Equal(t, "zai", entry.Provider)
		require.Nil(t, entry.Cost.Input)
		require.Nil(t, entry.Cost.Output)
	})
}

// TestCatalogProtectsQualifiedAliases mixes hosted names with public identities
// and original author namespaces. Hosted aliases must respect service ownership,
// while NVIDIA and an author's independent brand retain legitimate exact aliases.
func TestCatalogProtectsQualifiedAliases(t *testing.T) {
	catalog, err := Parse([]byte(`{
		"models":{
			"xai/public":{"limit":{"context":2000000}},
			"lab/public-only":{"limit":{"context":80}},
			"nvidia/model":{"limit":{"context":300}},
			"fastino/model":{"limit":{"context":80}}
		},
		"providers":{
			"xai":{"models":{"other":{}}},
			"openai":{"models":{"other":{}}},
			"nvidia":{"models":{"nvidia/model":{"limit":{"context":400},"cost":{"input":1,"output":2}}}},
			"pioneer":{"models":{"fastino/model":{"limit":{"context":100},"cost":{"input":1,"output":2}}}},
			"google-vertex":{"models":{
				"xai/public":{"limit":{"context":128000},"cost":{"input":9,"output":10}},
				"lab/public-only":{"limit":{"context":7},"cost":{"input":9,"output":10}},
				"openai/missing":{"limit":{"context":7},"cost":{"input":9,"output":10}},
				"custom/path":{"limit":{"context":12},"cost":{"input":1,"output":2}}
			}}
		}
	}`))
	require.NoError(t, err)
	for id, context := range map[string]int{"xai/public": 2000000, "lab/public-only": 80} {
		entry, found := catalog.Lookup([]string{id})
		require.True(t, found)
		require.Equal(t, context, *entry.Attributes.Context)
		require.Nil(t, entry.Cost.Input)
		hosted, found := catalog.Lookup([]string{"google-vertex/" + id})
		require.True(t, found)
		require.Equal(t, 9.0, *hosted.Cost.Input)
	}
	require.NotContains(t, catalog.Entries, "openai/missing")
	require.Contains(t, catalog.Entries, "google-vertex/openai/missing")
	for id, provider := range map[string]string{
		"nvidia/model": "nvidia", "nvidia/nvidia/model": "nvidia",
		"fastino/model": "pioneer", "pioneer/fastino/model": "pioneer", "custom/path": "google-vertex",
	} {
		entry, found := catalog.Lookup([]string{id})
		require.True(t, found)
		require.Equal(t, provider, entry.Provider)
		require.Equal(t, 1.0, *entry.Cost.Input)
	}
}

// TestUnknownServiceMetadataDoesNotInvalidatePrices supplies an unfamiliar SDK
// declaration for one model. Pricing and public attributes remain readable,
// while protocol selection receives no invented encoding for the service model.
func TestUnknownServiceMetadataDoesNotInvalidatePrices(t *testing.T) {
	catalog, err := Parse([]byte(`{"providers":{"openai":{"models":{"gpt-4":{"provider":"future-format","type":{"future":true},"cost":{"input":1},"limit":{"input":8192}}}}},"models":{"openai/gpt-4":{"name":"GPT-4"}}}`))
	require.NoError(t, err)
	entry, found := catalog.Lookup([]string{"openai/gpt-4"})
	require.True(t, found)
	require.Equal(t, new(8192), entry.Attributes.InputLimit)
	require.Empty(t, catalog.Publication().Offering("openai", "gpt-4").SDK)
}

// TestNormalizeRemoteCatalogURL updates known official URLs and keeps
// configured mirror or local-only locations. Explicit model-type filtering
// survives the endpoint switch without touching credential-bearing URLs.
func TestNormalizeRemoteCatalogURL(t *testing.T) {
	for input, want := range map[string]string{
		"https://models.dev/catalog.json?type=all": RemoteURL + "?type=all",
		"https://models.dev/api.json?type=chat":    RemoteURL + "?type=chat",
		"https://mirror.example/catalog.json":      "https://mirror.example/catalog.json",
		"":                                         "",
	} {
		require.Equal(t, want, NormalizeRemoteURL(input))
	}
}
