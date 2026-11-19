# 模型目录与市场

本文描述可请求模型目录如何依据分组、价格配置和提供商能力解析，以及如何整理为公开和个人模型广场的品牌、价格、容量与可用性。供应商动态模型清单不在本文列举；用量结算事务见[路由与结算](../domains/routing_and_billing.md)。

## 章节导航

- [目录解析](#目录解析)：修改 `/models`、默认模型或可请求交集时读取。
- [市场可见性](#市场可见性)：修改公开分组过滤、品牌或排序时读取。
- [目录元数据查询](#目录元数据查询)：修改名称写法、档位别名和能力来源时读取。
- [原厂归属](#model_catalog_origins)：维护厂商别名、区域入口和原厂筛选时读取。
- [目录价格换算](#目录价格换算)：修改 models.dev 报价换算、补充文件或发布校验时读取。
- [价格展示](#价格展示)：修改价格配置的价格、倍率或未知价格时读取。
- [管理员默认价格查询](#gateway_default_pricing)：修改默认价格页或目录更新时读取。
- [模型属性配置](#model_attributes)：修改默认属性、分组覆盖或客户端配置导出时读取。
- [容量与可用性](#group_availability_probe)：修改公开容量或探测时间序列时读取。
- [一致性要求](#一致性要求)：核对目录、市场与实际调度是否一致。

<a id="model_catalog_resolution"></a>
## 目录解析

公开市场由 `routing.Marketplace` 汇总分组、价格、属性、容量和可用性。`modelcatalog/provider.Service` 为管理端、网关和市场提供同一版本的价格与模型元数据。对外目录的候选只来自已保存的配置和平台执行规则。

### 候选生成与白名单规则

`routing.RequestableResolver` 从以下来源收集具体型号作为候选：

- 提供商白名单、提供商映射和分组映射中的具体来源与具体目标（例如 `client-* -> gpt-5.6-sol` 会提供候选 `gpt-5.6-sol`）。
- 分组策略和已启用自定义模型列表中的具体型号。
- Qoder、Bedrock 等平台执行规则提供的有限型号集合。

解析按以下顺序执行：

1. 为本次查询准备当前提供商的只读模型规则，复用映射、精确白名单索引、通配规则和协议集合；分组策略和计费来源各读取一次（多分组页面可预取并共享提供商记录）。
2. 在候选中匹配通配符，并执行分组策略、协议和提供商资格校验。
3. 在 Key 层追加可用的精确别名与复合前缀。

候选规则与边界：

- 目录候选规模与 models.dev 条目数无关，名称、价格和属性只按已选中型号查询。`gateway.models_list_cache_ttl_seconds` 仅保留配置解析兼容，旧的候选短缓存已停用。
- 提供商缺失、解析失败，或既无具体型号配置又无平台可枚举型号时，返回空目录；管理员可配置白名单、模型映射，或同步上游后保存。
- `model_whitelist` 缺失或为空时允许任意模型 ID，目录未收录的型号也可进入请求处理。非空白名单在提供商一跳 `model_mapping` 之后检查最终模型，支持精确匹配和末尾通配符；历史非空白名单继续生效。
- Spark 型号配额、OpenAI OAuth 资格、Bedrock 区域路由、Qoder 站点资格和协议检查各自独立执行。
- 模型 ID 是客户端请求键，display name 是展示名，pricing model 是查价键，三者可以不同。Key 级重定向和分组映射须保证 `/v1/models`、`/models`、实际调度与响应模型恢复一致，目标不可请求的别名不出现在列表中。

### 网关、图片与创作台目录

- 网关模型列表与 Gemini 模型资源共用同一可请求解析。`gateway/provider.ModelDisplayCatalogue` 按完整 ID 查询统一目录，未知名称返回原始 ID；协议适配器填充字段结构，缺失时间用空值或零值。列表仅代表通过本地规则的候选，上游能否执行以实际请求为准；Key 别名与复合 Key 前缀按各自配置处理。
- 批量图片从提供商和分组配置中的具体名称生成候选，在分组和提供商映射完成后查询最终型号的图片属性。最终路线的输出模态必须明确包含 `image`，未知或空模态不进入图片目录。
- Gemini 模型列表和创作台目录在属性配置读取失败时返回不可用错误。

### 管理端测试、白名单与分组配置

管理员提供商测试通过 `routing.AdminCatalog` 校验具体配置与平台规则中的型号，再按 ID 查询展示元数据，OAuth、透传和国产平台使用同一流程。新建分组的编辑建议与管理员搜索使用统一元数据目录。

| 展示场景 | 名称与标识规则 |
| --- | --- |
| 模型测试、定时测试、配置选择器 | 显示实际模型 ID；空目录提示配置白名单或同步上游，手动测试支持直接输入型号 |
| 模型广场与首页卡片 | 显示有效属性中的名称；首页精选卡片按公开型号 ID 中已核实的模型家族显示开发商署名，未识别时省略 |
| 市场分组标题 | 显示分组的展示品牌，表示服务入口 |

白名单、分组自定义列表与探针配置规则：

- 白名单表单按需搜索统一属性目录，明确传入的候选范围直接显示，支持手动添加未知型号。新建或切换平台时白名单为空；上游同步合并提供商实际返回的列表到表单，保存后才成为后续目录查询的候选来源。
- 分组编辑页的模型候选与探测模型选项复用 `RequestableResolver`，按已保存的分组映射、白名单检查阶段、协议和提供商能力返回请求模型名。启用自定义列表时，具体型号先过资格校验再按自定义顺序筛选；通过检查的映射目标也可出现。未保存的策略改动须先保存再重新加载候选。
- 自定义模型列表按已保存配置恢复勾选与顺序，其余候选默认不勾选，支持逐项选择或全选。关闭自定义列表仍保留已选项供再次启用恢复；加载候选后直接保存空配置，列表保持为空。
- 分组探针的 `availability_probe_config.protocol` 使用统一协议 ID，缺失或空串表示「自动」（沿用提供商选择与测试类型推断）。
- 探针指定协议时，先按分组入口准入、模型规则和转换策略筛选提供商，再将入口协议、解析出的上游协议和分组映射模型交给测试器；提供商映射执行一次，属性按最终上游模型和两协议匹配，历史记配置的请求模型。无法执行的候选会被排除，全不满足时记失败；关闭已选入口后，保存时须重选协议或禁用探针。

### 用户分组目录与客户端配置导出

- 用户可见的分组目录返回 `models: string[]` 和 `model_protocols: Record<string, ProtocolID[]>`（各模型当前可请求的客户端入口），两者只用于控制台展示和配置生成，请求时仍执行权限与资格校验。
- Key 列表内嵌分组缺少目录时，前端按分组 ID 复用可见分组列表。`/api/v1/groups/available` 的 `include_models` 默认为 `true`，使用记录和仪表盘筛选传 `false` 以跳过模型、协议和属性解析。
- Claude Code、Codex、Gemini、Grok、OpenCode 和 CC Switch 的导出按所选客户端协议与实际模型生成：存在 `model_protocols` 时按其过滤，目录为空时不虚构示例。
- 导出遇到 Key 级映射（精确或最长末尾通配符）时按一跳目标整理模型和协议，复合 Key 示例加回绑定前缀；导出不会按品牌或模型名追加 `/antigravity` 等强制平台路径，`tf` CLI 回环协议保持原格式。

<a id="marketplace_visibility"></a>
## 市场可见性

| 接口 | 认证与缓存 | 可见分组范围 |
| --- | --- | --- |
| `/api/v1/marketplace/models` | 公开（首页复用） | `active`、非 `exclusive`、至少有一个 `active` 提供商且有可请求模型的分组 |
| `/api/v1/marketplace/models/available` | JWT，`Cache-Control: private, no-store` | 公开分组 + 当前用户 `AllowedGroups` 授权的专属分组（管理员同样按本人授权查询） |

可见性与展示规则：

- 专属分组授权读取当前账户存储，读取失败返回错误，客户端不能指定其他用户或范围。专属分组同样须满足启用、活跃提供商和可请求模型条件；容量与探测历史只查询筛选后的可见分组。
- 响应字段 `is_exclusive` 标识专属分组。前端在品牌标签前显示金色皇冠「专属」胶囊并将专属分组置顶；两类内部按后台顺序和 ID 排序，不因临时价格或实时错误重排。
- 个人禁用列表不影响市场浏览，价格沿用分组报价（不叠加个人倍率或团队范围）。切换账户立即清空旧目录，请求代次与账户身份校验阻止迟到响应写回。
- 展示品牌优先取 `display_brand`，为空时取分组名。公开分组返回描述、品牌、倍率和模型数，不暴露单一上游平台；模型保留自身厂商与能力信息，与展示品牌无关。
- 分组「基本信息」提供「显示最高优惠标签」和「显示倍率标签」（默认开启），存在 `routing_policy.features_config` 的 `hide_marketplace_max_discount` 和 `hide_marketplace_rate_multiplier`（`true` 隐藏，随创建、编辑、复制保存）。
- 市场接口将上述开关下发为 `hide_max_discount` 和 `hide_rate_multiplier` 供前端隐藏标题徽章，不影响计价与兑换计算；最高优惠标签仍要求存在有效折扣。

模型属性与协议展示：

- 模型项携带 `attributes`（有效显示名、长度、模态与能力），`input_modalities` 和 `output_modalities` 保留为兼容字段。未知字段不按价格或名称猜测，仅旧接口缺少 `attributes` 时前端回退历史模态推断；属性读取失败不影响价格展示。
- `protocols` 取自 `RequestableModel.Protocols`（与 `model_protocols` 同源）。`native_protocols` 表示承接该模型的每个提供商对该协议都无需转换；只要任一提供商需要转换，就不算原生。
- 模型广场在模型名旁的信息浮层顶部展示协议（有属性或协议时显示信息图标）：原生协议带绿色描边，悬停显示端点和是否原生，默认选中「全部协议」，点击可切换完整属性。卡片始终显示总体模态并集，浮层内固定位置并将所选协议明确不支持的模态置灰，未知模态以中性色和「未知」提示；关闭浮层重置选择；无 `protocol_attributes` 时显示静态协议标签。
- 未登录用户无法读取管理员协议目录，前端在 `utils/marketplaceProtocols.ts` 维护协议顺序、品牌和端点，由单元测试通过 `protocol-catalog.json` 与后端核对一致。
- TypeSafe 品牌键为 `typesafe`，`jev`、`jev-latest` 和 `typesafe/jev-latest` 共用该图标与筛选键，SystemOne 协议图标也使用 TypeSafe。

<a id="model_catalog_metadata_lookup"></a>
## 目录元数据查询

`modelcatalog` 解析 models.dev 统一目录并维护供应商身份索引、属性和内容版本；`modelcatalog/provider.Service` 负责下载、缓存和原子发布；`billing/pricing` 将目录报价归一化为 `CatalogModelPricing` 并解释本地补充和计费规则。

### 同步、离线快照与发布机制

| 项目 | 规则与默认值 |
| --- | --- |
| 远程入口 | `https://models.dev/experimental/v2.0/api.json` |
| 检查周期 | `model_catalog.check_interval_minutes`（默认 10 分钟），结合 ETag 与本地内容摘要，支持管理员强制更新 |
| 磁盘缓存 | `models_dev_catalog_v2.json`，完整解析校验成功后通过临时文件原子替换 |
| 离线兜底 | 二进制内嵌已校验压缩快照、官方价格补充及 MIT 许可，随版本同步升降级；首启或网络故障时使用磁盘缓存或内嵌快照 |
| 原子发布 | 价格与属性同版本发布，并构建排序型号、属性检索与价格分类索引；本地补充指纹纳入目录版本 |
| 失败语义 | 候选目录解析或校验失败时保留上一有效版本，管理员接口返回当前版本、最近成功时间与最近错误 |

同步与查询细节：

- 本地文件合并前，旧字段 `litellm_provider` 归一化为 `provider`（新旧并存时以新字段为准，含空值与 `null`）。内部来源分类 `catalog` 不覆盖实际出处；远程返回 304 时仍校验本地层。
- 普通查询复用内存目录与单型号身份索引，管理列表先筛选分页再复制返回条目。代理失败策略和 URL 校验沿用现有出站规则。

### 原厂与托管渠道筛选

v2 目录按提供商和完整型号解析服务记录，通过 `canonical_id` 与原厂服务条目索引公共属性；仅 v2 未发布且业务依赖的属性由已核验本地补充提供。

原厂识别独立于网关平台、协议和已配置提供商，未接入的视频、语音、图片模型也可进入目录。归属判定中，明确的 v2 `canonical_id` 优先于公共资料匹配、端点和型号家族规则（详见[原厂归属](#model_catalog_origins)）。

除原厂记录外，运行时默认保留以下托管或套餐服务的完整属性与价格（其原厂身份仍按归属规则判断）：

- `amazon-bedrock`、`google-vertex`、`google-vertex-anthropic`、`ollama-cloud`
- `kimi-code-plan-cn`、`kimi-code-plan-global`、`zhipuai-coding-plan`、`zai-coding-plan`

筛选与渠道隔离规则：

- 其余非原厂记录在建立型号索引前即被排除（远程下载、磁盘重载、内嵌快照一致），不进入默认属性、价格、搜索和自动补全。例如 Azure 上的 Microsoft Phi 和 Copilot 上的 MAI 属原厂记录，其托管的 OpenAI、Anthropic 型号按各自作者识别。
- 磁盘缓存保存原始响应，ETag 与发布校验不受影响。查询仍识别原始服务商名，被排除的供应商限定名返回缺失，不能去前缀借用原厂属性或报价；页面服务商选项只由保留条目生成。本地补充和管理员覆盖继续按完整 ID 生效；曾依赖被排除渠道默认价的型号在无显式配置时保持未定价。
- OpenCode 服务索引保留 `opencode` 和 `opencode-go` 的编码与 canonical 属性身份；缺少 canonical 声明时，仅接受完整型号匹配到唯一公共作者模型的关系。计费身份与渠道价格筛选保持独立，见 [OpenCode 上游](opencode_upstream.md#opencode_routing)。
- v2 默认包含 `decision` 等特殊类型。Jev 1.13、1.14 及 latest/preview 的图片输入与附件能力由目录或补充文件提供，输出为 `text` 结构化决策；TypeSafe 直连缺失属性由补充文件填写，第三方报价按规则排除。特殊模型可进入属性与查价索引，可请求性仍由提供商配置与协议决定。

### 裸名解析、别名与完整 ID 规则

裸模型名按以下优先级选择来源：

1. 优先从原厂记录中选普通 API；无普通 API 时再考虑原厂套餐。
2. 在选定类别中，优先服务商 ID 与作者 ID 相同的主入口。
3. 是否为零价不影响优先级；同级存在多个候选时保留歧义。

完整 ID 与别名边界：

- 供应商限定查询保留各自属性和报价，例如 `glm-5-turbo` 取 Z.AI 普通 API 报价，`zai-coding-plan/glm-5-turbo` 查套餐记录。无服务商记录或报价的公共模型资料仍参与属性查询。
- 完整供应商限定名由该服务独占；无同名服务的公共 canonical ID 可关联明确的原厂记录；托管服务使用自身前缀；原厂自带命名空间的请求 ID 继续有效（例如 `xai/grok-4.1-fast-reasoning` 保留公共资料，Vertex 专属上下文和报价用 `google-vertex/xai/grok-4.1-fast-reasoning`）。
- 公开市场通过 `billing.PriceResolver.PublicQuote` 查价，与结算同源。价格和属性按完整模型 ID 查询，只清理大小写、首尾空白和已知 Google 资源路径，保持供应商来源隔离。Claude 的点号与短横线、日期版本和档位后缀均代表独立身份，不互换、不补全、不从基础条目拼接字段。
- 价卡先按原始请求名匹配精确与通配规则，再查同一型号的协议资源路径候选；完整型号的手动价卡和零价全部保留，查价不按分组或平台隔离，缺失时返回未定价，不剥离日期、effort 或供应商前缀借价。
- Gemini、OpenAI、Claude 和 Grok 的 `high`、`low`、`medium`、`tiered`、`max`、`thinking` 等后缀按完整 ID 处理，不生成基础候选或 effort；Grok 默认文本模型只用于省略模型的请求，不参与别名查询。
- GPT-5.6 系列的 `gpt-5.6-sol/terra/luna` 与裸 `gpt-5.6` 分别解析，裸名资格由白名单决定并按完整名查价。
- 模型编码规则依据已核验 canonical 作者、完整型号和上游协议选择；服务身份仍决定 OAuth、站点和账户策略，别名不改变实际提供商平台。请求缺省思考参数与字段清理使用同一模型身份。

### 属性模态、歧义回退与缺价边界

- 模型属性读取 models.dev 的 `modalities.input/output`（支持文字、图片、音频、视频、PDF）。字段缺失和零长度表示未知或不适用，明确的 `false` 和空数组保留。旧本地价格 JSON 中的模态推断仅用于兼容价格调用方。
- 裸模型名因多个保留服务产生歧义时，属性查询在精确查找失败后检查这些记录的 `canonical_model_id`：全部指向同一公共模型且公共资料存在时，使用该公共模型的属性；否则保持未知。此回退不适用于供应商限定名，不补全已有精确条目的字段，也不影响价格索引、价格歧义或借用中继报价。
- 目录和手动配置均未命中时返回缺价，不查询静态 Go 价格表，也不按日期、系列、Spark 重定向、默认型号或遍历顺序猜价。价格规则不作为能力查询的继承依据，也不改写公开模型 ID 或上游请求 ID。

<a id="model_catalog_origins"></a>
## 原厂归属

服务商 ID 表示 API 产品，模型作者 ID 表示研发厂商，两者可以不同。归属按以下顺序判定：

1. 读取明确的 canonical 作者。
2. 缺失时检查同一作者的公共资料（含已带作者前缀的请求 ID）。
3. 资料仍缺失时，原厂专用入口接受新增型号；混合托管入口仅接受已核验的原厂型号家族或命名空间。

新版本号无需逐个登记，未确认的新品牌或家族须核实后补充。Coding、Token 和 Step Plan 的套餐身份单独保存以支持裸名优先级，其原厂归属、明确零价和服务属性照常保留。以下关系由 `modelcatalog/origins.go` 集中维护：

| 服务入口 | 模型作者 | 归属依据与保留范围 |
| --- | --- | --- |
| MiniMax 国内、国际及 Token/Coding Plan | `minimax` | [MiniMax 文档](https://platform.minimax.io/docs/guides/quickstart)，包含文本、[视频](https://platform.minimax.io/docs/guides/video-generation)、图像和语音型号 |
| 火山方舟、火山 Coding Plan、BytePlus | `bytedance-seed` | [火山视频 API](https://docs.volcengine.com/docs/ark/video-generation-tutorial?lang=en)、[BytePlus 视频 API](https://docs.byteplus.com/en/docs/ModelArk/create-video-generation-task-api)；Doubao、Seed、Seedance、Seedream、Dreamina 等自研家族 |
| 阿里国内、国际、Coding/Token Plan、ModelScope | `alibaba` | [阿里模型目录](https://www.alibabacloud.com/help/en/model-studio/models)；Qwen、QwQ、QVQ、Wan、HappyHorse、HappyOyster、通义及自研语音和嵌入家族 |
| 腾讯 Coding Plan、Token Plan、TokenHub | `tencent` | [TokenHub 文档](https://cloud.tencent.com/document/product/1823/130050)；Hunyuan、HunyuanVideo 和 HY 家族 |
| StepFun 国内、国际及 Step Plan | `stepfun` | [国内](https://platform.stepfun.com/docs/zh/overview/concept)与[国际](https://platform.stepfun.ai/docs/en/overview/concept)官方端点 |
| Xiaomi 各地区 Token Plan | `xiaomi` | [MiMo 平台](https://platform.xiaomimimo.com/#/docs)，地区差异保留各自服务属性 |
| Moonshot 国内、国际及 Kimi Coding | `moonshotai` | [Moonshot](https://platform.moonshot.ai/docs/api/chat)、[Kimi Coding](https://www.kimi.com/code/docs/en/kimi-code/models.html) |
| Zhipu、Z.AI 及 Coding Plan | `zhipuai` | [GLM 官方文档](https://docs.z.ai/guides/overview/pricing)，保留国内和国际服务身份 |
| 百灵 Bailing | `inclusionai` | [蚂蚁百灵](https://www.antgroup.com/technology/new-tech-Technology-Antfocuses-Tabcomnent-detail/20241028001)，包含 Ling、Ring、Ming |
| LongCat | `meituan` | [LongCat 官方 API](https://longcat.chat/platform/docs/APIDocs.html) |
| Nova、Bedrock 的 Amazon 型号 | `amazon` | [Nova 官方端点](https://nova.amazon.com/dev/documentation)；Bedrock 另保留运行时所需的托管型号 |
| Meta 的 Llama API | `meta` | [Meta 模型文档](https://dev.meta.ai/docs)，保留快照中的官方 Llama 入口 |
| Arcee | `arcee-ai` | [Arcee 文档](https://docs.arcee.ai)，包括 Trinity 家族 |
| watsonx | `ibm` | [IBM 官方目录](https://www.ibm.com/docs/en/watsonx/saas?topic=solutions-supported-foundation-models)，保留 IBM/Granite 型号 |
| Azure、Azure Cognitive Services、GitHub Copilot | `microsoft` | [Microsoft MAI](https://microsoft.ai/news/introducingmai-code-1-flash/) 和 Phi；其他作者的托管记录按其 canonical 归属筛选 |
| Fireworks | `fireworks` | [Ember 自研模型](https://fireworks.ai/blog/ember-1)，`accounts/fireworks/models/` 本身是托管路径，不能整段放行 |
| Pioneer | `fastino` | [Fastino 的 Pioneer](https://fastino.ai/blog/introducing-pioneer)与[自研模型](https://fastino.ai/models/fastino-models)，保留 `fastino/` 型号 |

其他归属边界：

- NVIDIA、Vispark、Clarifai、Cloudflare 等混合目录通过作者命名空间和公共资料识别自研条目；Inception、Morph、Agnes、Abliteration、LucidQuery 等自研或自训模型同样保留。
- 路由器、第三方模型套餐和仅换别名的条目不因自有品牌归为原厂。Liquid Inference 交易平台与 Liquid AI 公共作者同名，归属表中分开处理，Liquid AI 公共资料照常保留。
- 模型类型、缺少 token 价格或上下文为零均不构成原厂过滤条件。独立作者资料仅在服务记录或核验补充提供时可查，目录不会按型号名称补造属性、报价或网关能力。

## 目录价格换算

### 单位、服务档位与上下文阶梯

- 目录以美元每百万 token 提供文本和图像 token 价，计费前换算为美元每 token；按张报价独立存在。
- v2 的 `context.gt` 与 `context.gte` 统一转换为内部区间边界，覆盖项按上下文边界合并。各阶梯保留绝对输入、输出和缓存价，按 `(min, max]` 展示与结算；价格配置手动区间优先，关闭内置长上下文规则时不应用目录阶梯。
- xAI 在达到上下文阈值时即切档，转换层将其内部分界点减 1（例如阈值 200,000 对应基础档上限 199,999），供结算、市场与默认价格页统一使用；原始目录阈值不变，其他供应商仍须严格超过阈值才切档。
- Fast、Flex 和 Ultrafast 使用目录档位金额或倍率（价格配置显式倍率优先），Fast 保留明确零价。独立日期倍率与所选档位叠加，手填金额的桶沿用自定义报价，其余桶按当天目录规则计价。OpenAI 日期规则按美国太平洋时间取值，其余按 UTC。v2 数值倍率作用于全部 token 桶，对象倍率仅作用于指定桶；无法从计价输入确定的区域和组合条件沿用基础报价与手动规则。
- GPT 的 `cost.text.input.cache.write` 保留 30m 与 12h 绝对价，Claude 5m 与 1h 沿用对应字段。Azure 旧 Cognitive Services 与 Vertex Claude 限定名按 v2 同服务记录解析，保持独立定价身份。

### 媒体单价与特例规则

- 价格配置的图片输入、输出单价覆盖每个目录阶梯（仅修改请求副本），手动零值与未填在各档含义一致：图片输入未填或为零按所在档文本输入价计费，图片输出未填或为零则不收图片 token 费。
- 默认目录的图片输出价区分显式零价与缺失：目录或补充明确给 0 时按免费结算，仅该桶缺失时回退文本输出价。
- 原厂 Gemini 生图条目（不适用于 Deep Research 和中继）的文本、图像输入与图像输出独立报价，补充文件填补缺失维度且目录现价优先，管理员可通过价格配置覆盖用户单价（见 [Google 官方定价](https://ai.google.dev/gemini-api/docs/pricing)）。
- 文本输出模型缺少完整文本输入输出价时，token 报价保持未定价，独立媒体价仍可查询，明确零价有效。
- OpenAI `gpt-image-1-mini` 若同时声明文本输出但只有图像输出价，保留图像输出并使用目录图像 token 费率（见 [OpenAI 模型说明](https://developers.openai.com/api/docs/models/gpt-image-1-mini)）。

### 补充文件、默认操作价与发布校验

数据合并优先级：`目录已有值（含明确零价） > 自定义补充（model_catalog.supplement_file，默认空） > 内嵌官方补充（backend/internal/modelcatalog/model_supplements.json，go:embed）`。`source` 和 `price_sources` 区分 models.dev、本地补充和规则补充。

- 补充只填缺失字段，媒体尺寸表等复合字段整体选一个来源，不因同名覆盖目录现价。补充按「提供方 + 原始模型 ID」同步到同一原厂记录的裸名和限定名（先匹配精确键，再填补等价名称空缺；日期版本和中继不共享）。
- 官方补充按完整型号保存已核实价格和属性并记录 `source_url`、`verified_at`：价格在条目顶层，展示属性在 `attributes` 对象中（按字段合并，`false` 和空数组有效；旧顶层模态字段保留给价格兼容）。自定义文件删除后恢复内嵌值，文件损坏则保留上一有效目录。
- 型号专属倍率不写死在 Go 代码中，统一通过 `fast_multiplier`、`flex_multiplier`、`max_reasoning_effort_multiplier`、`cache_write_multiplier`、`cache_write_1h_multiplier` 和 `time_pricing` 声明；Fast 缓存写入价遵循目录或价卡的服务档位规则（5m、1h、30m、12h）。
- 媒体默认价：`image_prices` 按 1K/2K/4K 存美元/张，旧 `output_cost_per_image` 为不分尺寸单张价；`video_prices` 按分辨率标签（含 `768p`、`2k` 等自定义档位）存美元/秒，缺失维度保持缺价而不套用尺寸倍率。
- 操作默认价存在保留节点 `_billing_defaults`，自定义按字段叠加并保留明确零价，不进入模型列表，随目录一同校验发布；历史账单与任务快照不重算。
- 发布校验：价格字段类型错误、嵌套阶梯解码失败、非法字段或非对象条目会拒绝本轮更新，返回模型和字段详情，保留原价格、属性、版本、更新时间和磁盘缓存，仅更新错误状态。只有属性没有报价的模型合法；未知模型上无价格的补充不生成计价条目；孤立或单侧缓存阶梯输出诊断，已发布模型的倍率或绝对阶梯消失时输出告警。

## 价格展示

### 报价解析与上下文区间

模型广场与实际结算共用同一计费解析器：先取价格配置有效价，缺失时取统一目录价，再应用分组倍率。分组映射已指定 pricing model 时不再猜测别名链；仅配服务层级、Max 或分时倍率的条目继承基础价并按字段覆盖倍率。积分人民币单价与美元汇率均有效时才展示相对官方价比例与人民币等价。

- 手动 `context_intervals` 按 `(min, max]` 展示与结算，优先于内置长上下文规则且不受关闭 `long_context_pricing_enabled` 影响；无手动区间时，开启开关按目录元数据合成基础与长上下文两档，关闭则单一基础价。
- 手动填写任一 `long_context_*`（含 `0`）会清除该条目 models.dev 默认绝对阶梯（阈值 `0` 关闭默认加价），置 `null` 恢复默认。
- 合成长上下文档时，普通档与 priority/Fast 档的缓存创建（统一及各 TTL 写入价）与读取价均按输入侧倍率缩放。展示补齐手动区间前后及中间有基础价的范围，缺价范围不显示零价也不借用相邻区间；仅所有正上下文范围有价且普通与 Fast 一致时才压平为单一价格。
- 零价区间保留范围标签与切换入口，JSON 省略零字段不代表缺价；区间价已含分组倍率。计费模型、价卡、服务层级、最终 effort、用量、时刻和倍率相同时，提供商平台不构成额外售价维度。以上游模型计费时先按提供商映射确定型号，多路线无法给出一致报价时标记歧义，不猜低价。

### Fast、分时、Max 与缓存分档展示

- 分组关联的价格配置开启 `free_openai_fast` 时，模型广场的 Fast 用户价与 Standard 基础价和上下文档一致（同时检查默认价和有效区间，含仅配区间的自定义模型）；仅调整展示副本，不影响结算 Fast `total_cost` 与提供商统计。
- token 基础单价对应分时 1x 时段与非 `max` 档位。校验通过的价格配置分时规则（分组条目覆盖优先）随报价下发 `time_pricing`（时区、`weekdays_only`、各时段倍率），正数且非 1 的 `max_reasoning_effort_multiplier` 单独下发。
- 模型广场将分时与 Max 和 Fast 并列切换，选中后视图内全部 token 单价（含 Fast）乘对应倍率；未覆盖全天或仅工作日生效时增加 1x「其他时段」，默认选中规则时区此刻时段并注明时区与生效日。目录分时规则按报价生成时刻直接乘入单价。
- 共享价格配置与提供商统计 API 支持可选 `cache_write_1h_price`（`cache_write_price` 代表 5m）。存在时市场与结算分 5m/1h 计价且公开 DTO 下发两项单价，缺失时按单一价格处理。

### 图片、视频与缺价展示

- 用户售价由价格配置覆盖目录（手动零价优先），无全局默认价卡且不读取 `pricing.override_file`。图片报价、普通图片结算、创作台与批量图片预占均逐尺寸查价（价卡优先，未配尺寸查目录）。
- 按次模式只用自身标签、上下文区间和默认价，不借目录图片价；缺配置且无明确默认价时报价省略该尺寸、结算返回缺价。含上下文区间的按次价卡在实时请求按总输入匹配，无上下文的报价与固定单张预占仅在命中明确标签或全区间与默认价一致时返回固定价。
- 公开图片报价独立检查完整型号的按张价，token 缺价不隐藏有效图片价。`image_price_1k/2k/4k` 对有价尺寸返回数值（含 0），缺价尺寸省略。缺价时不套用通用按张价或把图片价当视频每秒价；固定按张预留不接受 token 单价，但上游生图返回 token 用量且同型号有 token 报价时结算可按 token 计费。
- 视频报价使用 `pricing_mode=video`，`video_prices` 返回各分辨率每秒售价，`video_price_per_second` 返回显式默认每秒价（含分组倍率，0 为免费，缺价省略）。价卡先匹配分辨率再取显式默认价（命中后缺失档位保持缺价），无有效价卡时查目录视频价。
- 视频分辨率标签去空白、忽略大小写，保留 SD/HD/FHD 别名，`2k` 与 `1440p` 独立；自定义标签最长 50 字符且不含控制字符，规范化重复项在保存和发布时拒绝，价格档位不代表上游生成能力。
- 未知或歧义价格使用 `unpriced` 或 unknown，不填 0；仅指针明确为 0 表示免费。Qoder 公开别名和路由键同样按完整身份查目录，见 [Qoder 原生上游](qoder_upstream.md)。

<a id="gateway_default_pricing"></a>
## 管理员默认价格查询

「模型管理」包含「价格管理」（「价格配置」与「网关默认价格」两页签，保留原 URL）和「属性管理」。以下接口均需管理员认证：

| 接口 | 用途与规则 |
| --- | --- |
| `GET /api/v1/admin/pricing/defaults` | 按模型名、平台、计费模式筛选分页；平台标签仅标明来源，不作为自定义价卡查价维度 |
| `.../defaults/model`、`.../defaults/models` | 按模型自动填价和统一模型名查询，无需上游平台参数 |
| `POST /api/v1/admin/pricing/defaults/update` | 工具栏蓝色「更新目录」按钮调用：有远程来源时重下目录并加载本地补充，无远程来源时重载本地目录与补充；请求中禁用防重，成功保留筛选重查，失败报错并保持列表 |

查询与展示语义：

- 「网关默认价格」只读，日常改价在共享「价格配置」完成（一配置多分组，未关联分组用目录默认价，提供商成本规则独立）。工具栏左侧为搜索框和筛选面板（含平台、计费模式），右侧刷新按钮仅重查列表。
- 离线手动更新与后台本地重载共用同一流程并内部持有更新锁。缓存文件不可读时优先用内存目录，只读缓存目录不影响本地补充生效；候选价格完整校验失败时保留已发布版本与错误状态。
- 列表合并目录属性与价格条目（含未定价型号），单次查询固定目录版本与时间，分页后计算当页基础价与分时倍率（分组、用户、订阅倍率在报价与结算阶段应用）。详情标明各类单位与缓存、档位、长上下文规则，区分明确零价与未定价；批量图片和创作台使用任务保存时快照。
- 输出模态不决定计费模式：仅目录有明确按张价或命中专用媒体规则时才展示按张/按秒价，历史通用媒体默认价不计入；聊天模型图片元数据不切为图片计费。仅 token 报价按 token 展示，全缺价显示未定价；Gemini Image 图片 token 费率与计费模式分开解释，Omni 与 Deep Research 用普通输出价；价格配置不改写目录来源。

<a id="model_attributes"></a>
## 模型属性配置

### 图片能力、引用与属性档案

- 图片能力读取映射后最终路线的 `output_modalities`：缺失为未知，空数组或不含 `image` 为不支持，混合 text/image 保留文本协议。图片接口、原生图片工具、创作台、批量图片和图片测试均要求明确 `image` 输出；目录、前缀或价格不能补出能力，管理员可通过属性档案补充，配置读取失败返回不可用错误。
- 属性预览先枚举提供商和分组允许的路线再应用草稿，因此未具备图片能力的配置也可预览新增声明；普通目录在属性解析后按提供商分别筛选协议。
- 后台价格条目和属性规则支持 `reference_model`，将具体或末尾通配模型映射到具体目标（单层引用：目标不得命中本档案任一映射来源规则，含通配符和不同路线条件）。目标使用同档案生效规则并继承目录缺省值，显示名和译文来自目标，映射与字段覆盖互斥；目标缺失可保存，使用时呈未定价或未知。
- 属性引用按实际路线的平台、认证、站点和协议上下文解析目标并保留平台与转换器限制；预览返回 `reference_model` 和出站 `target.model`，编辑器支持自定义/映射切换并参考目标目录默认值。
- 属性配置保存在 `model_attribute_configs`（名称、描述、启用状态、有序规则），分组关联表以 `group_id` 唯一约束保证一分组至多一份配置、一配置可关联多分组。规则与关联同事务保存，冲突回滚；与价格配置独立，停用、删除或解绑即恢复默认属性。

### 属性合并顺序与路由解析

`modelcatalog` 维护目录条目与通用属性操作，`provider/modelrules` 维护各平台硬编码型号、认证变体、协议和媒体限制；预览、导出、探测与网关执行共用同一规则。

`modelrules.View.Resolve` 按以下顺序合并单条路线属性（省略字段继承下层，显式 `false` 与空数组保留）：

1. 实际服务的目录数据及本地补充。
2. 提供商声明的字段。
3. 分组属性配置的显式覆盖（匹配最终上游模型，可限定平台、认证类型、服务模式、站点、入口协议和上游协议；精确优先于通配，同类按配置顺序取首条）。

解析与快照规则：

- `reasoning` 与 `reasoning_options` 分开保存（选项缺失表示未知，空集合表示无可用控制）；`conditions` 表达 `temperature`、`top_p`、`tool_call` 对 effort 或 thinking 的条件。
- 转换器继续校验编码能力，冲突时预览同时显示配置值与可执行值。`Resolution` 返回字段来源与执行规则供协议适配器使用；属性查询只读内存目录，不发网络请求。
- 服务身份由平台、认证类型、`provider_mode`、站点及已识别官方地址确定：Bedrock 按出站区域 ID 查 `amazon-bedrock`，Anthropic Vertex 按转换日期 ID 查 `google-vertex-anthropic`，Gemini Vertex 查 `google-vertex`。
- Kimi 区分国内/国际/PayG/Coding，GLM 区分 `zhipuai`/`zai` 及各自 Coding，自定义中继查完整型号与公共资料。供应商前缀、日期与档位后缀参与匹配，Google 资源路径查询时规范化而出站 ID 保持实际路由。
- HTTP 与媒体执行固定目录版本并将分组配置复制进请求快照，故障转移、协议回退与映射后复用同一快照；同会话内相同完整路线复用解析副本（缓存键含服务、站点、认证、协议、端点和 Ollama 参数上限）。WebSocket 每 turn 创建新快照。配置变更在下一次请求生效，目录更新失败沿用上版，运行时配置读取失败返回不可用错误。

### 市场汇总、客户端导出与管理接口

- 模型广场在目录转换与批量属性查询间保留完整路线（Compact 用专用映射查询最终型号）。服务身份和执行条件相同的路线共享解析结果；原始中继地址参与服务身份识别并保留在路线中，未识别中继与官方服务分开缓存，协议、认证、站点、端点和 Ollama 上限差异仍区分缓存。
- 市场通过 `ResolveMarketplaceGroups` 汇总总体并集并按客户端协议生成 `protocol_attributes`：模态、媒体选项和推理档位取去重并集，长度与媒体数量上限取最大已知值；布尔能力任一路线支持则为真，无支持且有未知则未知，全明确不支持为 `false`。
- `route_differences` 与去重 `variants` 标明路线差异（未知证据、互斥条件、不连续预算、interleaved 差异）；响应不暴露内部目标地址或认证信息。
- 客户端配置导出使用 `Common` 汇总共同能力与已知最小上限（API Key 别名读取一跳目标属性）；市场批量读取分组档案共用同一目录版本，属性读取失败时保留模型和价格、属性显示未知。
- 管理员接口位于 `/api/v1/admin/model-attributes`：`/configs` 管理配置，`/defaults` 查目录，`POST /defaults/update` 更新目录，`POST /preview` 按 `group_id`、具体模型、可选入口协议和草稿返回最终属性、条件与来源（Google 短型号与资源路径同规则匹配）。
- 规则编辑器默认继承且仅保存修改字段，显示名留空时省略名称及译文以恢复继承（草稿保留译文供重输）；每条规则以首个非通配模型查询目录默认属性作为参考。
- OpenCode 导出显示名、附件、推理、温度、工具调用、模态和长度；`limit.context` 与 `limit.output` 缺一则省略 `limit`，条件温度与工具调用按 `false` 导出。Grok 模型列表的可调推理档位同样读取分组解析结果。

### GPT Image 2.6 补充

内嵌补充登记 `gpt-image-2.6-sunburst` 和 `gpt-image-2.6-flare` 的文字/图片输入与图片输出，以及官方每百万 token 美元单价：文字输入 5、文字缓存输入 1.25、图片输入 8、图片缓存输入 2、图片输出 30。图片缓存单价存于 `cache_read_input_image_token_cost` 并在默认价格页单独展示，未报告用量不推算。

两型号遵循缺项补充与管理员覆盖规则，不为裸 `gpt-image-2.6` 创建隐式别名，也不从 token 费率推导固定按张售价（创作台仍需按张价卡）。媒体参数支持 `xhigh`、`max` 和 GIF，提供商关闭 GIF 后不参与该格式选路。

### 平台执行规则

- OpenAI：使用目录 effort 集合；GPT-5.2/5.4 采样参数在 effort 为 `none` 时可用，GPT-6 Chat 工具能力按型号与 effort 设条件。Codex OAuth 禁用采样与输出参数但保留输出容量，Spark 当前仅文本输入，GPT-5.6 Sol/Terra/Luna OAuth Compact 在最终型号确定后将 `max` 编码为 `xhigh`。
- Claude：thinking 预算、开关和档位取自服务条目，温度按 thinking 状态处理，分别表达 adaptive、手动 budget 与 Sonnet 5.5 的 `between_tools`。Bedrock 型号缓存 TTL、tool search 与 adaptive 编码在 `provider/modelrules/bedrock.go`，新增须同步更新清单与报文测试。
- Grok：Grok 4.7 读取 `xhigh`，Build 0.1 等不可调推理型号显示空集合；Composer reasoning 清理和 4.20 系列 logprobs 规则单独执行。
- GLM 与 DeepSeek：GLM-5.2/5.3 使用叠加管理员覆盖后的档位集合，兼容别名在最终集合内转换，Messages 编码在读取该能力后执行。DeepSeek 的 `temperature` 与 `top_p` 按 thinking 状态处理。
- Qoder：路由与能力来自账号模型快照。`provider/qodermodel` 将原始路由和别名索引到同一项，按提供商和快照版本隔离缓存；同一路由键在不同账号上可有不同名称与限制。动态档位不绑定固定厂商身份，全来源缺输入上限时出站报文默认用 200000；转换器仅声明文本与工具能力。
- Antigravity：Messages Claude 路线限输出 64000，其他 Messages 转换限 65000，Chat 转换限 64000，并与目录取更小值；Gemini 直通沿用自身能力。已登记 Opus 4.6/4.7/4.8 adaptive 请求用 24576 预算；指定 Gemini 私有路由的采样、stop 与 `toolConfig` 单独处理。
- Gemini：转换按最终模型控制集发送互斥的 `thinkingLevel` 或 `thinkingBudget`，客户端控制取自转换前请求，预算按目录与当前输出空间约束；已知禁止关闭 thinking 的型号不显示可关闭。
- 媒体与 Ollama Cloud：媒体尺寸、参考图数与细分参数维护在 `provider/modelrules/media.go`，创作台、批量图片和 Responses 图片工具（HTTP/WS 共用，构造末尾读工具属性）按最终路线检查。OAuth 图片接口不发 `input_fidelity`；Ollama Cloud 输出 clamp 用解析上限，非正数关闭。

规则依据：[OpenAI 模型指南](https://developers.openai.com/api/docs/guides/latest-model?model=gpt-5.4)、[部署检查](https://developers.openai.com/api/docs/guides/deployment-checklist)、[Claude 协议](https://platform.claude.com/docs/en/claude_api_primer)、[Grok 推理](https://docs.x.ai/developers/model-capabilities/text/reasoning)、[GLM-5.3](https://docs.z.ai/guides/llm/glm-5.3)、[DeepSeek thinking](https://api-docs.deepseek.com/guides/thinking_mode/)、[Gemini GenerateContent thinking](https://ai.google.dev/gemini-api/docs/generate-content/thinking)、[Kimi Coding 型号](https://www.kimi.com/code/docs/en/kimi-code/models)；Qoder 以账号实时目录与协议为准，见 [Qoder 原生上游](qoder_upstream.md)。

<a id="group_availability_probe"></a>
## 容量与可用性

### 公开容量与历史可用性

- `GET /api/v1/marketplace/models` 默认附加分组 `capacity` 与历史可用性摘要，接受布尔参数 `include_capacity`（默认 `true`）。模型广场传 `false` 跳过容量聚合并省略 `capacity` 字段；非法布尔值返回 HTTP 400（reason：`INVALID_INCLUDE_CAPACITY`）。
- 容量取自当前提供商与并发数据，历史可用性仅查询开启 probe 且有可请求模型的可见分组；管理员分组管理继续展示容量。容量或可用性读取失败不阻塞模型与价格返回，缺失观测按未知处理。
- 可用性窗口和 bucket 粒度来自运行设置，受最大天数、分钟数和 bucket 数限制，按配置时区聚合，不参与实时调度与计费。
- 最新探测状态按分组索引取 `started_at DESC, id DESC` 首行，检查时间取该行 `finished_at`（可早于展示窗口，无记录时返回未知）。

### 探测配置、执行与永久诊断

- `availability_probe_config` 配置探测模型、提示词、间隔、单次超时、User-Agent 和 `max_retries`（首次失败后追加重试次数，缺省默认 3，0 表示只测一次，允许范围 0 到 10）。管理端非法配置统一返回 HTTP `400`（reason：`INVALID_AVAILABILITY_PROBE_CONFIG`）。
- 每次尝试独立计时，任一次成功即记本轮成功并停止重试；每轮仅存一条最终结果，中间失败不单独计入可用率样本。
- 探测模型填写分组允许的请求模型名：选择器执行分组白名单校验并返回分组 ID、提供商 ID 与分组映射模型，测试服务再执行提供商映射并按该分组读取最终属性。两阶段映射各执行一次，历史保存配置中的请求模型名。
- 探测编排归 `routing`，租约与历史查询归 `routing/postgres`。分钟级 cron 轮次不重叠，单轮领取到期分组数不超过 worker 数以便立即执行；PostgreSQL 租约防止重复领取并在超时后自动恢复。维护、领取与单组探测各有独立超时预算，避免慢维护挤占重试时间。启停操作幂等，停止时取消各预算并在关闭总预算内等待在途任务，超时返回失败且未保存领取按租约过期恢复。
- 永久性模型可用性诊断由 `routing.ModelAvailability` 执行，通过独立持久候选查询区分「模型不存在」与「暂时容量不足」。所有入口按明确组内关联查候选（Antigravity 不扩大成员范围）；查询失败或无法诊断时转交 503 分支，避免将临时限流误报为 404。

## 一致性要求

- 市场预取可调度提供商后，按提供商全局优先级和提供商 ID 恢复稳定候选顺序（`ProviderGroup` 只记成员关系，无组内优先级）。仅预取失败时逐组回退，不得把不同时间点的两批数据当作原子快照。
- 提供商、分组、价格配置、模型映射或价格变更时，必须让对应的解析缓存和市场查询缓存失效。
- 上下文区间边界及输入、输出、缓存单价与实际扣费共用同一解析结果；提供商平台不构成额外用户价格键，上游计费模型按明确映射来源确定。
- 市场「可见」仅代表当前存在产品、提供商和模型配置，不保证下次上游调用成功；实时请求仍须通过限流、并发、凭据和策略筛选。
- 新增平台须同步补齐默认展示信息、可请求解析、未知价格处理和平台文档，仅在下拉列表加常量不满足要求。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[路由与结算](../domains/routing_and_billing.md)。
