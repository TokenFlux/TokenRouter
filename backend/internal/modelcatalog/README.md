# 模型目录与官方补充

默认价格与展示属性来自 models.dev 的 `https://models.dev/catalog.json?type=all`。`catalog.json.gz` 保存经过校验的离线快照，`model_supplements.json` 按型号保存价格、展示属性及其官方来源；两者通过 `go:embed` 编入二进制。启动时加载磁盘缓存或离线目录，后台使用 ETag 同步，完整验证成功后才一起发布价格、操作价格和属性。更新失败保留最近有效版本。

官方补充随二进制更新和回退，无需安装外部资源，不依赖工作目录。仓库中的 JSON 仍是唯一维护源，修改后需要重新编译。每个模型条目记录 `source_url` 和 `verified_at`，填写目录缺失、已核实的字段。价格字段放在模型条目内，展示属性放在同一条目的 `attributes` 对象中。目录已有的价格、明确零价、属性值及空模态集合优先。型号按完整 ID 匹配。

## 自定义补充与价格配置

- `pricing.fallback_file` 默认为空，可填写独立的自定义 JSON 路径。目录已有字段（包括零价）优先，其次是自定义补充，最后由内嵌补充填缺。媒体尺寸表等复合字段按整个字段选择，不拼接不同来源的尺寸表。
- 操作价格的 `_billing_defaults` 按字段叠加到内嵌默认值，显式零价仍然有效。删除自定义文件会恢复内嵌补充，不会删除官方默认价。修改和删除在下次目录检查或手动更新时生效；程序和安装器都不改写自定义文件。
- 自定义售价通过管理端「价格配置」设置，再关联相应分组。
- `pricing.override_file` 和 `PRICING_OVERRIDE_FILE` 已退役。旧文件不读取、不自动迁移，启动时提示弃用；升级前迁移需要保留的售价，提供商成本另行配置。
- 保留旧 JSON 字段及 `litellm_provider` 的读取兼容；同时存在时 `provider` 优先，包括空值和 `null`。
- 默认每 10 分钟检查；管理员更新目录可立即重载。远程返回 304 或远程地址为空时，自定义文件修改和删除仍生效。非法单价、规则、属性或非对象条目拒绝本次更新，价格和属性一起保留最近有效版本。

同一原厂记录的裸名和供应商限定名共享补充，精确键优先；其他供应商、日期版本及档位后缀不会自动继承。保留 `source` 和 `price_sources` 作为实际字段来源，来源类别与 `source_url` 分开保存。

## 展示属性

`attributes` 使用模型属性接口的字段名，例如 `display_name`、`context`、`input_modalities`、`output_modalities` 和 `structured_output`。自定义补充和内嵌补充共用此格式。补充优先级按字段计算，目录已有值优先，其次为自定义文件，最后为内嵌文件。旧价格条目中的 `supported_modalities` 等顶层字段继续按价格兼容规则读取，展示层读取 `attributes`。

Jev 的三个官方请求型号在同一条目里声明价格和属性。models.dev 公共条目提供模型资料，各渠道报价按供应商记录隔离。TypeSafe 直连缺少目录报价时，使用官方补充；目录补上直连报价后，该报价优先。`jev-preview` 和 `jev-1.13.0` 的属性按官方资料登记，未来型号需要另行核验。

## 计价维度

价格统一使用美元。旧 token 字段单位为美元/token；`image_prices` 的 1K/2K/4K 键为美元/张，`video_prices` 的 480p/720p/1080p 键为美元/秒。不同单位不能替代；缺失尺寸不按固定倍率补价。旧 `output_cost_per_image` 表示不分尺寸的单张价，仍可读取。

`fast_multiplier`、`flex_multiplier`、`max_reasoning_effort_multiplier` 描述明确倍率；`cache_write_multiplier` 和 `cache_write_1h_multiplier` 仅在对应缓存单价缺失时由输入价派生，显式零价优先。`time_pricing` 使用现有价格配置的时区、每日时段和倍率结构。未声明规则时不根据型号产生加价或折扣。

`_billing_defaults` 是操作价格保留节点，不是模型。可用字段为 `web_search_price_per_call`、`search_price_per_1k`、`audio_realtime_price_per_min`、`audio_tts_price_per_million_chars`、`audio_stt_price_per_hour`。单位分别是美元/次、美元/千次、美元/分钟、美元/百万字符、美元/小时。管理员价格配置优先；`sources` 逐字段记录依据，`verified_at` 记录核验日期。

当前分发数据保留 OpenAI Web Search、xAI TTS 和 REST STT 的操作价。统一搜索次数无法表达 X Search 按帖子和档案的收费方式，通用实时音频时长也不足以覆盖不同语音型号，因此这两类操作不提供统一默认金额。部署者可以明确配置自己的售价。

Gemini 生图目录的 `cost.output` 是图片 token 价，补充只提供独立文本输出价及公布的尺寸单价。GPT Image 1/mini/1.5 补齐目录缺少的媒体价格桶。xAI 图片和视频只登记已核实的输出尺寸单价；`grok-imagine-image-2.0` 的自动质量随生成/编辑变化，现有计价输入没有质量维度，因此不分发该型号的统一默认单价。输入媒体、质量等尚未进入现有用量结构的维度不会伪装成输出价格。

## 维护

新增补充前，使用实际目录解析结果检查完整供应商身份和价格桶。确认数据源缺口后再登记官方依据；无法核实则保持缺价。目录补齐后删除重复字段。离线快照与 MIT 许可保存在 `backend/internal/modelcatalog/`，不能用补充文件替换完整缓存。

价格清理不重算历史账单或任务资金快照，也不改变既有缺价处理及提供商协议规则。
