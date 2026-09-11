package pricing

// initFallbackPricing 初始化硬编码回退价格（当动态价格不可用时使用）
// 价格单位：USD per token（与LiteLLM格式一致）
func DefaultFallbackPrices() map[string]*ModelPricing {
	prices := make(map[string]*ModelPricing)
	// Claude 4.5 Opus
	prices["claude-opus-4.5"] = &ModelPricing{
		InputPricePerToken:         5e-6,    // $5 per MTok
		OutputPricePerToken:        25e-6,   // $25 per MTok
		CacheCreationPricePerToken: 6.25e-6, // $6.25 per MTok
		CacheReadPricePerToken:     0.5e-6,  // $0.50 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 4 Sonnet
	prices["claude-sonnet-4"] = &ModelPricing{
		InputPricePerToken:         3e-6,    // $3 per MTok
		OutputPricePerToken:        15e-6,   // $15 per MTok
		CacheCreationPricePerToken: 3.75e-6, // $3.75 per MTok
		CacheReadPricePerToken:     0.3e-6,  // $0.30 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3.5 Sonnet
	prices["claude-3-5-sonnet"] = &ModelPricing{
		InputPricePerToken:         3e-6,    // $3 per MTok
		OutputPricePerToken:        15e-6,   // $15 per MTok
		CacheCreationPricePerToken: 3.75e-6, // $3.75 per MTok
		CacheReadPricePerToken:     0.3e-6,  // $0.30 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3.5 Haiku
	prices["claude-3-5-haiku"] = &ModelPricing{
		InputPricePerToken:         1e-6,    // $1 per MTok
		OutputPricePerToken:        5e-6,    // $5 per MTok
		CacheCreationPricePerToken: 1.25e-6, // $1.25 per MTok
		CacheReadPricePerToken:     0.1e-6,  // $0.10 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3 Opus
	prices["claude-3-opus"] = &ModelPricing{
		InputPricePerToken:         15e-6,    // $15 per MTok
		OutputPricePerToken:        75e-6,    // $75 per MTok
		CacheCreationPricePerToken: 18.75e-6, // $18.75 per MTok
		CacheReadPricePerToken:     1.5e-6,   // $1.50 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 3 Haiku
	prices["claude-3-haiku"] = &ModelPricing{
		InputPricePerToken:         0.25e-6, // $0.25 per MTok
		OutputPricePerToken:        1.25e-6, // $1.25 per MTok
		CacheCreationPricePerToken: 0.3e-6,  // $0.30 per MTok
		CacheReadPricePerToken:     0.03e-6, // $0.03 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Claude 4.6 Opus (与4.5同价)
	prices["claude-opus-4.6"] = prices["claude-opus-4.5"]

	// Claude 4.7 Opus (暂与4.6同价，待官方定价更新)
	prices["claude-opus-4.7"] = prices["claude-opus-4.6"]

	// Claude 4.8 Opus（官方常规定价 $5/$25 per MTok，Fast mode 为 2 倍）
	prices["claude-opus-4.8"] = &ModelPricing{
		InputPricePerToken:         5e-6,    // 每百万 token $5
		OutputPricePerToken:        25e-6,   // 每百万 token $25
		CacheCreationPricePerToken: 6.25e-6, // 默认按 5 分钟缓存写入价
		CacheReadPricePerToken:     0.5e-6,  // 每百万 token $0.50
		CacheCreation5mPrice:       6.25e-6,
		CacheCreation1hPrice:       10e-6,
		SupportsCacheBreakdown:     true,
		SupportsServiceTier:        true,
	}
	prices["claude-opus-5"] = prices["claude-opus-4.8"]

	// Claude Fable 5.x 的输入/输出和缓存写入价格相同；5.1 的缓存读取价降为每百万 token 0.25 美元。
	prices["claude-fable-5"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheCreation5mPrice:       12.5e-6,
		CacheCreation1hPrice:       20e-6,
		CacheReadPricePerToken:     1e-6,
		SupportsCacheBreakdown:     true,
	}
	prices["claude-fable-5-1"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheCreation5mPrice:       12.5e-6,
		CacheCreation1hPrice:       20e-6,
		CacheReadPricePerToken:     0.25e-6,
		SupportsCacheBreakdown:     true,
	}

	// Gemini 3.1 Pro
	prices["gemini-3.1-pro"] = &ModelPricing{
		InputPricePerToken:         2e-6,   // $2 per MTok
		OutputPricePerToken:        12e-6,  // $12 per MTok
		CacheCreationPricePerToken: 2e-6,   // $2 per MTok
		CacheReadPricePerToken:     0.2e-6, // $0.20 per MTok
		SupportsCacheBreakdown:     false,
	}

	// Gemini 3.5 Flash（Google AI 定价：输入 $1.50、输出 $9、缓存输入 $0.15/百万 token）
	prices["gemini-3.5-flash"] = &ModelPricing{
		InputPricePerToken:     1.5e-6,
		OutputPricePerToken:    9e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}

	// Gemini 3.6 Flash（Google AI 定价：输入 $1.50、输出 $7.50、缓存输入
	// $0.15/百万 token）。下方会匹配 Antigravity 的 -high/-low/-medium/-tiered
	// 别名，避免远端定价不可用时把有 token 的请求记为 $0。
	prices["gemini-3.6-flash"] = &ModelPricing{
		InputPricePerToken:     1.5e-6,
		OutputPricePerToken:    7.5e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}

	// OpenAI GPT-5.4（业务指定价格）
	prices["gpt-5.4"] = &ModelPricing{
		InputPricePerToken:             2.5e-6,  // $2.5 per MTok
		InputPricePerTokenPriority:     5e-6,    // $5 per MTok
		OutputPricePerToken:            15e-6,   // $15 per MTok
		OutputPricePerTokenPriority:    30e-6,   // $30 per MTok
		CacheCreationPricePerToken:     2.5e-6,  // $2.5 per MTok
		CacheReadPricePerToken:         0.25e-6, // $0.25 per MTok
		CacheReadPricePerTokenPriority: 0.5e-6,  // $0.5 per MTok
		SupportsCacheBreakdown:         false,
	}
	// OpenAI GPT-5.5（按官方发布价格兜底）
	prices["gpt-5.5"] = &ModelPricing{
		InputPricePerToken:             5e-6,    // $5 per MTok
		InputPricePerTokenPriority:     12.5e-6, // $12.5 per MTok
		OutputPricePerToken:            30e-6,   // $30 per MTok
		OutputPricePerTokenPriority:    75e-6,   // $75 per MTok
		CacheCreationPricePerToken:     5e-6,    // $5 per MTok
		CacheReadPricePerToken:         0.5e-6,  // $0.5 per MTok
		CacheReadPricePerTokenPriority: 1.25e-6, // $1.25 per MTok
		SupportsCacheBreakdown:         false,
	}
	// OpenAI GPT-5.5 Pro（按官方发布价格兜底）
	prices["gpt-5.5-pro"] = &ModelPricing{
		InputPricePerToken:             30e-6,  // $30 per MTok
		InputPricePerTokenPriority:     75e-6,  // $75 per MTok
		OutputPricePerToken:            180e-6, // $180 per MTok
		OutputPricePerTokenPriority:    450e-6, // $450 per MTok
		CacheCreationPricePerToken:     30e-6,  // $30 per MTok
		CacheReadPricePerToken:         3e-6,   // $3 per MTok
		CacheReadPricePerTokenPriority: 7.5e-6, // $7.5 per MTok
		SupportsCacheBreakdown:         false,
	}

	// OpenAI GPT-6 Astra 官方标准价格（USD/token）。
	prices["gpt-6-astra"] = &ModelPricing{
		InputPricePerToken:         10e-6,
		OutputPricePerToken:        50e-6,
		CacheCreationPricePerToken: 12.5e-6,
		CacheReadPricePerToken:     1e-6,
		SupportsServiceTier:        true,
	}

	// OpenAI GPT-5.6 官方价格（USD/token）。缓存写入为输入价的 1.25 倍。
	prices["gpt-5.6-sol"] = &ModelPricing{
		InputPricePerToken:                 5e-6,
		InputPricePerTokenPriority:         10e-6,
		OutputPricePerToken:                30e-6,
		OutputPricePerTokenPriority:        60e-6,
		CacheCreationPricePerToken:         6.25e-6,
		CacheCreationPricePerTokenPriority: 12.5e-6,
		CacheReadPricePerToken:             0.5e-6,
		CacheReadPricePerTokenPriority:     1e-6,
		SupportsServiceTier:                true,
	}
	prices["gpt-5.6-terra"] = &ModelPricing{
		InputPricePerToken:                 2e-6,
		InputPricePerTokenPriority:         4e-6,
		OutputPricePerToken:                12e-6,
		OutputPricePerTokenPriority:        24e-6,
		CacheCreationPricePerToken:         2.5e-6,
		CacheCreationPricePerTokenPriority: 5e-6,
		CacheReadPricePerToken:             0.2e-6,
		CacheReadPricePerTokenPriority:     0.4e-6,
		SupportsServiceTier:                true,
	}
	prices["gpt-5.6-luna"] = &ModelPricing{
		InputPricePerToken:                 0.2e-6,
		InputPricePerTokenPriority:         0.4e-6,
		OutputPricePerToken:                1.2e-6,
		OutputPricePerTokenPriority:        2.4e-6,
		CacheCreationPricePerToken:         0.25e-6,
		CacheCreationPricePerTokenPriority: 0.5e-6,
		CacheReadPricePerToken:             0.02e-6,
		CacheReadPricePerTokenPriority:     0.04e-6,
		SupportsServiceTier:                true,
	}

	prices["gpt-5.4-mini"] = &ModelPricing{
		InputPricePerToken:     7.5e-7,
		OutputPricePerToken:    4.5e-6,
		CacheReadPricePerToken: 7.5e-8,
		SupportsCacheBreakdown: false,
	}
	prices["gpt-5.4-nano"] = &ModelPricing{
		InputPricePerToken:     2e-7,
		OutputPricePerToken:    1.25e-6,
		CacheReadPricePerToken: 2e-8,
		SupportsCacheBreakdown: false,
	}
	// OpenAI GPT-5.2（本地兜底）
	prices["gpt-5.2"] = &ModelPricing{
		InputPricePerToken:             1.75e-6,
		InputPricePerTokenPriority:     3.5e-6,
		OutputPricePerToken:            14e-6,
		OutputPricePerTokenPriority:    28e-6,
		CacheCreationPricePerToken:     1.75e-6,
		CacheReadPricePerToken:         0.175e-6,
		CacheReadPricePerTokenPriority: 0.35e-6,
		SupportsCacheBreakdown:         false,
	}
	// Codex 族兜底统一按 GPT-5.3 Codex 价格计费
	prices["gpt-5.3-codex"] = &ModelPricing{
		InputPricePerToken:             1.5e-6, // $1.5 per MTok
		InputPricePerTokenPriority:     3e-6,   // $3 per MTok
		OutputPricePerToken:            12e-6,  // $12 per MTok
		OutputPricePerTokenPriority:    24e-6,  // $24 per MTok
		CacheCreationPricePerToken:     1.5e-6, // $1.5 per MTok
		CacheReadPricePerToken:         0.15e-6,
		CacheReadPricePerTokenPriority: 0.3e-6,
		SupportsCacheBreakdown:         false,
	}

	// ============================================================
	// 国产大模型兜底定价（数据源：各家官方定价页，美元口径）
	// 顺序：DeepSeek → 智谱 GLM → 月之暗面 Kimi → MiniMax → 豆包 Embedding
	// 覆盖逻辑见同文件 getFallbackPricing()
	// ============================================================

	// ---- DeepSeek 系列 ----
	// 资料来源：https://api-docs.deepseek.com/quick_start/pricing
	// 下面存储官方低谷价；高峰倍率由 applyDeepSeekPeakPricing 按请求时刻计算。
	prices["deepseek-v4-pro"] = &ModelPricing{
		InputPricePerToken:     DeepseekProOffPeakInputPrice,
		OutputPricePerToken:    DeepseekProOffPeakOutputPrice,
		CacheReadPricePerToken: DeepseekProOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}
	prices["deepseek-v4-flash"] = &ModelPricing{
		InputPricePerToken:     DeepseekFlashOffPeakInputPrice,
		OutputPricePerToken:    DeepseekFlashOffPeakOutputPrice,
		CacheReadPricePerToken: DeepseekFlashOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}
	prices["deepseek-v4-flash-vision-exp"] = &ModelPricing{
		InputPricePerToken:     DeepseekFlashOffPeakInputPrice,
		OutputPricePerToken:    DeepseekFlashOffPeakOutputPrice,
		CacheReadPricePerToken: DeepseekFlashOffPeakCacheRead,
		SupportsCacheBreakdown: false,
	}

	// ---- 智谱 GLM（Z.AI）----
	// 资料来源：https://docs.z.ai/guides/overview/pricing（美元/百万 token）
	// CacheReadPricePerToken 对应缓存命中价；未公开缓存写入价时按 0 处理。
	// GLM-5.2 与 GLM-5.1 的公开价格一致。
	prices["glm-5.2"] = &ModelPricing{
		InputPricePerToken:     1.4e-6,
		OutputPricePerToken:    4.4e-6,
		CacheReadPricePerToken: 0.26e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-5.1"] = &ModelPricing{
		InputPricePerToken:     1.4e-6,
		OutputPricePerToken:    4.4e-6,
		CacheReadPricePerToken: 0.26e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-5"] = &ModelPricing{
		InputPricePerToken:     1e-6,
		OutputPricePerToken:    3.2e-6,
		CacheReadPricePerToken: 0.2e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-5-turbo"] = &ModelPricing{
		InputPricePerToken:     1.2e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.24e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.7"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.7-flashx"] = &ModelPricing{
		InputPricePerToken:     0.07e-6,
		OutputPricePerToken:    0.4e-6,
		CacheReadPricePerToken: 0.01e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.6"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.5"] = &ModelPricing{
		InputPricePerToken:     0.6e-6,
		OutputPricePerToken:    2.2e-6,
		CacheReadPricePerToken: 0.11e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.5-x"] = &ModelPricing{
		InputPricePerToken:     2.2e-6,
		OutputPricePerToken:    8.9e-6,
		CacheReadPricePerToken: 0.45e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.5-air"] = &ModelPricing{
		InputPricePerToken:     0.2e-6,
		OutputPricePerToken:    1.1e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.5-airx"] = &ModelPricing{
		InputPricePerToken:     1.1e-6,
		OutputPricePerToken:    4.5e-6,
		CacheReadPricePerToken: 0.22e-6,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4-32b-0414-128k"] = &ModelPricing{
		InputPricePerToken:     0.1e-6,
		OutputPricePerToken:    0.1e-6,
		SupportsCacheBreakdown: false,
	}
	// GLM Flash 在 z.ai 上免费，保留零价条目防止未知别名误计费。
	prices["glm-4.5-flash"] = &ModelPricing{
		InputPricePerToken:     0,
		OutputPricePerToken:    0,
		SupportsCacheBreakdown: false,
	}
	prices["glm-4.7-flash"] = &ModelPricing{
		InputPricePerToken:     0,
		OutputPricePerToken:    0,
		SupportsCacheBreakdown: false,
	}

	// ---- 月之暗面 Kimi（K 系列）----
	// 资料来源：https://platform.moonshot.cn/docs/pricing/overview
	// Moonshot V1 与旧 K2 型号未保留清晰美元价，不做宽泛回退。
	prices["kimi-k3"] = &ModelPricing{
		InputPricePerToken:     3e-6,
		OutputPricePerToken:    15e-6,
		CacheReadPricePerToken: 0.30e-6,
		SupportsCacheBreakdown: false,
	}
	prices["kimi-k2.6"] = &ModelPricing{
		InputPricePerToken:     0.95e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	// kimi-for-coding 走 Kimi Coding 接口，按当前 K2.6 coding 档位兜底计费。
	prices["kimi-for-coding"] = &ModelPricing{
		InputPricePerToken:     0.95e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	prices["kimi-k2.5"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    3e-6,
		CacheReadPricePerToken: 0.098e-6,
		SupportsCacheBreakdown: false,
	}
	prices["kimi-k2-thinking"] = &ModelPricing{
		InputPricePerToken:     0.56e-6,
		OutputPricePerToken:    2.24e-6,
		CacheReadPricePerToken: 0.14e-6,
		SupportsCacheBreakdown: false,
	}
	prices["kimi-k2"] = &ModelPricing{
		InputPricePerToken:     0.56e-6,
		OutputPricePerToken:    2.24e-6,
		CacheReadPricePerToken: 0.14e-6,
		SupportsCacheBreakdown: false,
	}

	// ---- MiniMax M 系列 ----
	// 资料来源：https://platform.minimax.io/docs/guides/pricing-paygo
	// M3 长上下文高价档不在兜底中拆分，沿用标准档以避免高估。
	prices["minimax-m3"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    2.40e-6,
		CacheReadPricePerToken: 0.12e-6,
		SupportsCacheBreakdown: false,
	}
	prices["minimax-m2.7"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.06e-6,
		SupportsCacheBreakdown: false,
	}
	prices["minimax-m2.7-highspeed"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    2.40e-6,
		CacheReadPricePerToken: 0.06e-6,
		SupportsCacheBreakdown: false,
	}
	prices["minimax-m2.5"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	prices["minimax-m2.1"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}
	prices["minimax-m2"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    1.20e-6,
		CacheReadPricePerToken: 0.03e-6,
		SupportsCacheBreakdown: false,
	}

	// ---- 火山方舟 豆包 Embedding（多模态向量化）----
	// doubao-embedding-vision 回传图文 token 拆分，文本与图片输入按不同价计费。
	prices["doubao-embedding-vision"] = &ModelPricing{
		InputPricePerToken:      0.098e-6, // ¥0.7/MTok ≈ $0.098
		ImageInputPricePerToken: 0.252e-6, // ¥1.8/MTok ≈ $0.252
		OutputPricePerToken:     0,
		SupportsCacheBreakdown:  false,
	}

	// xAI Grok 4.5：20 万 token 以下每百万输入 $2、缓存输入 $0.30、输出 $6。
	prices["grok-4.5"] = &ModelPricing{
		InputPricePerToken:            2e-6,
		OutputPricePerToken:           6e-6,
		CacheReadPricePerToken:        0.3e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// xAI Grok 4.6：20 万 token 以下每百万输入 $2、缓存输入 $0.50、输出 $6；
	// 达到 20 万后输入、缓存输入和输出均按 2 倍结算。
	prices["grok-4.6"] = &ModelPricing{
		InputPricePerToken:            2e-6,
		OutputPricePerToken:           6e-6,
		CacheReadPricePerToken:        0.5e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// xAI Grok 4.3：20 万 token 以下每百万输入 $1.25、缓存输入 $0.20、输出 $2.50。
	prices["grok-4.3"] = &ModelPricing{
		InputPricePerToken:            1.25e-6,
		OutputPricePerToken:           2.5e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}
	// Grok 4.20 variants share the official $1.25 / $0.20 / $2.50 card
	// (and $2.50 / $0.40 / $5 long-context rates) with Grok 4.3.
	prices["grok-4.20"] = &ModelPricing{
		InputPricePerToken:            1.25e-6,
		OutputPricePerToken:           2.5e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}

	// Grok 3 Mini 保留独立历史价格，避免按 Grok 4.5 通用回退价计费。
	prices["grok-3-mini"] = &ModelPricing{
		InputPricePerToken:     0.30e-6,
		OutputPricePerToken:    0.50e-6,
		CacheReadPricePerToken: 0.075e-6,
		SupportsCacheBreakdown: false,
	}
	prices["grok-3-mini-fast"] = &ModelPricing{
		InputPricePerToken:     0.60e-6,
		OutputPricePerToken:    4e-6,
		CacheReadPricePerToken: 0.15e-6,
		SupportsCacheBreakdown: false,
	}
	// xAI Grok Build 0.1 官方价格为输入 $1、缓存输入 $0.20、输出 $2/百万 token。
	// Composer 仅通过 Grok Build 提供且没有独立公开价格，因此其别名沿用该编程模型价格，
	// 避免被静默按零费用结算。
	prices["grok-build-0.1"] = &ModelPricing{
		InputPricePerToken:            1e-6,
		OutputPricePerToken:           2e-6,
		CacheReadPricePerToken:        0.2e-6,
		SupportsCacheBreakdown:        false,
		LongContextInputThreshold:     200000,
		LongContextThresholdInclusive: true,
		LongContextInputMultiplier:    2,
		LongContextOutputMultiplier:   2,
	}
	return prices
}
