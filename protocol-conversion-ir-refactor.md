# 协议转换重构实施计划：中间表示、编解码器与分层测试

状态：待实施。本文件保存在仓库根目录的 `protocol-conversion-ir-refactor.md`，执行进度追加到文末的"进度记录"。

## 0. 使用方式

本计划交给实施模型执行。开发和测试分九个阶段（0 到 8），在 `main` 上分步提交。新引擎一次切换上线，旧引擎在同一个版本里删除（2.4 节）。阶段 0 到 7 的提交不改变生产行为；每个阶段末尾的验收全部通过后，才进入下一阶段。

- 标注"已定"的条目是已经做出的架构决策，直接照做。实施中发现某条决策与代码事实冲突时，停下来把冲突写进进度记录并询问用户，不要自行改方案。
- 计划没有覆盖到的局部选择，按 2.3 节的原则决定，并在进度记录里写一行说明。
- 开始前按 `AGENTS.md` 加载 `project-doc`、`code-complete`、`a-philosophy-of-software-design`、`code-comments`、`humanizer`。新增或改名后端 Go 文件前读 `docs/operations/development_workflow.md#backend_file_layout`。
- 代码片段里的类型和函数签名是契约，字段可以按需补充，已有字段的语义不要改。片段省略了仓库要求的文件总览和声明注释，实施时按 `code-comments` 补齐。
- 涉及 Codex、Claude Code、pi 等客户端的命令行参数来自 2026-11-14 的调研。这些工具更新很快，使用前用对应版本的 `--help` 和官方文档核对。

## 1. 调研结论

### 1.1 现状数据

| 项目 | 数值 | 说明 |
| --- | --- | --- |
| `protocol/bridge` 非测试代码 | 17,391 行，35 个源文件 | 纯叶子包，只依赖 `protocol` 下的兄弟包 |
| `protocol/bridge` 测试 | 11,257 行，约 360 个测试 | 全部是内联 JSON 断言，没有 testdata、golden、fuzz |
| bridge 之外导入 bridge 的非测试文件 | 68 个，合计 29,598 行 | 分布在 `gateway/httpapi`、`gateway/forward`、`gateway/provider/*forward`、`upstream/{openai,gemini,antigravity,grok,qoder}`；另有 22 个测试文件（23,235 行）导入 bridge |
| bridge 之外自带的转换实现 | Qoder 约 5,300 行，Antigravity 约 2,400 行，Grok `request_patch.go` 1,405 行 | 与 HTTP 读写、用量、重试混在一起 |
| 转换读取循环 | 约 12 个 | 每个循环自己扫描 SSE、解析用量、驱动状态机、写出 |
| SSE 写出辅助函数 | 至少 4 份 | `qoder.WriteSSEData`、`gemini.WriteSSE`、`searchtools.flushSSEJSON`、bridge 的 `*ToSSE` |
| 工具名映射机制 | 至少 6 套 | `ResponsesClientToolMapping`、Codex 反向表、Anthropic OAuth 改写、Qoder 映射器、Gemini 调用记忆、`CodexToolCorrector` |
| 录制的真实流量夹具 | 0 | |
| 使用真实客户端的测试 | 0 | 只有 `tools/gemini_sdk` 用官方 Google SDK 做可选检查 |

### 1.2 结构性问题

1. 成对转换加临时串联。请求方向 Chat 到 Messages 走 Chat→Responses→Messages（`gateway/forward/conversion.go:171-179`、`openaiforward/anthropic_flows.go:297-306`）；流方向 Messages 到 Chat 走 Messages→Responses→Chat；Gemini 上游的响应到 Chat 客户端走 Gemini→Messages→Responses→Chat（`upstream/gemini/response_openai.go:292-307`），共三跳。每一跳都按目标协议的子集类型重新序列化，丢失逐跳叠加。Responses 和 Messages 实际上被当成了临时的中间表示，但它们是线协议，表达不了对方的概念。
2. 同一对转换有多份实现。Anthropic 上游到 Responses、Chat 的输出读取在 `gateway/forward/conversion_output.go` 和 `openaiforward/anthropic_output.go` 各有一份；"Anthropic 事件→Responses→Chat"的驱动循环有四份；Claude 到 Gemini 的请求转换有 `bridge.NativeConvertClaudeMessagesToGeminiGenerateContent` 和 `antigravity.TransformClaudeToGeminiWithOptions` 两份；Gemini 到 Claude 的响应有 `NativeGemini*` 和 `GeminiToAnthropic*Processor` 两族；Messages 的类型有 `AnthropicRequest` 和 `ClaudeRequest` 两族。
3. 平台执行器按来源协议各写一套方法（`Forward`、`ForwardAsResponses`、`ForwardAsChatCompletions`），转换流程通过 Ports 接口把原生管线的步骤又实现了一遍。`1122d1b06` 瘦身后，`openAIMessagesExecutionAdapter` 仍有 33 个端口方法。
4. 三种 JSON 处理方式并存：带 `json.RawMessage` 空洞的子集类型、`map[string]any`、gjson 和 sjson 原地修补。类型化路径丢未知字段，map 路径经过 float64 损失大整数，代码里靠十几处注释提醒各自的保真边界。
5. 路线表（`routing/capability/protocol.go:212`）只给出目标协议，实际跑哪个转换器在至少四处重新判断：`unified_text_executor.go:222,258`、`openaiforward/prelude.go:309-380`、`openai_messages_execution_ports.go:140-146`、`openaiforward/chat.go:90-158`。
6. Qoder 自己定义了一套中间结构（`QoderPayloadRequest`、`QoderMessage`），三个入口协议各写一个解析器、一个流写出器和一个缓冲构造器。这说明"先归一再输出"的需求早已存在，只是每个上游各做各的。

### 1.3 逃过现有测试的缺陷

2026-08-01 以来有 48 个不重复的转换修复提交。按根因归类：

| 类别 | 例子 | 现有测试为什么抓不到 |
| --- | --- | --- |
| 多轮重放：网关第 N 轮输出的内容在第 N+1 轮作为历史回来，有时换了协议或提供商 | `b567a28f9`（DeepSeek 多轮工具调用缺 `reasoning_content` 返回 400）、`401dd43b4`、`612436a5a`、`057437f5e`（签名泄漏到 Messages 后跨提供商重放失败） | 单元测试只测单次转换，没有"输出再作为输入"的闭环 |
| 严格或合并式的客户端解码器 | `6b6987483`（缺 `created_at`，Rust serde 反序列化失败）、`091ec1f41`（每个参数增量带 `"name":""`，客户端调用了名为空串的工具）、`ac38d88f0`（`content_index` 不前进，SDK 覆盖内容） | 断言只看字段值，没有按目标协议的语法校验整条流，更没有真实客户端解析 |
| 上游的非标准线格式 | `22235a63c`（只有 data 行、多行 data、结尾没有空行的 SSE）、`4296d1e3a`、`826f45a2c`（后续增量带空 id 和 name）、Gemini 兼容上游重发累计批次 | 夹具是手写的理想报文，没有真实上游的录制 |
| 流的交错和截断 | `7b15de584`（并行工具调用交错，在 `content_block_stop` 之后又出增量）、`bd39eb589`（没有终止块就断流，仍返回 200 和零用量） | 手写事件序列覆盖不到交错和任意位置截断 |
| 跨跳的用量合并 | `4effd6b50`、`85b0079f6`（5m 和 1h 缓存桶丢失或重复计数） | 串联路径上每跳各自换算 |

每次修复都只在转换包里加了内联 JSON 的单元测试。问题的发现渠道是真实客户端（Codex 0.147.0 恢复会话、Claude Code 会话）、GitHub issue 和线上遥测。

### 1.4 Bifrost 和同类项目的启示

调研对象是 `maximhq/bifrost` 主分支（core 1.15.2）、LiteLLM、pi-ai 和 Vercel AI SDK 的源码。

| 观察 | 对本计划的取舍 |
| --- | --- |
| Bifrost 有两套并行的中间表示（Chat 形和 Responses 形），之间靠 3,076 行的 `mux.go` 互转，互转会丢 `CacheControl`、工具调用的 `ExtraContent`、签名、item id 等 | 只设一套中间表示 |
| Bifrost 的 Responses 形中间表示有 30 多种 item 类型，单个提供商的 Responses 适配是 Chat 适配的 3 到 10 倍（Anthropic 的 `responses.go` 有 11,621 行） | 中间表示不照搬任何一家的线协议形状，只建模语义核心，其余走扩展 |
| `ExtraParams` 是无类型 map。入口有时不采集，出口有时不消费，相关 issue 包括 #879、#5274、#8149 | 未知字段用 `encoding/json/v2` 的 embed 回退自动采集；扩展字段只允许同源协议的编码器整体写回，禁止按键名读取；用普查测试让未建模字段可见 |
| 条目列表拍平后丢了块顺序（#9156、#9736） | 中间表示保留每条消息内 part 的顺序，重排只允许发生在目标协议的合法化步骤里，并且要上报 |
| 每次转换随机生成 id，破坏了提示缓存的前缀稳定（#9518） | 请求方向生成的 id 必须由内容确定，不允许随机 |
| 签名靠拼在工具调用 id 或签名字符串里带过去（`_ts_`、`brid_` 前缀） | 设计显式的重放令牌和信封格式（3.10 节） |
| pi-ai 用一套很小的消息加内容块结构，重放数据是带类型的不透明字符串，跨模型时直接丢弃思考块 | 采用"消息加有序 part"的形状；重放令牌带签发方案标记，方案不匹配时丢弃并上报 |
| Bifrost 没有录制回放；测试靠真实提供商的场景矩阵和官方 SDK 的黑盒矩阵。LiteLLM 有用真实 Claude Code、Codex 跑端到端的 PR，但依赖真实提供商 | 场景矩阵照做，再补两样他们没有的：录制语料回放，以及用脚本化假上游驱动真实客户端 |

### 1.5 关于"是否拆成单独的包"

转换算法名义上已经在一个纯包里（`protocol/bridge`），拆包这一步其实做过了。可测性差的原因是包太浅：它只导出逐字段映射和状态机，解码、串联、工具降级、流驱动、SSE 分帧、用量合并这些步骤的编排散落在网关和上游的 68 个文件里，测试转换必须连同 HTTP 夹具一起搭。

已定：保留"转换在 `protocol` 下的纯包里"这个边界，把包做深。新的转换引擎对外只有"字节进、字节出"的窄接口，内部包含解码、IR 变换、编码和流状态。网关只负责 I/O、重试、健康、计费。这样转换可以在内存里用纯函数测完，网关侧的测试只需要覆盖粘合逻辑。

## 2. 目标、范围和原则

### 2.1 目标

1. 一套规范的中间表示（下称 IR）。每个协议实现一个编解码器，四个文本协议加 Qoder 共五个，替代现在的成对转换和串联。
2. 转换是深模块：`convert` 包接收客户端请求字节，返回上游请求字节；接收上游响应帧，返回客户端响应帧。
3. 网关里按上游协议合并流程：每个上游协议一条"原生"路径和一条"转换"路径，转换路径与客户端协议无关。
4. 所有信息丢失都由编码器显式上报，测试验证上报是完整的。
5. 六层测试体系，包含录制语料回放和真实客户端。
6. 一次发布完成切换：所有现有转换路线（包括 Qoder 和 Antigravity）通过验收后统一切到新引擎，旧引擎随同一版本删除。

### 2.2 范围

范围内：四个文本协议（`anthropic_messages`、`openai_responses`、`openai_chat_completions`、`gemini_generate_content`）之间的跨协议转换，Qoder 和 Antigravity 上游，Responses 同协议客户端工具降级（OpenAI API Key、DeepSeek、Grok 及 Responses WebSocket HTTP 桥在不支持 custom、namespace、tool_search 工具时通过 `convert` 的同协议轻量入口改写工具声明和调用条目，其余请求字段与原生流事件保持透传，见 3.8 和 6.6 节），以及原生 Messages、Responses 路径对推理信封的还原和清理（3.10 节）。

范围外：

- 来源协议和目标协议相同且不需要客户端工具降级的请求，继续走现有的原样透传和线级修补，不经过 `convert`。
- 图片、视频、音频、Embeddings、Realtime、Live、Compact、搜索入口。Images 转 Responses、Responses WebSocket 转 HTTP 属于传输和端点适配，保持现状。
- Bedrock、Vertex 对 Messages 请求的信封改写，Claude Code 伪装、Codex 请求变换、模型属性参数修正（`ApplyModelAttributeParameters`）等上游侧的线级后处理。它们在转换完成后作用于目标协议的报文，位置和实现都不变。
- 计费用量的解析。读取循环里现有的用量累加器保持不变，见 3.13 节。
- 新增客户端路线。Gemini 客户端转到非 Gemini 上游在 IR 落地后只差路线表的条目，本计划不做，留作后续。
- 原生路径和转换路径的进一步合并（让转换成为原生执行器外面的一层中间件）。这是合理的后续方向，记录在 9.5 节，本计划不承诺。

### 2.3 原则

- 解码器不丢信息：线上出现的每个成员要么进入建模字段，要么进入类型化的不透明条目，要么进入 `Extra`。
- 只有编码器可以丢信息，每次丢弃、近似或重排都产生一条 `Loss`。
- 适合统一表达的概念建模进 IR；只属于一家的内容用带来源标记的扩展保留；任何编码器需要按名字读取的扩展字段，必须先提升为建模字段。
- 目标协议的合法性规则（角色交替、工具调用配对、块的顺序限制）属于该协议编码器的内部知识，不外泄到 IR 和其他编解码器。
- 平台差异通过 `Dialect` 的能力字段表达，不在编解码器里写平台名判断。`Dialect` 只在 `gateway/provider` 的一个函数里构造。
- 请求方向的输出逐字节稳定：同一个版本里，同样的输入得到同样的字节，不依赖 map 遍历顺序、时钟和随机数。提示缓存命中依赖这一点。跨版本的字节一致不作要求（2.5 节）。
- 发布前以旧实现为行为基准。新旧差异只允许落在 6.2.4 节的验收差异清单里，其余差异要么消除，要么询问用户。基准只比较同一份输入在新旧引擎上的结果，不要求新版本认得旧版本留下的转换状态（2.5 节）。

### 2.4 发布方式（已定）

新引擎只上线一次。所有现有转换路线通过阶段 7 的总验收后，阶段 8 在一个发布里把全部分派点切到新引擎，并删除旧引擎。不设运行时引擎开关，生产中不让两套引擎并存，切换后也不安排观察期。

仓库从 `main` 直接发布，间隔通常是一两天（v0.7.0 到 v0.7.2 用了三天），所以阶段 0 到 7 的每个提交都必须保持生产行为不变：

- 阶段 0 和阶段 1 的新包没有生产代码导入。
- 阶段 2 到 6 的网关接入代码放在接线开关后面。接线开关是执行器构造时接收的装配选项，生产装配不打开它，只有 `internal/app` 的测试辅助函数（`newProtocolLabRouter`）会打开。它不读配置、环境变量和请求头，不用于运行时切换或回退。阶段 2 加一个测试，断言生产装配下接线开关是关闭的。
- 阶段 8 删除接线开关和旧路径，新路径成为唯一实现。

开发期间旧引擎仍是线上实现。线上转换缺陷照常在旧引擎修复；同一个修复要把复现用的请求或上游流加进语料或场景，新引擎也必须通过。修复改变了旧引擎的行为时，重新生成基线和差分报告。

阶段 8 需要一个发布窗口：从切换提交推送开始，到旧引擎删除、文档更新完成并通过 `make verify` 为止，不打发布标签。开始阶段 8 前先和用户确认窗口。切换后要回退只能整体回滚到上一个版本。旧引擎不认识信封（3.10 节），回滚后客户端历史里的信封对旧版本只是一段无法校验的签名，结果取决于各路径现有的签名错误处理。本计划不为回滚做专门兼容。

### 2.5 版本兼容边界（已定）

原则：新版本不需要认得旧引擎留下的转换状态，旧会话在升级后可以丢失其中一部分；客户端协议的正常支持和新引擎下的多轮续聊必须保证。按这个原则逐项评估原计划里的兼容要求：

| 原要求 | 结论 | 新要求、理由和后果 |
| --- | --- | --- |
| 进程配置 `gateway.conversion_engine`，按上游族逐个切默认值，观察一个发布周期后再删旧引擎 | 取消 | 见 2.4 节 |
| 推理令牌在阶段 1 到 6 按旧路线逐条等价（`gAAAA` 嗅探、非 `gAAAA` 签名写回 `encrypted_content`、Gemini 签名不写给 Messages 客户端），阶段 7 加固后仍保留嗅探，兼容加固之前的历史 | 取消 | 首版直接采用 3.10 节的信封方案。客户端历史里不是信封的令牌一律标为 `unknown`：跨协议路线丢弃并上报，同协议路线原样写回。后果：旧引擎写进客户端历史的令牌（例如 Claude Code 历史中 `signature` 字段里的 Grok `encrypted_content`）在升级后不再回传上游，模型在缺少这段推理历史的状态下继续。缺少令牌时的处理与现在客户端没带回令牌的情形相同 |
| 降级后的工具名与旧实现逐字节相同，工具名差分必须为零 | 放宽 | 命名算法仍从旧实现移植，作为起点。与旧版逐字节相同不再是门槛，新要求见 3.11 节。降级名只出现在发往上游的请求里，客户端始终看到原名；每个请求都按完整历史重新降级，同一请求里的声明和历史调用总是一致；Responses WebSocket 的 `ToolMap` 只在一条连接内继承，升级重启后连接会重建。名字变化只影响升级后第一轮的上游提示缓存 |
| 上游提示缓存跨版本命中，即请求字节与旧版一致 | 取消 | 切换后第一轮请求的字节与旧版不同，提示缓存可能未命中一次。同一版本内的字节稳定照旧保证（2.3 节、定律 Q5） |
| Redis 里的 `ReasoningHistory`（键前缀 `reasoning_content:`，按 Responses 推理条目 id 存推理文本，保留 7 天），以及服务端存储要与现有 `Lookup`、`Store` 行为等价 | 取消，存储在阶段 8 删除 | 新引擎把没有令牌的明文推理装进 `plain` 信封，写进 Responses 客户端的 `encrypted_content`。客户端只回传 `encrypted_content` 时（`186447541` 修的那类请求）也能取回文本，不再需要服务端副本。旧键在 7 天内自然过期，不做清理。后果：升级前生成、只剩 `encrypted_content` 的推理条目，升级后发往 Chat 上游时取不到文本，效果等同于现在 Redis 未命中 |
| 进程内状态：`GeminiToolCalls`（一小时）、`AnthropicPromptCache`（Messages 转 Responses 的提示缓存键绑定）、Responses WebSocket 连接内的工具映射 | 不做跨版本兼容 | 这些状态在进程重启时本来就会清空，阶段 8 可以改键和格式。`GeminiToolCalls` 改作 `CallMemory` 的实现继续使用（3.10 节），因为三种客户端协议都没有逐个调用的不透明字段 |
| 与旧实现的行为差分 | 保留，作为发布门槛 | 基线和差分一直跑到阶段 7。有意接受的差异写进 6.2.4 节的清单，其余差异为零。差分测试、`legacy_baseline.json` 和 `diff_ledger.json` 随旧引擎在阶段 8 删除 |
| Qoder 可以推迟到删除旧引擎之后 | 取消 | 所有现有转换路线（包括 Qoder 和 Antigravity）通过阶段 7 后才能切换 |

下面这些要求不属于跨版本兼容，保持不变：

- 客户端协议的正常支持：请求解码保留对无效 UTF-8 字符串的宽容（`jsontext.AllowInvalidUTF8(true)`），同时保持拒绝解码重名键（与网关入口 `wirejson.ValidateRequestObject` 的安全门禁一致）；Chat 的旧式 `function_call` 和 `functions`；Responses 请求体里旧式的 `messages` 字段（`openai_responses_ingress_compat.go`）、`input` 简写，以及 Cursor 发往 `/v1/chat/completions` 的 `input` 形状请求体（`openaiforward/chat.go`）。
- 上游的非标准报文：旧事件名、不规范的 SSE、兼容上游重发累计内容、Codex 流中孤立 `error` 帧被后续 `response.completed` 恢复。
- 计费用量零差异，客户端可见用量解码经 `wirejson.ValidateUsageEnvelope` / `ValidateUsageObject` 守卫；输出帧的 `Semantic` 和 `Terminal` 由 `convert` 按结构化事件直接填写（补齐旧 `CompatOutputMeaning` 漏判的 custom/search 工具、refusal 及 `incomplete`/`failed`/`error` 终态，归入 D3），读取循环靠它决定前导帧暂存窗口、TTFT 和保活停发。
- 上游侧的线级后处理不变。
- 第 4 节里取自旧实现的默认值和合法化规则。它们记录的是上游接受什么，改动要有证据，并记入 6.2.4 节的清单。

新引擎下的多轮续聊由定律 M1、T5 的多轮场景和 T6 保证，覆盖范围以阶段 7 的验收为准。

## 3. 目标架构

### 3.1 总览

```text
客户端请求 (协议 P 的字节)
        │
        ▼
 convert.NewExchange ── P 的请求解码器 ──► ir.Request
        │                                    │ 变换：连续性恢复、客户端标记清理、工具降级
        │                                    ▼
        │                     Q 的请求编码器 (Dialect) ──► 上游请求字节 + []ir.Loss
        ▼
 现有的上游侧线级后处理、鉴权、发送（不变）
        │
        ▼
 上游响应 (协议 Q 的 SSE 帧或 JSON)
        │
        ▼
 Q 的流解码器 ──► ir.Event 序列（顺序化、每个 part 结束时带完整快照）
        │              │ 变换：工具还原、连续性记录
        ▼              ▼
 现有读取循环      P 的流编码器 ──► 客户端帧（带语义和终止标记）
 的原生观察              │
 （用量、模型、          ▼
  故障分类，不变）   upstream.OutputContext ──► 客户端
```

### 3.2 包布局（已定）

全部位于 `backend/internal/protocol/` 下，角色都是 `pure`，`testkit` 除外。

| 包 | 职责 | 允许依赖的项目包 |
| --- | --- | --- |
| `protocol/sse` | SSE 帧类型、增量读取器、写出函数 | 自身 |
| `protocol/ir` | IR 类型、流事件、校验器、累加器、顺序化器、Loss、重放令牌和信封、语义摘要 | 自身、`protocol` |
| `protocol/qoder` | Qoder 归一化请求、消息、工具与思考指令的纯数据结构（供 `codec/qoderchat` 与 `upstream/qoder` 共用） | 自身、`protocol` |
| `protocol/codec/messages` | Anthropic Messages 编解码器 | 自身、`protocol`、`protocol/ir`、`protocol/sse`、`protocol/anthropic`、`protocol/wirejson` |
| `protocol/codec/responses` | OpenAI Responses 编解码器 | 自身、`protocol`、`protocol/ir`、`protocol/sse`、`protocol/openai`、`protocol/wirejson` |
| `protocol/codec/chat` | OpenAI Chat Completions 编解码器 | 同上 |
| `protocol/codec/generatecontent` | Gemini GenerateContent 编解码器 | 自身、`protocol`、`protocol/ir`、`protocol/sse`、`protocol/gemini`、`protocol/wirejson` |
| `protocol/codec/qoderchat` | Qoder 上游编解码器，只有上游侧的一半 | 自身、`protocol`、`protocol/ir`、`protocol/sse`、`protocol/qoder`、`protocol/wirejson` |
| `protocol/convert` | 转换引擎：`Exchange`、`Stream`、IR 变换、同协议工具降级/还原、`Dialect`、编解码器注册表 | 自身加以上全部 |
| `protocol/testkit` | 测试支撑：语料加载、golden、语法校验器、严格请求校验器、客户端模拟器、语义摘要的 Loss 规则、场景脚本类型。角色 `fixture` | 自身、`protocol`、`protocol/ir`、`protocol/sse`，加 `os`、`embed` |
| `protocol/testkit/mockupstream` | 脚本化上游（`http.Handler`）。角色 `fixture` | 自身、`protocol/testkit`、`protocol/convert`，加 `net/http` |
| `protocol/testkit/cmd/corpus` | 语料导入和检查的命令行工具。角色 `fixture` | 自身、`protocol/testkit` |

编解码器包按协议 ID 的后半段命名，避开已有的 `anthropic`、`openai`、`gemini`、`qoder` 包名，不需要 import 别名。编解码器对兄弟线类型包（`protocol/anthropic` 等）和 `protocol/wirejson` 的依赖只用于复用现有的用量校验与归一化函数，不复用它们的请求响应结构体。`tools/architecture/catalog.go` 的 `leafDependencies` 中每个条目都必须在 `Production` 包含自身包路径，否则外部测试包（`package <pkg>_test`）无法导入被测包；`testkit` 及其两个子目录同样要登记 `leafDependencies`，否则会经 `nearest` 继承 `internal/protocol` 的空依赖集。

依赖方向：`sse` ← `ir` ← `codec/*` ← `convert` ← `testkit/mockupstream`。`testkit` 核心包只依赖 `ir` 和 `sse`，校验器和客户端模拟器都直接操作线上的 JSON 和 SSE 帧，不经过编解码器。这样 `codec/*` 和 `convert` 的包内测试可以导入 `testkit` 而不形成导入环。`ir` 自己的测试如果要用 `testkit`，使用配对真实源文件的 `essence_external_test.go` 或带 `package ir_test` 的 `<场景>_scenario_test.go`（`ir` 没有 `ir.go`，不能建 `ir_external_test.go`，也不能起名 `*_scenario_external_test.go`）。编解码器之间互不导入。新包不导入 `protocol/bridge`，旧包也不导入新包；两者只在测试的差分工具里相遇。

### 3.3 IR 类型（已定）

IR 是"消息加有序 part"的形状。它不等于任何一家的线协议：Responses 的平铺 item 在解码时按角色归并成消息，Messages 的 `tool_result` 块从 user 消息里拆出来成为 `tool` 角色消息。

IR 的 Go 类型带 JSON 标签，可以直接序列化成测试夹具和 golden。联合类型用"带 `Kind` 的结构体加可选指针"表达，由 `Validate` 保证恰好一个分支有值。

```go
package ir

type Request struct {
	Model              string           `json:"model"`
	System             []Part           `json:"system,omitzero"`   // out-of-band leading instructions
	Messages           []Message        `json:"messages"`
	Tools              []Tool           `json:"tools,omitzero"`
	Namespaces         []Namespace      `json:"namespaces,omitzero"` // Responses namespace declarations
	ToolChoice         *ToolChoice      `json:"tool_choice,omitzero"`
	ParallelToolCalls  *bool            `json:"parallel_tool_calls,omitzero"`
	Sampling           Sampling         `json:"sampling,omitzero"`
	MaxOutputTokens    *int64           `json:"max_output_tokens,omitzero"`
	Stop               []string         `json:"stop,omitzero"`
	Reasoning          *ReasoningConfig `json:"reasoning,omitzero"`
	Output             *OutputFormat    `json:"output,omitzero"`
	Stream             bool             `json:"stream,omitzero"`
	IncludeUsage       *bool            `json:"include_usage,omitzero"` // Chat stream_options.include_usage
	ServiceTier        string           `json:"service_tier,omitzero"`
	CacheKey           string           `json:"cache_key,omitzero"` // prompt_cache_key across protocols
	PreviousResponseID string           `json:"previous_response_id,omitzero"`
	SessionID          string           `json:"session_id,omitzero"`
	ConversationID     string           `json:"conversation_id,omitzero"`
	ConversationSet    bool             `json:"conversation_set,omitzero"` // true when Responses top-level conversation is non-null
	Store              *bool            `json:"store,omitzero"`
	UserID             string           `json:"user_id,omitzero"`
	MetadataUserID     string           `json:"metadata_user_id,omitzero"` // metadata.user_id when distinct from top-level user
	Extra              Extra            `json:"extra,omitzero"`
}

type Message struct {
	Role  Role   `json:"role"` // system | developer | user | assistant | tool
	ID    string `json:"id,omitzero"`
	Parts []Part `json:"parts"`
	Extra Extra  `json:"extra,omitzero"`
}

type Part struct {
	Kind        PartKind     `json:"kind"` // text | reasoning | tool_call | tool_result | media | refusal | opaque
	ID          string       `json:"id,omitzero"` // wire item id when the origin protocol assigns one
	Text        string       `json:"text,omitzero"` // text and refusal
	Annotations []Annotation `json:"annotations,omitzero"`
	Reasoning   *Reasoning   `json:"reasoning,omitzero"`
	Call        *ToolCall    `json:"call,omitzero"`
	Result      *ToolResult  `json:"result,omitzero"`
	Media       *Media       `json:"media,omitzero"`
	Opaque      *OpaqueItem  `json:"opaque,omitzero"`
	Cache       *CacheMark   `json:"cache,omitzero"` // Anthropic cache_control breakpoint: TTL "", "5m", "1h"
	Extra       Extra        `json:"extra,omitzero"`
}

type Reasoning struct {
	Segments []string     `json:"segments,omitzero"` // visible reasoning text, one entry per wire segment
	Summary  bool         `json:"summary,omitzero"`  // provider summary rather than raw reasoning
	Redacted bool         `json:"redacted,omitzero"` // Anthropic redacted_thinking
	Token    *ReplayToken `json:"token,omitzero"`
}

// ReplayToken is opaque continuity data that only its issuer scheme can accept.
type ReplayToken struct {
	Scheme     string `json:"scheme,omitzero"`      // anthropic | antigravity_claude | openai | xai | gemini | plain | unknown; assigned by convert
	ProviderID int64  `json:"provider_id,omitzero"` // optional issuing provider id for account-bound schemes (openai, xai)
	Data       string `json:"data"`                 // token text exactly as the wire field carries it
}

type ToolCall struct {
	CallID     string       `json:"call_id"`
	Tool       ToolRef      `json:"tool"`
	Arguments  string       `json:"arguments"` // JSON text for function tools, free text for custom tools
	Token      *ReplayToken `json:"token,omitzero"` // Gemini thoughtSignature attached to the call
	Unsigned   bool         `json:"unsigned,omitzero"` // CallMemory confirmed the upstream sent no token
	Incomplete bool         `json:"incomplete,omitzero"` // stream ended before the arguments completed
}

type ToolRef struct {
	Kind      ToolKind `json:"kind"` // function | custom | client_search | hosted
	Namespace string   `json:"namespace,omitzero"`
	Name      string   `json:"name"`
}

type ToolResult struct {
	CallID  string  `json:"call_id"`
	Tool    ToolRef `json:"tool,omitzero"` // filled by Link from the matching call
	Content []Part  `json:"content"`       // text and media parts only
	IsError bool    `json:"is_error,omitzero"`
}

type Media struct {
	Type   MediaType `json:"type"`   // image | audio | video | document
	MIME   string    `json:"mime,omitzero"`
	Source string    `json:"source"` // base64 | url | file_id
	Data   string    `json:"data"`   // base64 payload, URL, or file id according to Source
	Name   string    `json:"name,omitzero"`
	Detail string    `json:"detail,omitzero"`
}

// OpaqueItem is a wire item that only its origin protocol can express.
type OpaqueItem struct {
	Origin protocol.ProtocolID `json:"origin"`
	Type   string              `json:"type"`
	Raw    jsontext.Value      `json:"raw"`
}

type Tool struct {
	Kind         ToolKind       `json:"kind"`
	Namespace    string         `json:"namespace,omitzero"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitzero"`
	InputSchema  jsontext.Value `json:"input_schema,omitzero"` // raw JSON Schema, never re-parsed through float64
	Strict       *bool          `json:"strict,omitzero"`
	Format       jsontext.Value `json:"format,omitzero"`       // custom tool grammar, raw
	Hosted       *OpaqueItem    `json:"hosted,omitzero"`       // hosted tool declaration, raw
	DeferLoading *bool          `json:"defer_loading,omitzero"`
	Cache        *CacheMark     `json:"cache,omitzero"`
	Extra        Extra          `json:"extra,omitzero"`
}

type ToolChoice struct {
	Mode         ToolChoiceMode `json:"mode"` // auto | none | required | tool
	Tool         ToolRef        `json:"tool,omitzero"`
	AllowedTools []ToolRef      `json:"allowed_tools,omitzero"` // explicit subset for Qoder and Gemini allowedFunctionNames
}

type Sampling struct {
	Temperature      *float64 `json:"temperature,omitzero"`
	TopP             *float64 `json:"top_p,omitzero"`
	TopK             *int64   `json:"top_k,omitzero"`
	Seed             *int64   `json:"seed,omitzero"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitzero"`
	PresencePenalty  *float64 `json:"presence_penalty,omitzero"`
	CandidateCount   *int64   `json:"candidate_count,omitzero"`
}

type ReasoningConfig struct {
	Mode             string `json:"mode,omitzero"`    // "" | enabled | adaptive | disabled
	Effort           string `json:"effort,omitzero"`  // "" | none | minimal | low | medium | high | xhigh | max
	BudgetTokens     *int64 `json:"budget_tokens,omitzero"`
	Summary          string `json:"summary,omitzero"` // "" | auto | concise | detailed
	IncludeEncrypted bool   `json:"include_encrypted,omitzero"`
}

type OutputFormat struct {
	Kind      string         `json:"kind,omitzero"` // "" | text | json_object | json_schema
	Name      string         `json:"name,omitzero"`
	Schema    jsontext.Value `json:"schema,omitzero"`
	Strict    *bool          `json:"strict,omitzero"`
	Verbosity string         `json:"verbosity,omitzero"`
}

type Response struct {
	ID          string  `json:"id,omitzero"`
	Model       string  `json:"model,omitzero"`
	CreatedAt   int64   `json:"created_at,omitzero"`
	Parts       []Part  `json:"parts"`
	Finish      Finish  `json:"finish"`
	Usage       *Usage  `json:"usage,omitzero"`
	ServiceTier string  `json:"service_tier,omitzero"`
	Extra       Extra   `json:"extra,omitzero"`
}

type Finish struct {
	Reason       FinishReason `json:"reason"` // stop | length | tool_calls | content_filter | stop_sequence | pause | error | other
	StopSequence string       `json:"stop_sequence,omitzero"`
}

// Usage keeps disjoint input buckets. Protocol totals are derived by encoders.
type Usage struct {
	Input              int64  `json:"input"` // fresh input: excludes cache reads and cache writes
	CacheRead          int64  `json:"cache_read"`
	CacheWrite         int64  `json:"cache_write"` // aggregate of all TTL classes
	CacheWrite5m       *int64 `json:"cache_write_5m,omitzero"`
	CacheWrite30m      *int64 `json:"cache_write_30m,omitzero"`
	CacheWrite1h       *int64 `json:"cache_write_1h,omitzero"`
	CacheWrite12h      *int64 `json:"cache_write_12h,omitzero"`
	Output             int64  `json:"output"` // includes reasoning
	Reasoning          int64  `json:"reasoning,omitzero"`
	ImageInput         int64  `json:"image_input,omitzero"`
	ImageOutput        int64  `json:"image_output,omitzero"`
	AudioInput         int64  `json:"audio_input,omitzero"`
	AudioOutput        int64  `json:"audio_output,omitzero"`
	AcceptedPrediction int64  `json:"accepted_prediction,omitzero"`
	RejectedPrediction int64  `json:"rejected_prediction,omitzero"`
	Speed              string `json:"speed,omitzero"` // Anthropic usage.speed ("fast")
}
```

配套函数，全部在 `ir` 包：

- `Validate(*Request) error`、`ValidateResponse(*Response) error`：结构不变量，包括 Kind 与分支匹配、`tool` 消息只含 `tool_result`、`ToolResult.Content` 只含 text 和 media。
- `Link(*Request)`：按 `CallID` 把每个 `ToolResult.Tool` 填成对应调用的 `ToolRef`。Gemini 编码器需要函数名，Responses 编码器需要区分 custom 和 function。
- `Essence(*Request) EssenceDoc`、`ResponseEssence(*Response) EssenceDoc`：与协议无关的语义摘要，供测试比较（5.4 节）。

各协议与用量桶的对应关系：Messages 的 `input_tokens` 等于 `Input`；Chat 的 `prompt_tokens` 和 Responses 的 `input_tokens` 等于 `Input + CacheRead + CacheWrite`。这与 `docs/interfaces/protocol_capabilities.md#client_usage_cache` 的口径一致。

### 3.4 扩展机制（已定）

IR 有三种保留协议特有信息的手段，适用场景不同：

| 手段 | 放什么 | 谁可以消费 |
| --- | --- | --- |
| 建模字段加来源标记 | 各家都有、语义相同、只是取值互不通用的内容。目前是 `ReplayToken`（Anthropic 的 `signature`、Responses 的 `encrypted_content`、Gemini 的 `thoughtSignature`） | 所有编码器，按 `Scheme` 是否与目标匹配决定写出或丢弃 |
| `OpaqueItem` | 整个条目只有来源协议能表达：Responses 的 `compaction`、`local_shell_call`、`mcp_*`、`image_generation_call`，Messages 的 `server_tool_use`、`web_search_tool_result`，托管工具声明 | 来源协议自己的编码器原样写回；其他编码器丢弃并上报 `opaque_item_dropped` |
| `Extra` | 已建模对象上未建模的成员 | 只有 `Origin` 相同的编码器整体写回；其他编码器忽略并上报 `extra_dropped` |

```go
// Extra holds members the origin codec did not model.
type Extra struct {
	Origin  protocol.ProtocolID        `json:"origin"`
	Members map[string]jsontext.Value `json:"members"`
}

func (e Extra) IsZero() bool { return len(e.Members) == 0 }
```

采集方式：编解码器的私有线结构体在每个对象上放一个 `map[string]jsontext.Value` 字段，标签为 `json:",embed"`。已在本仓库的 Go 1.27.4 上验证：这个字段会接住所有未声明的成员，值保持原始字节（大整数、小数位、`<&>` 都不变），编码时在已声明字段之后写回，配合 `json.Deterministic(true)` 按键名排序。`Extra.IsZero` 保证没有未知成员时（即使填了 `Origin`）仍被 `omitzero` 省略。注意 `json/v2` 默认区分字段名大小写，会把大小写变体（例如 `"Model"`）收进 `Extra.Members`；网关入口的 `wirejson.ValidateRequestObject` 继续在前置阶段拦截这类大小写碰撞和非规范拼写。

防止滥用的规则，都由测试强制：

1. 编解码器和变换代码不得按键名读写 `Extra.Members`。`convert` 包里放一个源码扫描测试（通过 `testkit` 读取源文件），在 `protocol/codec` 和 `protocol/convert` 的非测试代码里发现 `.Members[` 就失败。需要读取的字段必须提升为建模字段。
2. 未建模字段普查：对语料库里每个客户端请求，列出落进 `Extra` 和 `OpaqueItem` 的 JSON 路径，写成 golden（`testdata/golden/census/<protocol>.txt`）。新版客户端发来新字段时，这个 golden 会变，评审时决定建模、保持不透明还是有意忽略。
3. 新增建模字段的门槛：至少两个协议有等价概念，或者某个编码器必须读取它才能生成合法请求。只有一家有、其他家只能丢弃的内容留在 `Extra` 或 `OpaqueItem`。

原始请求体不进 IR。网关已经在 `requeststate` 里持有原始字节，原样透传路径继续用它。

### 3.5 流事件模型（已定）

```go
type Event struct {
	Kind        EventKind     `json:"kind"` // response_start | part_start | part_delta | part_end | usage | response_end | error
	Response    *ResponseMeta `json:"response,omitzero"`     // response_start: ID, Model, CreatedAt, ServiceTier
	Index       int           `json:"index,omitzero"`        // part events
	Part        *Part         `json:"part,omitzero"`         // part_start: skeleton; part_end: complete snapshot
	Delta       *Delta        `json:"delta,omitzero"`        // part_delta
	Usage       *Usage        `json:"usage,omitzero"`        // usage: full merged snapshot, latest wins
	Finish      *Finish       `json:"finish,omitzero"`       // response_end
	ServiceTier string        `json:"service_tier,omitzero"` // response_end when reported on terminal frame
	Err         *StreamError  `json:"error,omitzero"`        // error: Type, Code, Message, Status
}

type Delta struct {
	Text      string `json:"text,omitzero"`      // text, refusal, or reasoning text according to the open part
	Arguments string `json:"arguments,omitzero"` // tool call argument fragment
}
```

规范事件流的不变量。解码器保证这些不变量，编码器可以依赖它们，`ir.ValidateStream` 检查它们：

1. 第一个事件是唯一的 `response_start`。
2. part 是顺序的：任一时刻最多一个 part 处于打开状态，`Index` 从 0 开始逐个递增。
3. `part_delta` 只出现在对应 part 打开期间，增量类型与 part 的 Kind 相符。
4. `part_end` 携带该 part 的完整最终快照：文本全文、工具调用的完整参数、推理的全部分段和重放令牌。解码器负责累积。
5. `usage` 事件可以出现在 `response_start` 之后的任何位置，每次都是解码器按协议规则合并稀疏帧后的完整快照（尾帧只报告输出 token 时不清空先前已观测的输入和缓存桶）。
6. 恰好一个终止事件（`response_end` 或 `error`），之后没有任何事件。`response_end` 之前所有 part 已关闭；上游在 `finishReason` 之后还会发独立用量尾帧的协议（如 Gemini），解码器先记下结束原因，等流结束调用 `Close(nil)` 时再发出最终 `usage` 和 `response_end`。
7. 上游异常结束时，解码器的 `Close(cause)` 先关闭打开的 part（工具调用标 `Incomplete`），再发 `error`。

第 2 条把"交错"这类问题集中到解码器。Chat 上游的并行工具调用会按 index 交错到达，解码器用 `ir.Sequencer` 缓冲非当前 part 的事件，当前 part 关闭后按序释放。文本流不受影响；交错的工具参数会晚一点到客户端，这与现在 `chatcompletions_anthropic_bridge.go` 在收尾时统一发出工具调用的行为等价。

第 4 条让非流式聚合变成"收集所有 `part_end` 快照"，也让需要完整内容的编码器（Gemini 客户端要完整的参数对象，custom 工具还原要完整的包装）不必自己累积。

`ir.Accumulator` 把事件流折叠成 `ir.Response`。非流式响应的编码统一走"事件流→Accumulator→`EncodeResponse`"，上游被强制流式而客户端要非流式时也是这条路。

### 3.6 编解码器契约（已定）

接口定义在 `convert` 包（使用方定义接口），各编解码器包导出具体类型，由 `convert/registry.go` 里的显式表登记，不用 `init` 注册。

```go
package convert

// clientCodec is the half a protocol needs to act as the client-facing side.
type clientCodec interface {
	DecodeRequest(body []byte) (*ir.Request, error)
	EncodeResponse(resp *ir.Response, view ClientView) ([]byte, []ir.Loss, error)
	NewStreamEncoder(view ClientView) StreamEncoder
}

// upstreamCodec is the half a protocol needs to act as the upstream-facing side.
type upstreamCodec interface {
	EncodeRequest(req *ir.Request, dialect Dialect) ([]byte, []ir.Loss, error)
	DecodeResponse(body []byte) (*ir.Response, error)
	NewStreamDecoder() StreamDecoder
}

type StreamDecoder interface {
	Decode(frame sse.Frame) ([]ir.Event, error)
	Close(cause error) []ir.Event
}

type StreamEncoder interface {
	Encode(event ir.Event) ([]sse.Frame, []ir.Loss, error)
}

// ClientView carries what a response encoder needs to know about the client.
type ClientView struct {
	Model   string      // model name to present to the client
	Request *ir.Request // decoded client request, for echo fields and include_usage
	Runtime Runtime
}

type Runtime struct {
	Now   func() time.Time
	NewID func(prefix string) string // response-side ids only
}
```

四个文本协议两半都实现。`qoderchat` 只实现 `upstreamCodec`。四个协议都实现客户端侧的一半，原因有两个：测试用目标协议的请求解码器把转换结果解回 IR 做比较；脚本化上游用目标协议的响应编码器生成上游响应。

约束：

- 编解码器是同步的纯计算，不起 goroutine，不读时钟和随机数（`Runtime` 除外），不做日志。
- 请求解码对字符串中的无效 UTF-8 宽容（`jsontext.AllowInvalidUTF8(true)`），与现在 `encoding/json` v1 的字符串容忍度一致；重复键保持 `json/v2` 默认拒绝（不设 `AllowDuplicateNames(true)`），与网关入口 `wirejson.ValidateRequestObject` 的重名键门禁一致，同时避免 Chat/Responses 字符串形式的 `arguments` 在转成 Messages/Gemini JSON 对象时夹带重名键。
- 请求编码用 `json.Deterministic(true)`。请求方向需要生成 id 时（例如目标协议要求而历史条目没有），用内容的 SHA-256 前缀派生，不用 `Runtime.NewID`。
- 编解码器不认识信封和方案。解码时把 `signature`、`redacted_thinking.data`、`encrypted_content`、`thoughtSignature` 的原文放进 `Token.Data`，`Scheme` 和 `ProviderID` 留空；编码时原样写出 `Token.Data`。方案判定、提供商作用域检查、信封的封装和解析、令牌取舍都在 `convert`（3.10 节）。脚本化上游因此可以直接用响应编码器合成真实上游会发的裸令牌。
- 响应编码器负责目标协议要求的全部必填字段和回显字段。Responses 的 `response` 对象要有 `id`、`object`、`created_at`、`status`、`model`、`output`，并从 `ClientView.Request` 回显 `tools`、`tool_choice`、`parallel_tool_calls`、`reasoning`、`text`、`store`、`instructions`、`max_output_tokens`、`previous_response_id`。`6b6987483` 就是缺 `created_at` 导致 Rust 客户端反序列化失败。
- 流编码器输出的每个 `sse.Frame` 带 `Semantic` 和 `Terminal` 标记，由 `convert` 直接根据结构化事件和流编码器状态填写（3.8 节），读取循环用它暂存前导帧并调用 `OutputContext.NextEvent`。

### 3.7 Dialect（已定）

`Dialect` 描述一个上游端点在基础协议之外的能力差异。它是纯数据，定义在 `convert` 包。

```go
type Dialect struct {
	Name                   string        // diagnostic label, for example "openai", "deepseek", "antigravity"
	ProviderID             int64         // selected provider id for account-bound replay schemes (openai, xai)
	Tools                  ToolSupport   // bit set: Function, Custom, Namespace, ClientSearch, HostedWebSearch
	ToolNameMaxBytes       int           // 0 means unlimited
	ReplayScheme           string        // token scheme this endpoint issues and accepts; "" means none
	ReasoningHistory       HistoryMode   // how plaintext reasoning returns in history: none | tool_turns | all
	SchemaDialect          SchemaDialect // json_schema | gemini | antigravity
	Reasoning              ReasoningCapabilities
	StripEffort            bool   // clear request effort after recording Info().Effort when effort_model_mapping_strip_effort matched
	DropTemperature        bool   // model attributes forbid temperature
	DropTopP               bool   // model attributes forbid top_p
	DropTopK               bool   // model attributes forbid top_k
	OmitStopSequences      bool   // target forbids stop sequences
	OmitStoreAndInclude    bool   // Responses target (for example Grok) omits cross-protocol store/include defaults
	AllowBillingHeader     bool   // retain x-anthropic-billing-header in system text (Qoder)
	PlaceholderToken       string // Gemini: thoughtSignature placeholder when a call has none
}

// ReasoningCapabilities is the model-side input for effort and budget resolution.
type ReasoningCapabilities struct {
	ControlsKnown  bool
	Supported      *bool
	Toggle         bool
	Efforts        []string
	Budget         bool
	BudgetMin      int
	BudgetMax      int
	ForceBudget    bool
	AdaptiveBudget int
	OutputLimit    *int
	BudgetPadding  int
}
```

`ReasoningCapabilities` 的字段直接对应现有的 `bridge.GeminiThinkingOptions`，供 Gemini 和 Messages 编码器把 `ir.ReasoningConfig` 解析成各自的预算或档位。解析算法从 `bridge/gemini_thinking.go` 的 `ResolveGeminiThinking` 移植，输入从"从三种请求体里解析"改为 `ir.ReasoningConfig`。注意 `ControlsKnown == false`（模型未登记推理选项）时 `Efforts` 为空，编码器仍须保留客户端传入的 `"max"` 档位（对齐旧 `ModelConversionOptions` 的 `SupportsMaxEffort == true`），同时对 `"ultra"` 档位返回 400 校验错误。

构造位置（已定）：`gateway/provider/model_attributes.go` 新增 `ConversionDialect(ctx context.Context, target *ExecutionProvider, result modelrules.Resolution, upstream protocol.ProtocolID) convert.Dialect`，取代 `ModelConversionOptions` 和 `GeminiThinking`。这是唯一允许根据平台、提供商类型和 `modelrules.Resolution` 取值的地方，取值来源：

| 字段 | 取值依据 |
| --- | --- |
| `ProviderID` | `target.Record.ID`，用于 `openai` 和 `xai` 方案的同提供商重放判定 |
| `Tools` | 上游协议是 Responses 且为 OpenAI OAuth / OAuth-like（`!APIKey`）时为全集；OpenAI API Key、Grok、DeepSeek 等需要客户端工具适配的 Responses 路径只有 `Function`（依据现有的 `passthrough.go:238`、`prelude.go:299`、`grok/request_patch.go`、`openai_ws_http_bridge.go:626`，OpenCode/Kimi 按 `RequiresResponsesClientToolEncoding` 对齐）。Messages 上游另有 `HostedWebSearch` |
| `ToolNameMaxBytes` | Chat 和 Responses 的函数名为 64（`chatToolNameMaxLen`）；Gemini 和 Messages 按官方文档当前的限制，实施时核对 |
| `ReplayScheme` | 从 `result.Rules.SignatureMode`、规范模型作者（`result.Target`）和目标协议推导，不单纯看 `target.Record.Platform`：Anthropic/Vertex/Bedrock 及 OpenCode Claude 为 `anthropic`；Antigravity 的 `claude-*` 模型单独设为 `antigravity_claude`（避免与同分组 Gemini 签名混用）；OpenAI 及 OpenCode OpenAI 为 `openai`；Grok 及 OpenCode xAI 为 `xai`；Gemini、Antigravity 的 `gemini-*` 模型及 OpenCode Gemini 为 `gemini`；其余（含 `passback` 模型）为空 |
| `ReasoningHistory` | Chat 上游为 `tool_turns`（对应 `b567a28f9`）；Messages 上游在 `result.Rules.SignatureMode == "passback"`（`deepseek-*`、`kimi-*`、`glm-*`、`minimax-m*`、`qwen*-thinking`，含 OpenCode 对应模型）时同样为 `tool_turns`，写出无签名 `thinking` 块；其他为 `none` |
| `SchemaDialect` | Antigravity 为 `antigravity`（`CleanJSONSchema`），Gemini 为 `gemini`（`NativeCleanToolSchema`），其他为 `json_schema` |
| `Reasoning` | 现有 `GeminiThinking` 函数里从 `modelrules.Resolution` 取值的那段逻辑 |
| `StripEffort` | `ExecutionModelPolicy(target).ShouldStripWireEffort(ctx, model)` 为真时置真：先保留原始 `Info().Effort` 供计费和日志记录，再在编码前清空 `req.Reasoning.Effort`，防止 Messages 和 Gemini 编码器从 effort 派生残留的 `budget_tokens` / `thinkingBudget` |
| `DropTemperature` 等采样限制 | 从 `result.Attributes` 和目标平台规则读取。部分转换入口（`openaiforward/anthropic_flows.go`、`raw_fallback.go`、`antigravity_messages.go`）原先不在编码后调 `ApplyModelAttributeParameters`，且 `ApplyModelAttributeParameters` 只删顶层字段、删不到 Gemini 的 `generationConfig.*`，由 `Dialect` 统一向编码器提供 |
| `OmitStoreAndInclude` | Grok 的 Responses 转换入口置真（对齐 `grok_chat_execution.go:97-98` 不写 `store` 和 `include`） |
| `AllowBillingHeader` | `upstream == qoder_chat` 时置真（对齐 Qoder 保留并归一化 `x-anthropic-billing-header` 的指纹规则） |
| `PlaceholderToken` | Gemini 及 Antigravity 的 `gemini-*` 模型在非 `count_tokens` 请求下取 `DummyThoughtSignature`；Antigravity 的 `claude-*` 模型和 `count_tokens` 必须为空 |

### 3.8 转换引擎（已定）

```go
package convert

type Route struct {
	Client   protocol.ProtocolID
	Upstream protocol.ProtocolID
	Dialect  Dialect
}

type Options struct {
	Route       Route
	ClientModel string   // model name presented back to the client
	Runtime     Runtime
	Calls       CallMemory // optional upstream call bindings, see 3.10
	Tools       *ToolMap   // optional inherited tool map, for WebSocket sessions
}

// NewExchange decodes the client request for one upstream attempt.
func NewExchange(clientBody []byte, opts Options) (*Exchange, error)

func (x *Exchange) UpstreamRequest() ([]byte, error) // encoded once, then cached
func (x *Exchange) Losses() []ir.Loss
func (x *Exchange) ToolMap() *ToolMap
func (x *Exchange) Info() RequestInfo // Model, Stream, IncludeUsage, Effort

// Response converts a complete upstream response body.
func (x *Exchange) Response(upstreamBody []byte) ([]byte, error)

// Stream converts upstream frames into client frames as they arrive.
func (x *Exchange) Stream() *Stream

// Buffer consumes upstream frames and yields one complete client response body.
func (x *Exchange) Buffer() *Buffer

func (s *Stream) Write(frame sse.Frame) ([]sse.Frame, error)
func (s *Stream) Close(cause error) ([]sse.Frame, error) // nil cause: normal end of upstream
func (s *Stream) Summary() Summary                        // response id, finish, client-visible usage, content flags

func (b *Buffer) Write(frame sse.Frame) error
func (b *Buffer) Close(cause error) ([]byte, error)

// NeedsConversion reports whether a request uses cross-protocol IR conversion.
func NeedsConversion(route Route) bool // route.Client != route.Upstream

// NeedsResponsesToolAdaptation reports whether a same-protocol Responses request
// requires client-tool lowering (including inherited tools on WS turns that omit tools).
func NeedsResponsesToolAdaptation(dialect Dialect, clientBody []byte, inherited *ToolMap) bool

// AdaptResponsesTools lowers unsupported Responses client tools in place while
// preserving all unrelated request fields, item-level cache breakpoints, and compaction items.
func AdaptResponsesTools(clientBody []byte, dialect Dialect, inherited *ToolMap) ([]byte, *ToolMap, []ir.Loss, error)

// RestoreResponsesTools restores lowered client tool calls in a buffered Responses JSON body,
// and NewResponsesToolStream restores them across native Responses SSE frames without dropping
// hosted-tool streaming events.
func RestoreResponsesTools(upstreamBody []byte, tools *ToolMap) ([]byte, error)
func NewResponsesToolStream(tools *ToolMap) *ResponsesToolStream

// PrepareNativeReplay opens or removes gateway carriers in a request body that
// is forwarded natively. Bodies without a carrier at protocol token paths are returned unchanged.
func PrepareNativeReplay(route Route, body []byte) ([]byte, []ir.Loss, error)
```

每次跨协议上游尝试创建一个 `Exchange`。换号后路线不同，重新创建。解码结果不跨尝试缓存，先保持简单，基准测试表明有必要时再加。同协议 Responses 的客户端工具降级（阶段 5）走 `AdaptResponsesTools` 和 `NewResponsesToolStream`，内部复用 `lowerTools` / `raiseTools` 的命名与包装算法，只改写 `tools` 声明和工具调用/输出条目，保持其余请求字段、条目级 `prompt_cache_breakpoint`、Compact 报文和托管工具中间流事件原样透传。

请求管线：

1. 客户端协议的 `DecodeRequest`（请求体为 `/v1/chat/completions` 上无 `messages` 但带 `input` 的 Cursor 请求时，解码用 `ProtocolOpenAIResponses`，客户端响应视图仍保持 `ProtocolOpenAIChatCompletions`），然后 `ir.Validate`；先记录 `Info()`（保留剥离前的 `Effort` 供计费和日志使用）。
2. `openCarriers`：解析客户端历史里的令牌。信封还原成方案、签发提供商 ID 和签发方原文，不是信封的标为 `unknown`；按客户端调用 id 从 `CallMemory` 取回上游调用 id 和调用令牌（3.10 节）。
3. `stripClientMarkers`：跨协议且 `!Dialect.AllowBillingHeader` 时删除只对来源协议有意义的客户端标记，目前只有以 `x-anthropic-billing-header: ` 开头的 system 文本（现有 `isAnthropicBillingHeaderText`）。上报 `client_marker_dropped`。
4. `lowerTools`：`Dialect.Tools` 不支持请求里用到的工具种类时降级，生成 `ToolMap`（3.11 节）。
5. `selectReplayTokens`：按 3.10 节的写出规则保留或丢弃令牌，`plain` 令牌还原成推理文本。
6. 应用 `Dialect` 的跨协议请求调整：`StripEffort` 为真时清空 `req.Reasoning.Effort`；`DropTemperature` / `DropTopP` / `DropTopK` / `OmitStopSequences` 为真时删除对应参数并上报 `sampling_dropped` / `stop_dropped`；目标为 `ProtocolOpenAIResponses`（此时必为跨协议）时按来源协议补齐默认项（4.2 节）。
7. `ir.Link`。
8. 上游协议的 `EncodeRequest`。

响应管线，每个上游帧：

1. 上游协议的 `StreamDecoder.Decode`，然后给解码出的令牌标上 `Route.Dialect.ReplayScheme` 和 `Route.Dialect.ProviderID`。
2. `raiseTools`：按 `ToolMap` 还原工具身份。
3. `sealCarriers`：调用令牌和上游调用 id 写进 `CallMemory`；推理令牌和没有令牌的明文推理封装成信封（3.10 节）。
4. 客户端协议的 `StreamEncoder.Encode`。
5. 给每个输出帧填 `Semantic` 和 `Terminal`：`convert/output_meaning.go` 直接从结构化 `ir.Event` 与流编码器状态计算，覆盖 function/custom/search 工具、非空文本/推理/refusal 增量，以及 `[DONE]`、`message_stop`、`response.completed`、`response.incomplete`、`response.failed`、`error` 和 Chat 错误帧，不再用 `gjson` 二次反解析输出字节。

### 3.9 Loss（已定）

```go
type Loss struct {
	Kind   LossKind `json:"kind"`
	Path   string   `json:"path"`   // IR path, for example "messages[3].parts[1]"
	Detail string   `json:"detail,omitzero"`
}
```

初始的 Kind 集合：`extra_dropped`、`opaque_item_dropped`、`replay_token_dropped`、`reasoning_text_dropped`、`reasoning_text_inlined`、`cache_mark_dropped`、`media_unsupported`、`media_relocated`、`tool_lowered`、`tool_choice_approximated`、`hosted_tool_dropped`、`schema_approximated`、`system_relocated`、`parts_reordered`、`tool_pairing_repaired`、`sampling_dropped`、`stop_dropped`、`annotation_dropped`、`output_format_approximated`、`client_marker_dropped`、`candidate_count_ignored`、`id_rewritten`。新增 Kind 时同步在 `testkit` 里实现它对语义摘要的作用（5.4 节）。

用途：测试里验证上报完整并写成 golden；生产里由读取流程在 debug 级别记录每条路线的 Loss 计数，不影响请求。

### 3.10 推理连续性（已定）

问题：推理的重放数据由某个上游签发，只有同一方案的上游能接受。客户端用别的协议时，这份数据要么借客户端协议的不透明字段带回来，要么存在服务端。旧实现的做法分散在几处，全靠隐含约定：

- Responses 上游的 `encrypted_content` 原样写进 Messages 客户端的 `thinking.signature`（`responses_to_anthropic.go:113-121`）。下一轮 Messages 转 Responses 时，非空且不以 `gAAAA` 开头的签名写回 `encrypted_content`（`anthropic_to_responses.go:353-362`，为 Grok 引入，见 `d0cb4573e`）；Grok 拒绝时再用 `StripThinkingSignaturesJSON` 去掉签名重试。
- Responses 客户端转 Chat 上游时，DeepSeek 等要求工具轮次带 `reasoning_content`。网关在输出推理条目时把摘要文本按 item id 存进 Redis（`gateway/session/reasoning_history.go`，7 天），下一轮按 id 取回。
- Gemini 的推理签名不写给 Messages 客户端，`signature` 留空（`2435ab3f5`）。Gemini 的调用 id 和 `thoughtSignature` 按 API Key、提供商和上游模型存在进程内的表里（`gateway/session/gemini_tool_calls.go`，一小时），未命中时用兼容签名。

新引擎从第一个版本起采用下面的方案，上面这些规则不移植，也不为旧引擎写进客户端历史的令牌做嗅探（2.5 节）。

令牌的方案：

- 编解码器只搬运令牌原文（3.6 节），方案由 `convert` 判定。
- 从上游响应解码出的令牌，标上 `Route.Dialect.ReplayScheme`。`ReplayScheme` 为空的端点签发的令牌在封装前丢弃并上报，reasoning part 按没有令牌处理。旧实现如果把这类令牌交给了客户端，差分会把它列出来，按 6.2.4 的第 3 类处理。
- 从客户端请求解码出的令牌，`ir.ParseCarrier` 能解析的取信封里的方案；解析不了的一律标为 `unknown`，不按前缀猜签发方。

信封格式，定义在 `ir/token.go`：

```text
trc1:<scheme>:<payload>
payload 是 JSON 对象的 base64url 编码（无填充）：
  {"d": 签发方令牌原文, "t": 推理文本, "r": 是否为 redacted_thinking, "p": 签发提供商 ID（可选正整数）}
```

- `scheme` 取 `anthropic`、`antigravity_claude`、`openai`、`xai`、`gemini`、`plain` 之一。`ParseCarrier` 只接受这六个值和能解码的载荷，其他字符串都不是信封。
- `t` 出现在三种方案里。`anthropic` 和 `antigravity_claude`：上游用签名校验 thinking 文本，文本必须原样回到上游，不能依赖客户端回传的摘要。`plain`：文本只有这一份副本。其他方案的令牌自成一体，不带文本。
- `p` 在 `openai` 和 `xai` 方案里记录签发该密文的提供商 ID（`Dialect.ProviderID`）。这两个方案的加密推理与签发账号绑定，同分组换到其他账号时在本地即可识别并丢弃，省去一次必败的上游 `400 invalid_encrypted_content` 往返。
- 信封不加密，也不签名。它装的内容本来就在客户端可见的字段里，令牌真伪由上游自己校验；`plain` 的文本相当于客户端自己写的 `reasoning_content`。
- `ir.FormatCarrier` 和 `ir.ParseCarrier` 是仅有的两个入口，`convert` 的变换、原生路径的处理以及 `OpenAIEncryptedContentDigest` 共用它们。`upstream/openai/encrypted_content.go` 的 `OpenAIEncryptedContentDigest` 计算 SHA-256 前先调用 `ir.ParseCarrier` 取出内部签发方原文 `d`，确保客户端信封态和上游裸密文态在 HTTP 与 WS 会话失效密文表里得到同一个摘要。

客户端方向，`sealCarriers` 在客户端编码之前执行：

- 跨协议转换路线上，带令牌的 reasoning part 把令牌改写成信封再交给客户端编码器：Messages 写进 `thinking.signature`，redacted 的写进 `redacted_thinking.data`；Responses 写进 `reasoning.encrypted_content`。阶段 5 的同协议轻量工具适配不改写推理条目，保持原生裸令牌透传。
- 没有令牌但有文本的 reasoning part（Chat 上游的 `reasoning_content`、没有签名的 Gemini 思考摘要、`passback` Messages 上游的无签名 thinking）封装成 `plain` 信封。Messages 客户端不会再拿到空的 `signature`；Responses 客户端只回传 `encrypted_content` 时也能取回文本。
- Chat 客户端没有不透明字段。令牌丢弃并上报 `replay_token_dropped`，推理文本照常写进 `reasoning_content`。
- 调用令牌（Gemini `functionCall` 上的 `thoughtSignature`）和上游调用 id 写进 `CallMemory`，键是客户端看到的调用 id。三种客户端协议都没有逐个调用的不透明字段，这是方案里唯一需要服务端状态的部分。

上游方向，`openCarriers` 解析令牌，`selectReplayTokens` 按下表取舍：

| 令牌方案 | 写出条件 | 写出内容 | 不满足时 |
| --- | --- | --- | --- |
| 与 `Dialect.ReplayScheme` 相同 | 非账号绑定方案，或信封 `p` 与 `Dialect.ProviderID` 任一为 0，或两者相等 | 签发方原文；`anthropic` 和 `antigravity_claude` 同时用信封里的文本替换 thinking 文本 | 丢弃令牌，上报 `replay_token_dropped`；推理文本按 `ReasoningHistory` 处理 |
| `plain` | `Dialect.ReasoningHistory` 允许写回明文推理 | 推理文本（Chat 的 `reasoning_content`，或 `passback` Messages 的无签名 `thinking` 块） | 丢弃文本，上报 `reasoning_text_dropped` |
| `unknown` | 同协议路线 | 原文，等同于现在的原样透传 | 丢弃令牌，上报 `replay_token_dropped`；推理文本按 `ReasoningHistory` 处理 |
| 其他方案 | 不写出 | | 同上 |

调用令牌由 `openCarriers` 从 `CallMemory` 取回。命中且有令牌时写回原调用；命中但上游当时没有签名时，tool_call part 标 `Unsigned`；未命中时由 Gemini 编码器按现有规则写占位签名（4.4 节）。上游因签名被拒时的重试（`upstream/gemini/exchange_openai.go` 用兼容签名重试，`upstream/anthropic` 的签名修复，以及 `AsConverted` 跨 Claude 模型切号遇到签名 400 时的剥离重试）保留在上游侧，不进 `convert`。

`CallMemory` 由网关注入，注入前已经按 API Key、提供商和上游模型限定了范围：

```go
// CallMemory keeps upstream call identity that no client protocol can carry.
type CallMemory interface {
	Remember(clientCallID string, binding CallBinding)
	Recall(clientCallID string) (CallBinding, bool)
}

// CallBinding is the upstream identity behind one client-visible call id.
type CallBinding struct {
	UpstreamID string          // original upstream call id; empty when the upstream sent none
	Token      *ir.ReplayToken // call-bound replay token; nil for an observed unsigned call
}
```

网关用现有的 `session.GeminiToolCalls` 实现它（同时挂载到 `googleforward.Gemini` 和 `googleforward.Antigravity`），作用域、一小时有效期和容量上限都不变。

原生路径，`convert.PrepareNativeReplay`：客户端带着有信封的历史，下一轮可能落到原生透传路径，例如同一分组换了提供商，或者模型改由原生协议的提供商服务。Messages 原生路径（Anthropic 平台 `messageforward`、兼容平台的 Anthropic 端点）和 Responses 原生路径（OpenAI、Grok 等 HTTP Responses、`/v1/responses/input_tokens` 预检，以及专用 WS `gateway/ws/passthrough.go`、连接池 WS `gateway/ws/stream.go` 和 WS HTTP 桥 `openai_ws_http_bridge.go` 的原生转发分支）在发往上游之前调用它：

- 先做 `bytes.Contains(body, "trc1:")` 快速短路，命中后再用 `jsontext.Decoder`（开启 `jsontext.AllowInvalidUTF8(true)`）只扫描协议令牌路径：Messages 的 `messages[*]`（`role == "assistant"`）下 `content[*]` 的 `thinking.signature` 与 `redacted_thinking.data`；Responses HTTP 和 WS `response.create` 帧的顶层 `input[*]` 或 `response.input[*]` 中 `type == "reasoning"` 的 `encrypted_content`（忽略 `session.update` 等其他 WS 帧及用户/工具正文）。这些路径上没有合法 `trc1:` 信封时直接原样返回 `body` 切片，避免正文中出现字面量 `"trc1:"` 时改写空白和转义（守住 D10 的逐字节不变约束）。
- 信封方案与上游 `ReplayScheme` 匹配（`openai` 和 `xai` 同时满足 `p` 匹配规则）：还原成签发方原文 `d`，`anthropic` 同时还原 thinking 文本 `t`。
- 方案或账号不匹配：
  - Messages 且 `Dialect.ReasoningHistory == tool_turns`（`passback` 模型，如 Kimi、DeepSeek、GLM）：若信封带文本 `t`（`plain`、`anthropic`、`antigravity_claude`），保留 `thinking` 块并写入 `t`、清空 `signature`；否则删除该块。
  - 其他 Messages 路径：删除对应的 `thinking` 或 `redacted_thinking` 块；删除后若该条 assistant 消息的 `content` 数组变空，填入 `{"type":"text","text":"(assistant content removed)"}` 防止上游报空内容 400。若请求启用了 thinking 且当前工具循环的最后一条 assistant 消息不再以 `thinking` / `redacted_thinking` 块开头，用 `jsontext` 流式将该请求剩余的 `thinking` 块降级为普通文本块并移除顶层 `thinking`（不调用经过 `[]any` / `float64` 反序列化的旧 `FilterThinkingBlocksForRetry`）。
  - Responses 与 WS：若该 `reasoning` 条目仍有非空的 `summary` 或 `content`，只删除 `encrypted_content` 字段（`store: false` 时一并移除合成 `id`）；若没有任何可见内容剩余，删除整条 `reasoning` 条目（单元素 `input` 变空时保留空数组或按端点规则处理，不留孤立 `id`）。
- 除发生还原、删除或上述必要的消息级修复外，其余字段的原始字节保持不变。

Gemini 和 Chat 的原生路径不需要这一步：Gemini 客户端没有转换路线，历史里不会出现信封；Chat 没有不透明字段。

### 3.11 工具身份的降级和还原

现状：`bridge/responses_client_tools.go`、`responses_namespace.go`、`responses_tool_identity.go`、`responses_tool_history.go`、`responses_tool_search_discoveries.go`、`responses_input_namespace.go`、`client_tools_json.go`、`responses_tool_adaptation.go` 合计约 2,700 行，在线报文上把 Responses 的 custom、namespace、tool_search 工具降成普通函数，再在响应里还原。跨协议转换时这套逻辑通过 `ResponsesClientToolMapping`（77 处引用）和 `ChatCompletionsResponseToResponses` 的四个还原参数传递。

新设计（已定）：降级和还原是 `convert` 包里的两个 IR 变换，与协议无关。

- `lowerTools(req, dialect) (*ToolMap, []ir.Loss)`：
  - `Dialect.Tools` 缺 `Custom` 时，custom 工具降为函数，参数 schema 用现有的 `customToolInputSchema`，历史里的 custom 调用把输入包成 `{"input": "..."}`。
  - 缺 `ClientSearch` 时，tool_search 降为名为 `tool_search` 的代理函数，schema 用现有的 `toolSearchProxySchema`。
  - 缺 `Namespace` 时，命名空间工具拍平成单一名字。命名算法从 `flattenNamespaceToolName` 和 `customToolAlias` 移植，包括 64 字节上限、哈希后缀、Codex 默认 `functions` 作用域视为裸名、冲突检测。历史里声明已经消失的调用，用 `responsesToolHistoryNames` 的算法取重放名。
  - tool_search 结果里发现的工具声明提升进工具列表（`EffectiveResponsesTools` 的现有规则），`additional_tools` 条目由 Responses 解码器直接并入 `Tools`。
- `raiseTools(events, toolMap)`：对事件流做逆向。被降级的 custom 调用在 `part_start` 时改回 custom 身份，吞掉参数增量，在 `part_end` 拿到完整包装后解出 `input`，发一个增量再结束。tool_search 代理和命名空间同理改回身份。
- `ToolMap` 可以序列化，可以通过 `Options.Tools` 或 `AdaptResponsesTools` 的 `inherited` 参数传入上一轮状态，供 Responses WebSocket 会话跨轮继承（现有 `AdaptResponsesClientToolsJSONWithMapping` 的用法）。`ToolMap` 同时保存降级后的工具声明字节切片（`LoweredTools []jsontext.Value`），使 Codex WS 第 1 轮执行本地 `generate: false` 预热、第 2 轮省略 `tools` 字段时仍能补齐已降级的工具列表。

命名要求（已定）：同一个版本里，同样的工具声明和历史得到同样的名字；名字不超过 `Dialect.ToolNameMaxBytes`，符合目标协议的字符集；同一请求内没有冲突；`raiseTools` 能按 `ToolMap` 还原每个名字；历史里声明已经消失的调用也有确定的重放名。不要求与旧版逐字节相同（2.5 节）。移植时先照搬旧算法，差分报告列出新旧不同的名字（6.2.4 节的 D8）；有理由改进算法时可以改，改动写进进度记录。

Codex 反向表、Anthropic OAuth 工具名改写、`CodexToolCorrector`、Qoder 的名字别名是上游侧的线级策略，不在本节范围，保持现状。

### 3.12 JSON 处理约定（已定）

- 编解码器和 IR 使用 `encoding/json/v2` 和 `encoding/json/jsontext`。需要在 `tools/architecture/catalog.go` 的 `pureStandard` 里加入这两个路径（架构检查按完整路径匹配，`encoding/json` 不覆盖子包）。
- 每个编解码器在自己的 `wire.go` 里定义私有的线结构体，不导出，不复用 `protocol/openai`、`protocol/anthropic`、`protocol/gemini` 里现有的结构体。现有结构体是为旧转换器裁剪的子集，带自定义序列化，还被很多调用方使用，改它们风险大。线结构是编解码器的内部知识。
- v2 的行为差异要注意：默认拒绝无效 UTF-8（请求解码和 `PrepareNativeReplay` 显式开启 `jsontext.AllowInvalidUTF8(true)`，重复键保持默认拒绝，见 3.6）；默认大小写敏感；`omitempty` 按 JSON 空值判断，标量、指针和切片用 `omitzero`（注意 `omitzero` 只省略 `nil` 切片/map，空切片必须保持 `nil`，`Extra` 通过 `IsZero()` 省略空集合）；默认不做 HTML 转义。
- 原样保留的 JSON 片段（工具 schema、custom 工具的 format、托管工具声明、`OpaqueItem.Raw`）用 `jsontext.Value`，全程不解析成 `any`。
- 新代码不使用 gjson、sjson、`map[string]any` 处理报文，Gemini 的 schema 清洗除外（它需要遍历任意 schema，沿用现有算法时可以继续用 `map[string]any`，输入输出两端用 `jsontext.Value`）。

### 3.13 网关集成形态：按上游协议合并（已定）

接入后，每个上游协议族只剩两条路径：原生路径（现状，不动）和一条与客户端协议无关的转换路径。

请求侧，现在按来源协议分开的入口合并：

| 上游族 | 现有入口 | 合并后 |
| --- | --- | --- |
| Responses（OpenAI 兼容平台） | `openaiforward.RunMessages`（Messages 来源）、`RunChat`（Chat 来源）、Grok 的 `GrokExecutor.ChatResponses` | 一个转换入口，接收来源协议参数 |
| Chat（OpenAI 兼容平台的 raw chat） | `MessagesViaRawChat`、`ResponsesViaRawChat`（`openaiforward/raw_fallback.go`） | 一个转换入口 |
| Messages（Anthropic 平台） | `forward.AsResponses`、`forward.AsChat`（`gateway/forward/conversion.go`） | 一个 `forward.AsConverted` |
| Messages（兼容平台的 Anthropic 端点） | `ForwardNativeResponses`、`ForwardNativeChat`（`openaiforward/anthropic_flows.go`） | 一个转换入口，与上一行共用输出读取 |
| Gemini | `Gemini.Forward`（Messages 来源）、`ForwardAsResponses`、`ForwardAsChatCompletions`（`googleforward/gemini_openai.go`、`gemini_attempts.go`） | 一个转换入口 |
| Antigravity | `antigravity_messages.go` 的 `Forward`、`antigravity_openai.go` 的两个 `ForwardAs*` | 一个转换入口 |
| Qoder | `payload.go` 的三个解析器 | 一个从 IR 构造 `QoderPayloadRequest` 的函数 |

每个转换入口里，原来的"解析来源类型、调用 bridge 转换、序列化"替换成 `convert.NewExchange` 和 `UpstreamRequest`，后面的伪装、缓存限制（含 `Mimic -> EnforceCacheControlLimit -> InjectAnthropicCacheControlTTL1h` 顺序）、模型属性修正、鉴权、构造、发送步骤不变。`RunChat` 保留对 `/v1/chat/completions` 上无 `messages` 但带 `input` 的 Cursor 请求的识别：目标为 Responses 上游时走 Responses 原生/适配请求并把响应转回 Chat；目标为其他协议时请求侧按 `ProtocolOpenAIResponses` 解码、响应侧保持 `ProtocolOpenAIChatCompletions` 编码。

响应侧，现在按客户端协议分开的读取函数合并，每个上游族一对（流式和缓冲）：

| 上游族 | 现有读取函数 | 合并后 |
| --- | --- | --- |
| Responses | `upstream/openai/response_messages.go` 的 `ReadMessagesStreaming/Buffered`，`response_chat.go` 的 `ReadChatStreaming/Buffered` | `response_converted.go` 的 `ReadConvertedStreaming/Buffered` |
| Chat | `response_raw_chat.go` 的 `ReadCCAsResponsesStreaming/Buffered`、`ReadCCAsMessagesStreaming/Buffered` | `ReadRawChatConvertedStreaming/Buffered` |
| Messages | `gateway/forward/conversion_output.go` 的四个函数，`openaiforward/anthropic_output.go` 的四个函数 | 一对，放在 `openaiforward`，两类平台（含 Vertex 与 Bedrock）共用 |
| Gemini | `upstream/gemini/response.go` 的 `HandleStreamingResponse/HandleNonStreamingResponse`，`response_openai.go` 的三个处理函数 | 一对 |
| Antigravity | `response_stream.go` 的 `HandleClaudeStreamingResponse`，`response_compat_stream.go`、`response_compat_nonstream.go` | 一对 |
| Qoder | `stream_conversion.go` 的三个 `WriteQoder*StreamResponse` 和三个 `BuildQoder*` | 一对 |

合并后的读取函数保留现有循环里的全部上游原生逻辑：SSE 扫描、用量累加（`wire.ParseSSEUsageBytesWithType`、`UsageAccumulator`、`NormalizeStreamUsage`、Gemini 的 `observeRaw`）、转换前的原始 `ResponseModelObserver` 采集（正常返回与读错部分结果均写入 `UpstreamResponseModel`）、故障分类和切号、cyber 策略、缺失终止事件的处理、客户端断开后继续排空。只有"驱动 bridge 状态机并序列化"那一段换成 `stream.Write(frame)` 和写出返回的帧。有两个流控不变量必须保留：

1. 前导帧暂存窗口：`OutputContext.NextEvent` 会无条件置 `CommitForRetry: true`，写出非空字节即关闭故障转移窗口。因此 `ReadConvertedStreaming` 在收到首个 `Semantic == true` 的输出帧或不可重试终态之前，先把非语义前导帧（如 `response.created` 转出的 `message_start` 或 Chat 首块）暂存在本地缓冲，同时经过 `responseStreamLifecycleTracker`（支持 Codex 孤立 `error` 帧被后续 `response.completed` 恢复）、`EmptyCompleted` 检查和 Chat 的 `ChatSilentRefusalDetector`，确保收到可重试错误帧时仍能切号。
2. 延迟提交响应头与提前提交 200 后的终态 SSE 错误：流式读取器统一通过 `NewDeferredOutputContext` 在首个实际输出帧写出时再提交响应头，不在读首帧前调用 `WriteHeader(200)` 关闭等待期保活窗口；一旦 `DownstreamSSEKeepalive` 已提前提交 HTTP `200 text/event-stream`（`CommittedSSE(c) == true`），后续超时或上游失败一律通过无计时的本地错误写出回调发送客户端协议的终态 SSE 错误帧（Responses 客户端固定发合成 `response.failed`，见 `httpapi/stream_error.go`）。

用量的处理（已定）：计费用量继续由读取循环里现有的累加器产生，本次重构不改计费路径。客户端可见的用量由流解码器产生，解码器内部调用同一批带 `wirejson` 校验的归一化函数（`protocol/openai` 的 `ResponsesUsage` 解码和 `UsageAccumulator`，`protocol/anthropic` 的 `MergeAnthropicUsage` / `ParseSSEUsagePassthrough`，Gemini 的跨帧 `usageMetadata` 合并提取），不重新实现别名和缓存桶规则。T5 层有一条断言核对两者一致（5.6 节）。

输出抽象：统一用 `upstream.OutputContext`。`gateway/forward.Output` 接口和 `httpapi.ForwardConversionOutput` 适配器（连同 `openai_native_anthropic_ports.go:224` 的 `writeTextResponsesError` 调用点）在阶段 8 随 `conversion_output.go` 一起迁移并删除。

`UnifiedTextExecutor.native` 里按 `source` 的 `switch` 相应简化：每个平台在"来源协议等于路线协议"时调原生入口，否则调转换入口。`usesOpenAIExecutor` 的分派保持不变。

切换方式（已定）：每个上游族的转换入口和读取函数在阶段 2 到 6 接入，放在 2.4 节的接线开关后面，生产装配一直走旧流程。阶段 8 一次删除旧流程和接线开关。不设进程配置，没有回退开关。阶段 8 之前，T5 对旧路径和新路径都跑：旧路径的结果与 `legacy_baseline.json` 比较，新路径必须全绿。

## 4. 各协议的映射规范

本节给出规范映射。旧实现里的细节行为（默认值、下限、修复规则）以 6.2.4 节的差分流程为准：差分发现新旧不同时，除非属于验收差异清单里的类别，否则向旧行为对齐。推理令牌的取舍不在本节，统一按 3.10 节。每个小节末尾列出要对照的旧代码。

所有解码器的共同规则：

- 字符串形式的 content 解码成单个 text part。
- 未知的块或条目类型解码成 `OpaqueItem`，不报错。
- 每个对象上未声明的成员进 `Extra`。

### 4.1 Messages（`anthropic_messages`）

请求解码：

| 线字段 | IR |
| --- | --- |
| `system`（字符串或 text 块数组） | `System`，块上的 `cache_control` 进 `Part.Cache` |
| `messages[].role` 为 `user`、`assistant`、`system` | `Message.Role` |
| `text` 块 | text part，`citations` 进 `Extra` |
| `image` 块，`source.type` 为 `base64`、`url`、`file` | media part（image） |
| `document` 块 | media part（document），`title`、`context`、`citations` 进 `Extra` |
| `thinking` 块 | reasoning part，`thinking` 进 `Segments`，`signature` 原文进 `Token.Data` |
| `redacted_thinking` 块 | reasoning part，`Redacted` 为真，`data` 原文进 `Token.Data` |
| `tool_use` 块 | tool_call part，`id` 进 `CallID`，`input` 的原始 JSON 文本进 `Arguments` |
| `tool_result` 块 | 归入 `tool` 角色消息的 tool_result part；`content` 的 text 和 image 块进 `Content`；`is_error` 保留 |
| `server_tool_use`、`web_search_tool_result`、其他块 | `OpaqueItem` |
| `tools[]` 无 `type` 或 `type` 为 `custom` | function 工具，`input_schema`、`strict`、`defer_loading`、`cache_control` 对应 |
| `tools[]` 其他 `type` | hosted 工具，整项进 `Tool.Hosted` |
| `tool_choice` 的 `auto`、`any`、`tool`、`none` | `ToolChoice.Mode` 的 `auto`、`required`、`tool`、`none` |
| `tool_choice.disable_parallel_tool_use` | `ParallelToolCalls` 取反 |
| `max_tokens`、`temperature`、`top_p`、`top_k`、`stop_sequences`、`stream` | 对应字段 |
| `thinking.type`、`thinking.budget_tokens` | `Reasoning.Mode`、`Reasoning.BudgetTokens` |
| `output_config.effort`、`output_config.format` | `Reasoning.Effort`、`Output` |
| `metadata.user_id`、`prompt_cache_key`、`session_id`、`conversation_id` | `UserID` 与 `MetadataUserID`、`CacheKey`、`SessionID`、`ConversationID` |
| `speed` 为 `fast` | `ServiceTier` 为 `priority` |

一条 user 消息里既有 `tool_result` 又有其他块时，按出现顺序切成连续段：`tool_result` 段成为 `tool` 消息，其他段成为 `user` 消息，段之间的相对顺序不变。

请求编码（Messages 作为上游），合法化步骤在编码器内部按这个顺序做：

1. `System` 加上开头连续的 system、developer 消息合并成 `system` 块数组。对话中间的 system、developer 消息按旧实现处理并上报 `system_relocated`。
2. `tool` 消息和紧随其后的 `user` 消息合并成一条 user 消息，`tool_result` 块在前。
3. 相邻的同角色消息合并（旧 `mergeConsecutiveMessages`）。
4. 工具配对修复：没有结果的调用和没有调用的结果按旧 `normalizeAnthropicToolPairing` 处理，上报 `tool_pairing_repaired`。
5. reasoning part：经过 `selectReplayTokens` 后仍带令牌的，写成 `thinking` 或 `redacted_thinking` 块，位置保持在同一条 assistant 消息里的原位；没有令牌时，若 `Dialect.ReasoningHistory == tool_turns`（`passback` 模型，如 Kimi、DeepSeek、GLM），在带工具调用的 assistant 消息开头写出无签名的 `thinking` 块，否则按旧行为丢弃或内联成文本并上报对应 Loss。
6. `MaxOutputTokens` 缺省时用旧实现的默认值；`Reasoning` 通过 `Dialect.Reasoning` 解析出 `thinking` 和 `output_config.effort`（`ControlsKnown == false` 时保留 `"max"` 档位，`"ultra"` 档位返回 400）；`ServiceTier` 为 `priority`、`fast` 或 `ultrafast` 时映射出 `speed: "fast"`。
7. `Output` 的 json_schema 写进 `output_config.format`。
8. `ToolResult.Content` 里的图片保留在 `tool_result` 块内。

流解码（Messages 作为上游）：

| 线事件 | IR 事件 |
| --- | --- |
| `message_start` | `response_start`，并发一个 `usage`（保留 `usage.speed` 和 5m/1h 缓存写入桶，经 `wirejson` 校验） |
| `content_block_start` | `part_start`，按块类型建骨架 |
| `content_block_delta` 的 `text_delta`、`thinking_delta` | `part_delta.Text` |
| `input_json_delta` | `part_delta.Arguments` |
| `signature_delta` | 累积进当前 reasoning part 的 `Token`，在 `part_end` 交付 |
| `content_block_stop` | `part_end`，带完整快照 |
| `message_delta` | `usage`（与 `message_start` 用量按字段合并，保留 `speed`）；记下 `stop_reason` 和 `stop_sequence` |
| `message_stop` | `response_end` |
| `error` | `error` |
| `ping` | 丢弃（心跳由读取侧处理） |

`stop_reason` 到 `Finish.Reason`：`end_turn` 为 `stop`，`max_tokens` 和 `model_context_window_exceeded` 为 `length`，`stop_sequence` 为 `stop_sequence`，`tool_use` 为 `tool_calls`，`refusal` 为 `content_filter`，`pause_turn` 为 `pause`。

流编码（Messages 作为客户端）：事件序列必须是 `message_start`，若干个"`content_block_start`、增量、`content_block_stop`"，`message_delta`，`message_stop`。块的 `index` 是已输出块的序号，被丢弃的 part 不占序号。`message_start.message` 带 `id`、`type`、`role`、`model`、空的 `content`、`stop_reason: null`、`usage`（带 `Speed` 时同步写出 `usage.speed`）。reasoning part 的令牌在 `content_block_stop` 之前用 `signature_delta` 发出。`error` 事件写成 `event: error`。

对照的旧代码：`anthropic_to_responses.go`、`responses_to_anthropic_request.go`、`responses_to_anthropic.go`、`anthropic_to_responses_response.go`、`chatcompletions_anthropic_bridge.go`、`compat_replay_guard.go`。

### 4.2 Responses（`openai_responses`）

请求解码：

| 线字段 | IR |
| --- | --- |
| `instructions` | `System`，一个 text part |
| `input` 为字符串 | 一条 user 消息 |
| `message` 条目（含无 `type` 只有 `role` 的简写） | 消息；`input_text`、`output_text` 为 text part，`input_image` 为 media，`input_file` 为 media（document），`refusal` 为 refusal part；`output_text.annotations` 的 `url_citation` 进 `Annotations`，其他进 `Extra` |
| `reasoning` 条目 | assistant 消息里的 reasoning part：`id` 进 `Part.ID`，`summary[]` 进 `Segments` 且 `Summary` 为真，`content[]`（`reasoning_text`）进 `Segments` 且 `Summary` 为假，`encrypted_content` 原文进 `Token.Data` |
| `function_call` 条目 | tool_call part：`call_id`、`name`、`namespace`、`arguments`，`id` 进 `Part.ID` |
| `custom_tool_call` 条目 | tool_call part，`Tool.Kind` 为 custom，`input` 进 `Arguments` |
| `tool_search_call` 条目 | tool_call part，`Tool.Kind` 为 client_search |
| `function_call_output`、`custom_tool_call_output`、`tool_search_output` | `tool` 消息里的 tool_result part；`output` 是字符串时为单个 text，是内容数组时逐项解码 |
| `additional_tools` 条目 | 并入 `Tools`（现有 `LiftResponsesAdditionalTools`） |
| 其他条目（`compaction`、`local_shell_call`、`web_search_call`、`mcp_*`、`image_generation_call`、`item_reference`、`configuration_update` 等） | 所在位置的 `OpaqueItem` |
| `tools[]` 的 `function`、`custom`、`namespace`、`tool_search` | 对应 Kind；namespace 的子工具展开成带 `Namespace` 的工具，声明本身进 `Namespaces` |
| `tools[]` 其他类型（`web_search` 等） | hosted 工具 |
| `tool_choice`、`parallel_tool_calls` | 对应字段（`allowed_tools` 进 `ToolChoice.AllowedTools`） |
| `max_output_tokens`、`temperature`、`top_p`、`stream` | 对应字段 |
| `reasoning.effort`、`reasoning.summary` | `Reasoning.Effort`、`Reasoning.Summary` |
| `include[]` 含 `reasoning.encrypted_content` | `Reasoning.IncludeEncrypted`；其余值进 `Extra` |
| `text.format`、`text.verbosity` | `Output` |
| `service_tier`、`prompt_cache_key`、`previous_response_id`、`store` | 对应字段 |
| `conversation`、`session_id` | `ConversationID`（非 null 时 `ConversationSet` 为真）、`SessionID` |
| `user`、`metadata.user_id`、`safety_identifier` | `UserID`、`MetadataUserID`，`safety_identifier` 进 `Extra` |

平铺条目归并成消息的规则：连续的 assistant 侧条目（reasoning、role 为 assistant 的 message、各类调用、assistant 侧的不透明条目）归入同一条 assistant 消息；连续的工具结果归入同一条 `tool` 消息；user、system、developer 的 message 各自成消息。归并不改变顺序，编码时按原顺序拍平可以得到同样的条目序列。

请求编码（Responses 作为上游）：

1. `System` 写成 `instructions`（多个 part 用换行连接）还是开头的 developer 消息，按旧实现（`anthropic_to_responses.go` 把 system 放进 `input` 的 developer 条目以保持位置）。
2. 消息按顺序拍平成条目。assistant 的 text 用 `output_text`，其他角色用 `input_text`。
3. reasoning part：经过 `selectReplayTokens` 后仍带令牌的，令牌写进 `encrypted_content`；没有令牌只有文本的推理按旧行为处理。需要 `id` 而 IR 里没有时，用内容哈希派生。
4. 跨协议默认策略（仅在 `convert` 确认 `Route.Client != ProtocolOpenAIResponses` 时注入，`Responses.EncodeRequest` 本身不注入这些默认值，以保证定律 Q2 的同协议往返不变）：来源是 Messages 时默认设置 `parallel_tool_calls: true`、`text.verbosity: "medium"`、`reasoning.summary: "auto"`（未指定 effort 时补默认档位），正数 `max_output_tokens` 取不小于 128；来源是 Chat 时强制 `stream: true` 且正数 `max_output_tokens` 取不小于 128；`!Dialect.OmitStoreAndInclude` 时补 `include: ["reasoning.encrypted_content"]` 和 `store: false`（Grok 跳过这两项）。
5. 工具结果里的图片：Responses 的 `function_call_output.output` 支持内容数组时直接放入；旧实现把图片移到后面的 user 消息，按旧行为做并上报 `media_relocated`。

流解码（Responses 作为上游）：

| 线事件 | IR 事件 |
| --- | --- |
| `response.created` | `response_start`（含 `service_tier`） |
| `response.in_progress` | 无 |
| `response.output_item.added`（reasoning、function_call、custom_tool_call、tool_search_call） | `part_start` |
| `response.output_item.added`（message） | 无，等内容 part |
| `response.content_part.added`（output_text、refusal） | `part_start` |
| `response.output_text.delta`、`response.refusal.delta`、`response.reasoning_summary_text.delta`、`response.reasoning_text.delta` | `part_delta.Text` |
| `response.function_call_arguments.delta`、`response.custom_tool_call_input.delta` | `part_delta.Arguments` |
| `response.content_part.done` | text 或 refusal 的 `part_end` |
| `response.output_item.done` | reasoning、调用类条目的 `part_end`，以该事件里的完整条目为准（`encrypted_content`、最终参数）；message 条目无事件 |
| `response.output_item.done`（其他类型，如 `web_search_call`） | 一对 `part_start`、`part_end`，内容是 `OpaqueItem` |
| `response.completed`、`response.incomplete` | 一个 `usage`（含 30m/12h 缓存写入分档及外层零值回填、图片与音频明细，经 `wirejson` 校验），然后 `response_end`（含 `service_tier`） |
| `response.failed`、`error` | `error`（外层读取循环先过 `responseStreamLifecycleTracker`，Codex 模式下孤立 `error` 帧暂存至终态再决定恢复或发错） |

解码器要容忍上游的不规范：只有 `output_item.done` 没有增量时，直接补发 `part_start` 和 `part_end`；终止事件里 `output` 为空但前面有条目时以累积为准（现有 `BufferedResponseAccumulator.SupplementResponseOutput` 的规则）；`response.done` 等旧事件名和裸的 Response 对象按现有 `protocol/openai/response_lifecycle.go` 的归一化规则处理（`4296d1e3a`）。状态和原因到 `Finish`：`completed` 且含非 hosted 调用为 `tool_calls`，否则 `stop`；`incomplete` 的 `max_output_tokens` 为 `length`，`content_filter` 为 `content_filter`。

流编码（Responses 作为客户端）：

- 每个事件带递增的 `sequence_number`。
- `response.created` 和 `response.in_progress` 带完整的 `response` 骨架（`status: "in_progress"`、空 `output`、可选 `service_tier`）。
- 连续的 text、refusal part 归入同一个 message 条目：第一个 part 开始时发 `output_item.added`，每个 part 发 `content_part.added`、增量、`output_text.done`、`content_part.done`，`content_index` 逐个递增；遇到其他 Kind 的 part 或流结束时发该 message 的 `output_item.done`。这条规则对应 `ac38d88f0`。
- reasoning part：`output_item.added`，每个分段一组 `reasoning_summary_part.added`、`reasoning_summary_text.delta`、`reasoning_summary_text.done`、`reasoning_summary_part.done`，最后 `output_item.done` 带 `encrypted_content`（part 带令牌时）。
- 调用类 part：`output_item.added`（`arguments` 为空串），增量，`function_call_arguments.done`，`output_item.done`。custom 调用用 `custom_tool_call_input.*`。增量事件里不带 `name`（`091ec1f41`）。
- `response.completed` 的 `response.output` 是全部条目，`usage` 里 `input_tokens_details.cached_tokens` 和 `output_tokens_details.reasoning_tokens` 必须存在（Codex 对这两个明细字段是严格解析），30m 和 12h 缓存写入分档相加等于 `cache_write` 时输出 `cache_write_tokens_details`。
- `error` 事件：已经发过 `response.created`，或下游 HTTP `200 text/event-stream` 已由保活心跳提前提交时，统一写合成的 `response.failed`（带 `id`、`object: "response"`、`created_at`、`status: "failed"` 和空 `output`，对齐 `httpapi/stream_error.go`）；其余未提交情况按客户端错误格式写出。

对照的旧代码：`chatcompletions_to_responses.go`、`chatcompletions_responses_bridge.go`、`responses_to_chatcompletions.go`、`response_forward_wire.go`、`protocol/openai/responses_stream_event_wire.go`。

### 4.3 Chat Completions（`openai_chat_completions`）

请求解码：

| 线字段 | IR |
| --- | --- |
| `messages[].role` 为 `system`、`developer`、`user`、`assistant`、`tool`、`function` | 对应角色，`function` 视为 `tool` |
| content 的 `text`、`image_url`、`input_audio`、`file` 部分 | text、media |
| assistant 的 `reasoning_content`，其次 `reasoning` | reasoning part，放在该消息的最前面，`Summary` 为假 |
| assistant 的 `tool_calls[]`，以及旧的 `function_call` | tool_call part，`id` 进 `CallID` |
| assistant 的 `refusal` | refusal part |
| `tool` 消息 | tool_result part，`tool_call_id` 进 `CallID` |
| `tools[]` 的 `function`，以及旧的 `functions[]` | function 工具 |
| `tool_choice`、旧的 `function_call` | `ToolChoice`（含 `allowed_tools`） |
| `max_completion_tokens`，其次 `max_tokens` | `MaxOutputTokens` |
| `temperature`、`top_p`、`seed`、`frequency_penalty`、`presence_penalty`、`n`、`stop` | 对应字段，`stop` 为字符串时成为单元素数组 |
| `reasoning_effort`，以及嵌套的 `reasoning.effort` | `Reasoning.Effort`，嵌套形式优先（现有 `ExtractEffort` 的规则） |
| `response_format` | `Output` |
| `stream`、`stream_options.include_usage` | `Stream`、`IncludeUsage` |
| `service_tier`、`prompt_cache_key`、`user`、`metadata.user_id`、`session_id`、`conversation_id`、`parallel_tool_calls`、`store` | 对应字段 |

请求编码（Chat 作为上游）：

1. `System` 和 system、developer 消息写成 `system` 或 `developer` 角色的消息，角色保留；DeepSeek 不接受 `developer` 的问题由现有的线级后处理解决（`protocol/openai/compatibility_fields.go`），编码器不管。
2. assistant 消息：text part 连接成 `content`；tool_call part 写成 `tool_calls`；reasoning 文本按 `Dialect.ReasoningHistory` 写进 `reasoning_content`（`tool_turns` 表示只在带工具调用的 assistant 消息上写）。重放令牌丢弃并上报。
3. `tool` 消息的每个 tool_result 写成一条 `tool` 角色消息。结果里的图片按旧实现移到后面的 user 消息并加归属标记（现有 `toolOutputMediaMarker`、`toolOutputMediaAttribution`），上报 `media_relocated`。
4. 工具配对修复、空消息清理按旧 `normalizeChatMessages`。
5. `Output` 的 json_schema 写成 `response_format.json_schema` 包装（现有 `response_format.go`）。
6. 参数不是合法 JSON 的工具调用及其结果按旧行为丢弃并上报。

流解码（Chat 作为上游）：

- 第一个 chunk 产生 `response_start`（`id`、`model`、`created`、`service_tier`）。
- `delta.content` 产生 text part 的开始和增量；`delta.reasoning_content` 或 `delta.reasoning` 产生 reasoning part，前者优先，前者显式为空串时不回退到后者（现有 `chat_stream_compat.go` 的规则）。
- `delta.tool_calls[]` 按 `index` 跟踪。后续增量里的空 `id`、空 `name` 不覆盖已有值（`826f45a2c`）。名字未知之前到达的参数先缓冲。并行调用交错时由 `ir.Sequencer` 顺序化。
- 推理、文本、工具调用之间切换时关闭上一个 part。
- `finish_reason` 记下，等到流结束或 `[DONE]` 时发 `response_end`；带 `usage` 的 chunk（包括 `choices` 为空的尾块）经 `wirejson` 校验和 `UsageAccumulator` 合并，并调用 `protocol.IncludeIndependentReasoningTokens` 计入 Grok 等把 `reasoning_tokens` 独立于 `completion_tokens` 报告的场景，产生 `usage`。
- 没有任何终止信号就断流时，`Close` 发 `error`（`bd39eb589`）。
- 上游重发累计内容的情况不在 Chat 解码器里处理（那是 Gemini 兼容上游的问题，见 4.4）。

流编码（Chat 作为客户端）：第一个 chunk 带 `delta.role: "assistant"`；text 增量写 `delta.content`；reasoning 增量写 `delta.reasoning_content`；工具调用在 `part_start` 时发带 `index`、`id`、`type`、`function.name` 和空 `arguments` 的 chunk，之后的参数增量 chunk 只带 `index` 和 `function.arguments`；`response_end` 发带 `finish_reason` 的 chunk；`IncludeUsage` 为真时再发 `choices` 为空、带 `usage`（含音频与预测明细）的 chunk（旧 `ReadChatStreaming` 在 Responses->Chat 路线上曾忽略客户端开关强制发 usage，统一按 `IncludeUsage` 控制后产生的差异归入 D3）；最后发 `[DONE]`。所有 chunk 的 `id`、`created`、`model` 相同，带 `ServiceTier` 时同步写出 `service_tier`。

`Finish.Reason` 到 `finish_reason`：`stop`、`stop_sequence`、`pause` 为 `stop`，`length` 为 `length`，`tool_calls` 为 `tool_calls`，`content_filter` 为 `content_filter`。

对照的旧代码：`chatcompletions_to_responses.go`、`chatcompletions_anthropic_bridge.go`、`chatcompletions_responses_bridge.go`、`responses_to_chatcompletions.go`、`protocol/openai/chat_stream_compat.go`、`upstream/openai/response_raw_chat.go`。

### 4.4 Gemini GenerateContent（`gemini_generate_content`）

请求解码：

| 线字段 | IR |
| --- | --- |
| `systemInstruction.parts[]` | `System` |
| `contents[].role` 为 `user`、`model` | `user`、`assistant` |
| part 的 `text`（`thought` 不为真） | text part |
| part 的 `text` 且 `thought` 为真 | reasoning part，`Summary` 为真 |
| part 的 `thoughtSignature` | 原文进所在 part 的 `Token.Data`（reasoning 或 tool_call）；挂在普通 text part 上的签名进该 part 的 `Extra` |
| part 的 `inlineData`、`fileData` | media part |
| part 的 `functionCall` | tool_call part，`id` 有则进 `CallID`，没有时用"消息序号加 part 序号"的内容哈希派生 |
| part 的 `functionResponse` | `tool` 消息里的 tool_result part，`response` 的 JSON 文本进一个 text part |
| part 的 `executableCode`、`codeExecutionResult` 等 | `OpaqueItem` |
| `tools[].functionDeclarations[]` | function 工具，`parameters` 或 `parametersJsonSchema` 进 `InputSchema` |
| `tools[]` 的 `googleSearch`、`codeExecution`、`urlContext` 等 | hosted 工具 |
| `toolConfig.functionCallingConfig` 的 `mode` 和 `allowedFunctionNames` | `ToolChoice` |
| `generationConfig` 的 `maxOutputTokens`、`temperature`、`topP`、`topK`、`stopSequences`、`seed`、`candidateCount`、`presencePenalty`、`frequencyPenalty` | 对应字段 |
| `generationConfig.thinkingConfig` 的 `thinkingBudget`、`thinkingLevel`、`includeThoughts` | `Reasoning` |
| `generationConfig` 的 `responseMimeType`、`responseSchema`、`responseJsonSchema` | `Output` |
| `safetySettings`、`cachedContent`、`labels` | `Extra` |

模型名和流式标记在 URL 路径里，不在请求体里。`DecodeRequest` 只处理请求体；`convert.Options` 不需要为此加字段，因为本计划范围内 Gemini 只作为上游，客户端侧的解码只在测试里用（测试直接比较请求体）。

请求编码（Gemini 作为上游）：

1. `System` 和 system、developer 消息合并进 `systemInstruction`。
2. `assistant` 写成 `model`，`tool` 消息写成带 `functionResponse` 的 `user` 内容；相邻同角色内容合并。
3. tool_call part 写成 `functionCall`，`Arguments` 解析成对象；带令牌时写 `thoughtSignature`。没有令牌、没有 `Unsigned` 标记且 `Dialect.PlaceholderToken` 非空时写占位值，条件按现有 `NativeEnsureGeminiFunctionCallThoughtSignatures` 和 `4b05d79a5`：每个步骤的首个调用缺签名时写占位值，后续已知未签名的并行调用保持原样。
4. tool_result part 写成 `functionResponse`，`name` 取 `ToolResult.Tool.Name`，`response` 按旧 `ParseToolResultContent` 的规则包装。
5. reasoning part：带令牌的随 `thought` part 写出；没有令牌时，普通 Gemini 不发历史思考块并上报，而 Antigravity 的 `claude-*` 模型（`Dialect.ReplayScheme == "antigravity_claude"`）按现有 `BuildParts` 规则把无签名历史思考降级为可见 `text` part 并清空本次请求的 `thinkingConfig`。
6. 工具 schema 按 `Dialect.SchemaDialect` 清洗：`gemini` 用现有 `NativeCleanToolSchema` 的算法，`antigravity` 用 `CleanJSONSchema` 的算法。两个算法都实现为纯函数（同时供保留的 `upstream/antigravity.CleanGeminiRequest` 复用），上报 `schema_approximated`。
7. `Reasoning` 通过 `Dialect.Reasoning` 解析成 `thinkingConfig`（移植 `ResolveGeminiThinking`）；`maxOutputTokens` 与预算的关系按 `EnsureMaxTokensGreaterThanBudget`；`generationConfig` 同步按 `Dialect.DropTemperature` / `DropTopP` / `DropTopK` / `OmitStopSequences` 过滤不支持的采样与停止序列参数。
8. 托管的网页搜索工具和函数工具并存时的处理按现有 `BuildInternalGeminiTools`、`InternalHasMixedToolInvocations`。
9. AI Studio 的字段归一化（`NativeNormalizeGeminiRequestForAIStudio`，迁入 `protocol/gemini`）和 Antigravity 的信封、身份补丁是线级后处理，留在 `upstream/gemini` 和 `upstream/antigravity`。

流解码（Gemini 作为上游）：

- 每个帧是一个 `GenerateContentResponse`，只看第一个候选。第一个帧产生 `response_start`（`responseId`、`modelVersion`）。
- text part 的文本是增量，但兼容上游可能发累计文本：按现有 `NativeComputeGeminiTextDelta` 的规则算出真正的增量。
- `thought` 为真的文本产生 reasoning part。
- `functionCall` 一次到齐，产生一组 `part_start`、`part_end`。兼容上游重发累计批次时按现有 `toolFrames` 的去重规则处理（`2435ab3f5`）。同名且没有 id 的多个调用不能合并（`d91aacd8e`）。没有 id 时用 `Runtime.NewID` 生成 `CallID`，`sealCarriers` 把它和上游 id、签名的对应关系写进 `CallMemory`。
- `usageMetadata` 先跨帧合并非 null 字段（避免只有 `candidatesTokenCount` 的尾帧冲掉先前的 `promptTokenCount` / `cachedContentTokenCount`），再计算 `usage`：`Input = promptTokenCount - cachedContentTokenCount`，`CacheRead = cachedContentTokenCount`，`Output = candidatesTokenCount + thoughtsTokenCount`，`Reasoning = thoughtsTokenCount`（现有 `NativeExtractGeminiUsage`）。
- `finishReason` 在 `Decode` 中记下，不在看到 `finishReason` 的当帧立即发 `response_end`（读取侧 `ReadGeminiStream` 在看到终态后会调 `c.Complete()` 停发保活并继续收集最多五秒的用量尾帧，见 `docs/interfaces/gemini_upstream.md#gemini_stream_lifecycle`）；等 `ReadGeminiStream` 返回后调用 `StreamDecoder.Close(nil)` 时再发出最终 `usage` 和 `response_end`，严格遵守 3.5 节不变量 6。`STOP` 且有函数调用为 `tool_calls`，否则 `stop`；`MAX_TOKENS` 为 `length`；`SAFETY`、`RECITATION`、`BLOCKLIST`、`PROHIBITED_CONTENT`、`SPII` 为 `content_filter`；其他为 `other`。
- `groundingMetadata`：阶段 3 保持旧行为，把来源整理成 Markdown 的 "Sources:" 尾注追加为文本，同时把结构化的引用放进 `Annotations`，供以后的编码器使用。

流编码（Gemini 作为客户端，只用于测试和脚本化上游）：每个增量事件写成一个带单个 part 的响应帧；tool_call 在 `part_end` 写成完整的 `functionCall`；`response_end` 写成带 `finishReason` 和 `usageMetadata` 的帧。

对照的旧代码：`gemini_native.go`、`gemini_contents.go`、`gemini_internal_options.go`、`gemini_internal_schema.go`、`gemini_thinking.go`、`gemini_stream.go`、`gemini_response.go`、`gemini_messages_stream.go`、`gemini_compat_stream.go`、`upstream/antigravity/request_transformer.go`。

### 4.5 Qoder（`qoder_chat`，只有上游侧）

- `protocol/qoder` 定义归一化的 `PayloadRequest`、消息、工具、工具选择与客户端能力拒绝错误（`RequestError`）；`codec/qoderchat.EncodeRequest` 将 `ir.Request` 编码成该结构体的 JSON 字节，取代 `ParseQoderChatCompletionsPayload`、`ParseQoderResponsesPayload`、`ParseQoderAnthropicMessagesPayload`（管理端连接测试 `provider/provider/qoder_test_execution.go` 同步改用该路径）。
- 编码阶段完整保留 `02f1b789f` 和 `201a0326d` 的无状态与会话规则：Responses 来源遇到 `Store == true`、`PreviousResponseID != ""`、`ConversationSet`、历史含 `OpaqueItem{Type: "item_reference"}` 或未配对的 `tool_result` 时直接返回类型化 `RequestError`（由外层映射为本地 `400 MODEL_CAPABILITY_UNSUPPORTED`，不触发切号和健康扣分）；提取 `SessionID`、`ConversationID`、`CacheKey`、`MetadataUserID` 和 `ToolChoice.AllowedTools`；`Dialect.AllowBillingHeader` 保留 `x-anthropic-billing-header` 供 Qoder 指纹归一化 `cch=<cch>;`。会话规划、指纹、站点和模型能力（含 `attrs.ToolCall == false` 时裁剪可选工具并拒绝必选 `tool_choice`）仍在 `upstream/qoder` 的 `withEffectiveControls` 执行。
- `StreamDecoder` 把 Qoder 的 SSE 事件解码成 IR 事件，包含现有的文本编码工具调用解析（`stream_tool_text.go`）和参数合并规则（`MergeQoderToolArguments`）。
- 工具名别名（`QoderToolNameMapper`）是 Qoder 侧的线级策略，在解码之后作为事件变换应用，留在 `upstream/qoder`。

## 5. 测试体系

### 5.1 分层

| 层 | 名称 | 位置 | 验证什么 | 何时运行 |
| --- | --- | --- | --- | --- |
| T1 | IR 单元测试 | `protocol/ir`、`protocol/sse` | 校验器、累加器、顺序化器、SSE 读取器、信封 | 每次 `make test-go` |
| T2 | 编解码器一致性 | `protocol/codec/*` | 单协议的解码、编码、流语法、不动点定律、fuzz | 每次 |
| T3 | 转换矩阵 | `protocol/convert` | 场景乘路线的定律、多轮重放、发布前与旧实现的差分 | 每次 |
| T4 | 语料回放 | 同 T2、T3，数据来自录制语料 | 真实客户端请求和真实上游流通过全部定律；未建模字段普查 | 每次 |
| T5 | 网关路由测试 | `internal/app` | 真实路由、执行器、输出包装加脚本化上游：选路、SSE 提交、保活、切号、错误格式、用量一致 | 每次 |
| T6 | 客户端实验室 | `internal/app` 加 `tools/clientlab` | 真实的 Codex、Claude Code、pi 二进制驱动真实网关路由，上游是脚本化假上游或真实提供商 | 路径触发、每晚、手动 |

T1 到 T5 都不需要 Docker 和网络，属于无标签的单元测试。T6 用环境变量开启，默认跳过。

一个重要的限制：T2、T3 里"编码再解码"的自洽测试发现不了编码器和解码器对协议的共同误解。事实来源只有四个：录制的真实流量（T4）、真实客户端（T6）、官方 SDK、真实上游（T6 的 live 模式）。所以语料库和客户端实验室不是可选项。

### 5.2 `protocol/testkit` 的组成

| 文件组 | 内容 |
| --- | --- |
| `corpus*.go` | 用 `embed` 内嵌 `testdata/`，提供按协议、类型、标签查询夹具的函数。纯包的测试通过它读数据，自己不导入 `os` |
| `golden.go` | `Golden(t, name, got)`：与调用包 `testdata/golden/` 下的文件比较，`TOKENROUTER_UPDATE_GOLDEN=1` 时写回（不用 `flag.Bool("-update")`，避免 `go test ./internal/protocol/...` 在未导入 `testkit` 的兄弟包上因未知参数失败） |
| `jsondiff.go` | JSON 语义比较，输出 JSON 路径级的差异 |
| `grammar_*.go` | 四个协议的流语法校验器：输入一串 `sse.Frame`，按状态机检查事件顺序、必填字段、索引连续性、终止条件，返回违规列表 |
| `request_rules_*.go` | 四个协议的严格请求校验器，直接检查线上的 JSON，按规则集返回违规（5.2.1） |
| `clientsim_*.go` | 四个协议的客户端模拟器：给定上一轮请求、本轮响应和工具结果的线报文，按真实客户端的习惯拼出下一轮请求（5.4 的多轮定律用） |
| `essence.go` | 每种 `LossKind` 对语义摘要的作用 |
| `script*.go` | 场景脚本的类型和加载 |
| `converter.go` | `Converter` 接口，让旧 bridge 和新引擎都能被同一套矩阵测试驱动（阶段 8 删除） |
| `doc.go` | 包说明（`testkit` 在阶段 0 已超过 15 个手写源文件，按 `layout.go` 门禁必须提供） |
| `mockupstream/` | 脚本化上游，一个 `http.Handler`（5.2.2）。单独成包，因为它依赖 `convert` |
| `cmd/corpus/main.go` | 语料导入和检查的命令行工具（位于 `internal/` 下，日志输出用 `fmt.Fprintln(os.Stderr, ...)` 或 `log/slog`，不导入标准库 `log`） |

校验器和客户端模拟器故意不经过编解码器：它们是独立写的第二份协议知识，用来检查编解码器，不能和被检查的代码共用实现。

#### 5.2.1 严格请求校验器

脚本化上游和 T3 都用它判断"上游会不会接受这个请求"。每条规则有编号、说明和出处（官方文档链接或提交、issue 编号）。初始规则集，实施时逐条对照官方文档核实：

| 协议 | 规则 |
| --- | --- |
| Messages | 每个 `tool_use` 在紧随的 user 消息里有同 id 的 `tool_result`，反之亦然；`tool_result` 块位于 user 内容的最前面；启用 thinking 时 `thinking` 块带非空 `signature`，且位于同条 assistant 消息的 `tool_use` 之前；`max_tokens` 为正，`budget_tokens` 小于 `max_tokens`；没有空的 text 块；工具名符合字符集和长度限制；严格模式下拒绝未知的顶层字段 |
| Responses | 每个调用输出条目有同 `call_id` 的调用条目，反之亦然（Codex 测试套件的 `validate_request_body_invariants`）；`reasoning` 条目后面跟着它所属的条目；函数名符合 `^[a-zA-Z0-9_-]{1,64}$` |
| Chat | `tool` 消息前面有带相同 `tool_calls[].id` 的 assistant 消息，下一条 user 或 assistant 消息之前全部调用都有结果；assistant 消息有 `content` 或 `tool_calls`；`arguments` 是合法 JSON；函数名符合 `^[a-zA-Z0-9_-]{1,64}$`；方言规则 `deepseek-thinking`：带 `tool_calls` 的 assistant 消息必须有 `reasoning_content` |
| Gemini | `functionResponse` 对应前一个 model 回合里同名的 `functionCall`；方言规则 `gemini-signature`：model 回合里每个步骤的第一个 `functionCall` 带 `thoughtSignature`；schema 不含该方言不支持的关键字；函数名符合官方限制 |

线上出现新的上游 400 时，流程是：加一条规则和一个复现夹具，再修代码。规则集因此逐步积累成可执行的提供商知识。

#### 5.2.2 脚本化上游

```go
type Script struct {
	Name    string
	Turns   []Turn
	Default Reply // reply for requests that match no turn, for example client side requests
}

type Turn struct {
	Match  Match   // how to recognize this turn, see below
	Expect []Check // predicates on the decoded upstream request
	Reply  Reply
	Fault  *Fault  // optional: HTTP status, truncate after N frames, delay
}

type Reply struct {
	Events []ir.Event // synthesized through the upstream protocol's response encoder
	Raw    string     // or: name of a recorded raw stream in the corpus, replayed verbatim
}
```

行为：

- 按请求路径识别协议：`/v1/messages`、`/v1/responses`、`/v1/chat/completions`、`/v1beta/models/{model}:generateContent` 和 `:streamGenerateContent`。
- 每个请求先过严格校验器。有违规时返回该提供商风格的 400，并把违规记进日志。测试结束时断言日志里没有预期之外的违规。
- 用该协议的请求解码器解成 IR，按 `Match` 找回合。`Match` 用历史里 assistant 消息和工具结果的数量定位回合，不用全局计数器。真实客户端会发旁路请求（例如 Claude Code 用小模型做的标题生成和后台请求），这些请求匹配不到任何回合，走 `Default`。
- 回复有两种来源：IR 事件经该协议的响应编码器合成，或者把语料库里录制的原始流逐字节重放。有录制时优先用录制。
- 记录收到的全部请求，供测试断言。

脚本化上游另有两种模式，供 T6 使用：`proxy-record`（转发到真实提供商并记录双向原始字节）和 `replay`（按回合序号重放录制的流）。

### 5.3 语料库

目录布局，位于 `backend/internal/protocol/testkit/testdata/`：

```text
scenarios/                 协议无关的场景脚本（IR JSON）
wire/<protocol>/request/   手写的请求夹具
wire/<protocol>/stream/    手写的 SSE 流夹具
wire/<protocol>/response/  手写的非流式响应夹具
corpus/clients/<client>/<version>/<scenario>/<n>.request.json    录制的真实客户端请求
corpus/upstreams/<provider>/<model>/<scenario>/<n>.stream.sse    录制的真实上游流
golden/ir/        解码结果的 IR 快照
golden/loss/      每个夹具乘路线的 Loss 报告
golden/census/    未建模字段普查
golden/wire/      规范场景集的编码输出
legacy_baseline.json   旧实现在各场景乘路线上的通过情况（阶段 8 删除）
diff_ledger.json       新旧差分里已接受的差异（阶段 8 删除）
```

路线清单另放在 `internal/app/testdata/golden/routes.txt`（6.1 节第 7 步），由路线表生成。

每个录制目录带 `meta.json`：协议、客户端或提供商、版本、采集日期、是否流式、备注。

采集方式：语料只来自客户端实验室。T6 的 `record` 开关把网关收到的客户端请求和脚本化上游（或 `proxy-record` 模式的真实上游）的双向字节写到 `testdata/corpus/_incoming/`，再用 `cmd/corpus import` 整理入库。不在生产代码里加流量采集功能，也不从线上用户流量取样。阶段 0 把 `backend/internal/protocol/testkit/testdata/corpus/_incoming/` 加入 `.gitignore`（防止未脱敏的原始捕获进入 `git status` 或快检），并在 `.gitattributes` 里给 `backend/internal/protocol/testkit/testdata/**/*.sse` 标 `-text -whitespace`（允许 `upstream_sse_quirks` 与录制流保留 CRLF 和行尾空格而不触发 `git diff --check` 失败）。`testdata/` 下不放 `.go` 文件。

`cmd/corpus import` 的清洗规则：不保存请求头，只在 `meta.json` 里留 `user-agent`、`anthropic-beta`、`anthropic-version`、`openai-beta`；用占位符替换 API Key、邮箱、用户主目录路径（正则表）；把 base64 媒体替换成小的合成图片；单个文件超过 512 KB 时拒绝入库，要求裁剪历史。`cmd/corpus verify` 检查入库文件不含密钥特征，语料总量不超过 15 MB。

每个受支持的客户端版本至少录一轮完整的场景集。客户端升级版本时重录，旧版本的语料保留。

### 5.4 定律

T2 到 T4 的主体是数据驱动的定律测试：同一个测试函数遍历全部适用的夹具。记 `Dec_P`、`Enc_P` 为协议 P 的解码和编码，`Agg` 为事件流聚合。

请求方向：

| 编号 | 定律 | 抓什么 |
| --- | --- | --- |
| Q1 | 每个 P 的请求夹具都能解码，结果通过 `ir.Validate` | 解码器的覆盖面 |
| Q2 | 令 `y = Enc_P(Dec_P(x))`，则 `Dec_P(y)` 等于 `Dec_P(x)`，且 `Enc_P(Dec_P(y))` 与 `y` 逐字节相等 | 同协议往返稳定、编码确定 |
| Q3 | 对每条路线 P→Q：`Convert(x)` 通过 Q 的严格请求校验器 | 上游会拒绝的请求 |
| Q4 | 对每条路线 P→Q：把编码器上报的每条 Loss 作用到 `Essence(Dec_P(x))` 上，结果等于 `Essence(Dec_Q(Convert(x)))` | 没有上报的丢失和改动 |
| Q5 | 同一输入转换两次，输出逐字节相等 | 不确定性（map 顺序、随机 id） |

Q4 是这套体系的核心。语义摘要是与协议无关的结构：按顺序列出每条消息的角色和每个 part 的种类与规范化内容（文本、工具调用的名字和规范化 JSON 参数、调用 id、媒体的 MIME 和内容哈希、重放令牌打开信封后的方案和原文），加上工具声明（名字、schema 的规范化哈希）、系统指令文本、采样和推理参数。每种 `LossKind` 在 `testkit/essence.go` 里有一个很小的独立实现，说明它允许摘要发生什么变化。源和目标的摘要之间出现任何 Loss 解释不了的差异，测试就失败。这样不需要为每条路线手写期望输出，同时保证"编码器说丢了什么，就只丢了什么"。

响应方向：

| 编号 | 定律 | 抓什么 |
| --- | --- | --- |
| S1 | 每个 Q 的流夹具解码后通过 `ir.ValidateStream` | 解码器对真实上游流的处理 |
| S2 | 有成对的流式和非流式夹具时，`Agg(DecStream_Q(s))` 的摘要等于 `DecResponse_Q(r)` 的摘要 | 流式和非流式解码不一致 |
| S3 | 对每个客户端协议 P：`EncStream_P(events)` 通过 P 的流语法校验器 | 事件顺序、必填字段、索引 |
| S4 | `Agg(DecStream_P(EncStream_P(E)))` 的摘要等于把上报的 Loss 作用到 `Agg(E)` 摘要后的结果 | 响应方向没有上报的丢失 |
| S5 | 把上游流按任意字节边界重新切分后喂给 SSE 读取器，或者把相邻文本增量合并、拆分后喂给解码器，聚合结果不变 | 分块边界、UTF-8 截断 |
| S6 | 流式编码的聚合结果等于非流式编码的结果 | 流式和非流式编码不一致 |
| S7 | 在上游事件流的每个事件边界截断并 `Close(err)`，客户端输出仍通过语法校验器的"异常终止"模式，不 panic | 断流后的悬空块、缺终止事件 |
| S8 | 客户端可见的用量由 `ir.Usage` 按 3.3 的口径推出，三个输入桶只相加一次 | 用量重复计数 |

多轮重放定律（针对 1.3 的第一类缺陷）：

| 编号 | 定律 |
| --- | --- |
| M1 | 取场景的第一轮：客户端请求经路线 P→Q 转换，上游流经 Q→P 转换成客户端响应。用 P 的客户端模拟器把这个响应和工具结果拼成第二轮请求，再经路线 P→Q' 转换。结果必须通过 Q' 的严格请求校验器，包括方言规则。Q' 遍历所有受支持的上游协议，包括与 Q 不同的情况（两轮之间切了提供商），以及 Q' 等于 P、第二轮经 `PrepareNativeReplay` 走原生路径的情况 |

客户端模拟器对每个协议有若干变体，对应真实客户端的不同习惯，例如 Chat 客户端有"保留 `reasoning_content`"和"丢弃未知字段"两种，Responses 客户端有"保留全部输出条目"和"非 OpenAI 提供商时剥掉 `encrypted_content`"两种。变体的依据来自 T6 录到的真实请求。

fuzz（Go 原生 `testing.F`）：每个解码器对任意字节输入不 panic；SSE 读取器对任意切分不 panic。种子取自语料库。CI 里只跑种子，本地和每晚任务跑定时 fuzz。

golden 用得克制：IR 快照、Loss 报告、普查、规范场景集的线输出。其余靠定律。

### 5.5 场景目录

场景用 IR 写一次，每个场景自动展开到所有适用的路线。初始场景：

| 场景 | 内容 |
| --- | --- |
| `text_basic` | 单轮文本，流式和非流式 |
| `text_unicode_split` | 多字节字符和 emoji 被增量边界切开 |
| `system_roles` | system 和 developer 指令，包括对话中间的 |
| `tool_single` | 一次工具调用和结果，两轮 |
| `tool_parallel` | 一轮里两个并行调用 |
| `tool_parallel_interleaved` | 上游把两个调用的参数增量交错发出（仅 Chat 上游） |
| `tool_args_fragmented` | 参数被切成很多小片，包括在转义序列中间切开 |
| `tool_args_empty` | 无参数调用，参数为空串和 `{}` 两种 |
| `tool_result_image` | 工具结果带图片 |
| `tool_result_error` | 工具结果标记为错误 |
| `tool_ids_missing` | 上游的调用没有 id 或后续增量带空 id、空 name |
| `reasoning_summary` | 推理摘要加最终文本 |
| `reasoning_tool_replay` | 推理、工具调用、下一轮带着推理历史回来 |
| `reasoning_token_foreign` | 历史里的重放令牌来自另一种方案 |
| `reasoning_carrier_roundtrip` | 转换路线输出的信封在下一轮回到同方案、另一方案和要求明文推理的上游 |
| `reasoning_carrier_native` | 带信封的历史下一轮走原生 Messages 或 Responses 路径：方案匹配时还原，不匹配时删除，必要时降级 thinking |
| `reasoning_encrypted_only` | Responses 客户端只回传 `encrypted_content`（`plain` 信封），下一轮发往要求 `reasoning_content` 的 Chat 上游 |
| `legacy_history` | 升级前的会话：客户端历史里有旧引擎写下的裸令牌（Messages `signature` 里的 `encrypted_content`）和空签名。请求仍通过严格请求校验器，每个丢弃项都有 Loss |
| `tools_custom` | Responses 的 custom 自由文本工具（Codex 的 `apply_patch`） |
| `tools_namespace` | 命名空间工具，包括拍平后超过 64 字节的名字 |
| `tools_search` | tool_search 及其发现的工具 |
| `tools_hosted_search` | 托管网页搜索工具的声明和结果 |
| `image_input` | base64 和 URL 两种图片输入 |
| `document_input` | PDF 输入 |
| `structured_output` | json_schema 输出格式 |
| `stop_sequence`、`max_tokens` | 两种截断的终止原因 |
| `refusal` | 拒答 |
| `usage_cache` | 带缓存读取和 5m、1h 写入的用量 |
| `stream_truncated` | 上游在各个位置断流 |
| `stream_error_event` | 上游在流中发错误事件 |
| `upstream_sse_quirks` | 只有 data 行、多行 data、CRLF、结尾没有空行、重发累计内容 |
| `empty_response` | 上游返回空内容 |

每个场景文件声明它需要的能力，不适用的路线自动跳过并在测试输出里列出。

### 5.6 T5：网关路由测试

基础是现有的 `internal/app/sse_keepalive_routes_scenario_test.go` 里的 `newHeartbeatRouteRouter`。它调用生产的 `RegisterGatewayRoutes`，用真实的 `httpclient.UpstreamPool` 连到 `httptest.Server`，只替换了认证、持久化存储和定时器。

改造：

1. 把 `newHeartbeatRouteRouter` 泛化成 `newProtocolLabRouter(t, labOptions)`，放进现有的 `internal/app/helpers_test.go`（`newHeartbeatRouteRouter` 保留为调用它的薄包装，兼容 `sse_keepalive_routes_scenario_test.go` 和 `gemini_sdk_scenario_test.go` 的既有调用点）。`labOptions` 可以声明多个提供商（平台、类型、上游地址、模型、启用的协议）和分组的 `protocol_fallbacks`，以及是否打开接线开关（2.4 节，通过在 7 组执行器与运行时的 `Options` 结构体上设置 `UseIRConversion: true` 生效，不在 Wire 中注册裸 `bool`；阶段 8 删除该字段）。路由装配补齐真实的 `ModelsHandler`、`CountTokensHandler`、`OpenAITokens`、`MessagesHandler`（覆盖 `/antigravity/v1/messages`）、`Antigravity`、`Qoder` 以及内存态 `session.GatewayCache`（现有 `newHeartbeatRouteRouter` 将这些入口留成 `nil`，直接请求会空指针 panic）。
2. 新增 `internal/app/protocol_lab_scenario_test.go`。对每个"客户端协议乘提供商种类"的组合和每个场景：启动脚本化上游，用 Go 的 `http.Client` 按客户端协议发请求，断言：
   - 客户端收到的流通过该协议的语法校验器；
   - 脚本化上游的日志里没有校验违规；
   - 客户端可见的用量与记录存储里结算的用量一致（按 3.3 的口径换算后比较）；
   - 上游在首字节前返回可切换错误时，请求切到第二个提供商并成功，客户端流没有混入第一个上游的内容；
   - 上游在输出开始后断流时，客户端收到该协议格式的错误事件，用量仍然被记录；
   - 错误响应是客户端协议的格式；
   - 同一分组里第一轮走转换路线、第二轮落到原生路径（以及反过来）时，第二轮请求通过上游的校验，信封按 3.10 节还原或删除。
   阶段 8 之前，每个组合对旧路径和新路径各跑一遍：旧路径的结果与 `legacy_baseline.json` 比较，新路径必须全绿。
3. 提供商种类至少覆盖：`anthropic`（Messages 上游）、`openai`（Responses 上游）、`openai_chat`（Chat 上游）、`gemini`（Gemini 上游），以及 Kimi 或 DeepSeek 的 Anthropic 端点（Messages 上游，兼容平台路径）。Qoder 和 Antigravity 的认证和会话较特殊，在各自阶段再加。

可选的 SDK 驱动：沿用 `tools/gemini_sdk` 的做法，在 `tools/clientlab/sdk/` 放官方 OpenAI、Anthropic SDK 的小脚本，由环境变量开启，验证官方 SDK 的流累加器能解析网关输出。这一项排在真实客户端之后，可以后补。

### 5.7 T6：客户端实验室

形态（已定）：真实客户端作为子进程，连到进程内的网关（`httptest.NewServer(newProtocolLabRouter(...))`），网关的上游是脚本化上游。不需要 Docker、数据库和真实服务器。测试代码在 `internal/app/client_lab_scenario_test.go`，客户端的版本锁定和配置模板在 `tools/clientlab/`。

客户端和路线矩阵：

| 客户端 | 客户端协议 | 要覆盖的上游协议 |
| --- | --- | --- |
| Codex CLI | Responses（HTTP） | Chat、Messages、Gemini，以及 Responses 原生作为对照 |
| Claude Code | Messages | Responses、Chat、Gemini，以及 Messages 原生作为对照 |
| pi | 每个模型可配 `api`：`openai-completions`、`openai-responses`、`anthropic-messages` | 每种客户端协议乘其余三种上游，加原生对照 |

原生对照组的意义：透传时客户端直接解析脚本化上游合成的流。对照组失败说明脚本化上游或该协议的响应编码器有问题，这是用真实客户端校验编码器的途径。

pi 一个客户端能覆盖三种客户端协议，优先接入。pi 的 `google-generative-ai` 模式只用来做 Gemini 原生对照，因为 Gemini 客户端目前没有转换路线。可选扩展：OpenCode、Gemini CLI（只做原生对照）、Codex 的 WebSocket 传输。

客户端的启动方式（调研结论，实施时用对应版本核对）：

- Codex：临时的 `CODEX_HOME`，其中 `config.toml` 定义 `[model_providers.lab]`（`base_url` 指向实验室网关的 `/v1`，`env_key`，`wire_api = "responses"`，`supports_websockets = false`，重试次数设为 0），并关闭 analytics 和更新检查。命令是 `codex exec --json --skip-git-repo-check --ephemeral -C <工作目录> "<提示>"`，加上允许无人值守执行工具的参数。Codex 只支持 Responses 线协议。它启动时还会向网关请求 `GET {base}/models`，实验室路由必须挂载真实的 `ModelsHandler`（现有夹具里 `endpoints.Models` 为 `nil` 会触发 panic；Claude Code 可能调用的 `CountTokensHandler` 同理一并挂载）。`tools/clientlab/` 下的模板目录避开 `.gitignore` 中未锚定的 `CLAUDE.md`、`.claude`、`tests`、`scripts`、`output`、`tmp` 命名。
- Claude Code：环境变量 `ANTHROPIC_BASE_URL`、`ANTHROPIC_API_KEY`、`ANTHROPIC_MODEL`、`ANTHROPIC_DEFAULT_HAIKU_MODEL`、`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`、`DISABLE_AUTOUPDATER=1`、临时的 `CLAUDE_CONFIG_DIR`。命令是 `claude -p "<提示>" --output-format stream-json --verbose --no-session-persistence`，加上允许无人值守执行工具的权限模式。跑两个配置：`--bare`（请求面最小）和默认（带完整的 beta 头、`cache_control`、`context_management` 等）。
- pi：包名已迁到 `@earendil-works/pi-coding-agent`。用 `PI_CODING_AGENT_DIR` 指向临时目录，其中 `models.json` 定义一个提供商和若干模型，每个模型指定 `api` 和 `baseUrl`。命令是 `pi -p --mode json --model lab/<模型> --no-session --offline --no-extensions --no-skills --no-context-files "<提示>"`。JSON 模式下失败不一定体现在退出码上，要断言事件流。

安全边界（已定）：

- 脚本模式下，"模型"让客户端执行的命令全部来自我们写的脚本（例如把一个随机串写进工作目录里的文件），工作目录是 `t.TempDir()`。这种模式可以在开发机和 CI 上直接跑，客户端使用跳过审批的参数（Codex 的 `--dangerously-bypass-approvals-and-sandbox`、Claude Code 的 `--permission-mode bypassPermissions`）。
- live 模式下命令由真实模型决定，只允许在一次性容器里运行。测试在 live 模式下检查环境变量 `TOKENROUTER_CLIENTLAB_CONTAINER=1`，没有就拒绝启动；这个变量只由 `tools/clientlab/Dockerfile` 的入口设置。

T6 的场景（每个场景对矩阵里每个格子各跑一次）：

| 场景 | 脚本 | 断言 |
| --- | --- | --- |
| `lab_text` | 一轮文本回复，含一个随机串 | 客户端退出码为 0，最终消息含随机串 |
| `lab_tool` | 第一轮让客户端用它的 shell 工具把随机串写进 `result.txt`，第二轮在收到工具结果后回复完成 | 文件内容正确；客户端事件流里有一次工具执行且没有错误事件；上游日志显示第二轮请求带着工具结果，没有校验违规 |
| `lab_tool_parallel` | 一轮里两个工具调用 | 两个文件都写入 |
| `lab_reasoning_tool` | 第一轮发推理（带该上游方案的重放令牌）加工具调用 | 第二轮请求通过上游的方言规则（例如 DeepSeek 的 `reasoning_content`、Gemini 的签名） |
| `lab_switch_provider` | 两轮之间把分组的提供商从上游协议 A 换成 B | 第二轮请求通过 B 的校验 |
| `lab_failover` | 第一个提供商在首字节前返回 529，第二个正常 | 客户端无感知地成功 |
| `lab_truncated` | 上游在输出中途断开 | 客户端报告错误或按自身策略重试，不挂起，不崩溃 |
| `lab_custom_tool` | 仅 Codex：让模型用 `apply_patch` 自由文本工具改一个文件 | 文件被修改（验证 custom 工具的降级和还原能被真实客户端解析） |
| `lab_long_stream` | 很长的文本加大参数的工具调用 | 内容完整 |

每个场景还断言：网关没有 5xx，每个请求只结算一次用量。

运行模式，由环境变量选择：

| 模式 | 上游 | 用途 |
| --- | --- | --- |
| `scripted`（默认） | 脚本化上游 | CI 和日常开发，确定、无网络 |
| `record` | 同上，另外把客户端请求和上游往返写到 `_incoming/` | 采集真实客户端的请求进语料库 |
| `live` | `proxy-record` 模式，转发到真实提供商 | 发布前的金丝雀；采集真实上游的流。只断言结构性结果（任务完成、无协议错误） |

live 模式是唯一能验证"真实提供商接受转换后的请求"的手段，费用和不确定性都高，不进常规 CI，由维护者在发布前和客户端大版本更新后手动跑。阶段 7 的 live 金丝雀是切换的前置条件。

阶段 8 之前，T6 的转换格子通过接线开关跑新路径。旧路径的 T6 成本高，只在阶段 0 和阶段 7 各跑一次，记录基线。

### 5.8 CI 和 Make 目标

- `make test-go` 自动包含 T1 到 T5。
- 新增 `make test-clientlab`：`cd tools/clientlab && npm ci`，然后 `TOKENROUTER_CLIENTLAB=1 go -C backend test ./internal/app -run '^TestClientLab' -count=1 -timeout 30m -v`。
- 新增 `.github/workflows/protocol-lab.yml`：
  - 触发条件：PR 改动了 `backend/internal/protocol/**`、`backend/internal/gateway/**`、`backend/internal/upstream/**`、`backend/internal/app/*lab*`、`tools/clientlab/**`；每晚定时；手动触发（可选客户端版本）。
  - 两个 job：`pinned` 用 `tools/clientlab/package-lock.json` 锁定的版本；`latest`（只在定时和手动触发时跑）安装各客户端的最新版，用来尽早发现新版客户端带来的不兼容。`latest` 失败不阻塞合并。
  - 阶段 8 之前，CI 里的 T6 只跑新路径；T5 的旧路径和新路径都在 `make test-go` 里自动运行。
- `tools/check_changed.py` 的快检不跑 T6。
- `tools/clientlab/package.json` 锁定三个客户端的确切版本。调研时的最新版本是 `@openai/codex` 0.169.0、`@anthropic-ai/claude-code` 2.1.323、`@earendil-works/pi-coding-agent` 1.5.2，实施时取当时的稳定版。

### 5.9 缺陷回归流程

写进 `docs/operations/development_workflow.md` 的验证策略：

1. 线上或 T6 发现转换问题后，先把触发它的请求或上游流整理成夹具：能用场景脚本表达的进 `scenarios/`，依赖真实报文细节的进 `corpus/`（经过清洗）。
2. 如果问题是上游拒绝了请求，在严格请求校验器里加规则；如果是客户端解析失败，在流语法校验器里加规则。
3. 确认现有定律在新夹具上失败，再修代码。
4. 不为单个缺陷写一次性的内联 JSON 断言，除非它确实无法用夹具加定律表达。

## 6. 实施阶段

每个阶段结束时都要：运行 `make fmt`、`make fmt-check`、`make lint-go`、`make test-go`、`make check-docs`；涉及 `tools/architecture` 时按 `development_workflow.md` 的要求用无标签和 integration 两个集合验证正反例；按 `project-doc` 评估文档影响；按 Conventional Commits 分步提交。阶段 0 到 7 的提交不能改变生产行为（2.4 节）；阶段 2 起，每次提交前确认"生产装配下接线开关关闭"的测试通过。

### 6.1 阶段 0：测试地基和基线

目标：不改生产行为，先把测试设施建起来，并用它给旧实现画一条基线。这一步同时验证测试设施本身。

步骤：

1. 架构规则与仓库配置（`tools/architecture/`、`.gitignore`、`.gitattributes`）：
   - `catalog.go` 的 `pureStandard` 加 `encoding/json/v2`、`encoding/json/jsontext`。
   - `leafDependencies` 按 3.2 的表为本阶段新建的包（`internal/protocol/sse`、`internal/protocol/ir`、`internal/protocol/testkit`、`internal/protocol/testkit/mockupstream`、`internal/protocol/testkit/cmd/corpus`）加条目，每个条目的 `Production` 都包含包自身路径。
   - `policy.go`：把 `fixture` 分支整体移到 `pure` 分支（包含 `within(dir, "internal/protocol")` 和 `l.leaf != ""` 条件）之前，确保 `internal/protocol/testkit` 及其子目录得到 `fixture` 角色并可使用 `os`、`embed`、`net/http`。
   - 为 `internal/protocol/bridge` 的 `Tests` 登记对 `internal/protocol/testkit` 的依赖（`"internal/app"` 属于 `assembly` 角色，已允许导入任意内部包，不要把 `"internal/app"` 加进 `moduleDependencies` 或尚无消费方的 `filePermissions`）。
   - `.gitignore` 加入 `backend/internal/protocol/testkit/testdata/corpus/_incoming/`；`.gitattributes` 为 `backend/internal/protocol/testkit/testdata/**/*.sse` 标 `-text -whitespace`。
   - 按现有惯例补正反例夹具。
2. `protocol/sse`：`Frame{Event string; Data []byte; Semantic, Terminal bool}`；`Reader`（增量解析，处理只有 data 行、多行 data、CRLF 与 LF 混用、注释行、EOF 处没有空行的最后一帧、单帧大小上限）；`Append(dst []byte, f Frame) []byte`。行为对照 `gateway/forward/conversion_sse.go` 和 `22235a63c` 的修复。测试包含任意切分的 fuzz。
3. `protocol/testkit` 第一版：语料加载、golden（使用环境变量 `TOKENROUTER_UPDATE_GOLDEN=1` 控制写回）、jsondiff、四个协议的流语法校验器和严格请求校验器、客户端模拟器、`Converter` 接口、脚本类型，以及 `doc.go`（本包手写源文件已超过 15 个）。`cmd/corpus/main.go` 不用标准库 `log`。`ir` 包在这一阶段先建出 `testkit` 用到的类型（`Event`、`Loss` 等类型定义先行，行为函数留到阶段 1）。
4. `protocol/testkit/mockupstream` 第一版：这一阶段还没有编解码器，回复只支持 `Raw`（手写或录制的线字节），回合匹配用按协议的轻量 JSON 检查实现（数历史里的工具结果条数）。阶段 1 之后改为把请求解码成 IR 再匹配，并支持用 IR 事件合成回复。源文件命名避开 `mock_upstream_*.go` 前缀重复。
5. 夹具：为 5.5 的每个场景手写四个协议的线夹具（请求、流、非流式响应），放进 `wire/`。数量大，按场景分批提交。
6. 旧实现的适配器和基线：在 `protocol/bridge` 里新增 `conversion_matrix_scenario_test.go`（文件开头注释列出覆盖的源文件），用测试内的适配器把旧转换函数包成 `testkit.Converter`，覆盖 Messages、Responses、Chat 之间的六条路线，以及 Messages 到 Gemini 的请求和 Gemini 到 Messages 的流。对每个场景乘路线跑定律 Q3、S3、S7，把通过情况写进 `legacy_baseline.json`。失败项逐个判断是旧实现的缺陷还是校验器规则写错了；校验器的问题当场修，旧实现的缺陷只记录，不在本阶段修。这个测试一直运行到阶段 8，结果与 `legacy_baseline.json` 不一致时失败，旧引擎的线上修复因此会提醒更新基线（2.4 节）。
7. 路线清单：在 `internal/app` 的测试里遍历平台、提供商类型和客户端协议，用 `capability.ProtocolFallbackTargets` 和 `SupportsProtocolConversion` 生成"客户端协议、平台、提供商类型、上游协议"的转换路线清单（包含 Anthropic `bedrock` 的转换路线，在阶段 2 补齐执行适配），加上不经过路线表的转换用途（Responses 同协议客户端工具降级、`/v1/chat/completions` 上 Cursor 的 `input` 形状请求、`count_tokens` 到 Gemini 和 OpenAI、输入 token 估算、Responses 请求体里旧式 `messages` 的改写），写成 golden `internal/app/testdata/golden/routes.txt`。路线表以后有增减时这个 golden 会变。阶段 7 按它逐行核对覆盖。
8. T5：按 5.6 泛化路由夹具（补齐 `ModelsHandler`、`CountTokensHandler`、`OpenAITokens`、`MessagesHandler` 和内存 `GatewayCache`），新增 `protocol_lab_scenario_test.go`，在旧引擎上跑通，失败项同样记入基线。
9. T6 骨架：`tools/clientlab/`（`package.json`、锁文件、各客户端的配置模板、`README.md`、live 模式用的 `Dockerfile`）；`client_lab_scenario_test.go` 实现三个客户端的驱动和 `lab_text`、`lab_tool` 两个场景；原生对照组先跑通，再跑转换路线，在旧引擎上记录基线。用 `record` 模式采集第一批真实客户端请求，经 `cmd/corpus import` 入库。
10. CI：新增 `protocol-lab.yml`，`Makefile` 加 `test-clientlab`。
11. 文档：`development_workflow.md` 的测试分层加入 T1 到 T6 的说明、运行方法和长期有效的缺陷回归流程（5.9 节，不把开发期临时规则写进长期文档）；`backend_modules.md` 的包地图补齐本阶段新建的全部 5 个包（`protocol/sse`、`protocol/ir`、`protocol/testkit`、`protocol/testkit/mockupstream`、`protocol/testkit/cmd/corpus`）。

验收：

- `make lint-go`、`make test-go` 通过。
- `legacy_baseline.json` 入库，每个失败项有一行原因。
- `routes.txt` 入库，人工核对过它与 3.13 两张表的上游族一致。
- 本机 `make test-clientlab` 能跑完三个客户端的原生对照组。
- `corpus/clients/` 下有三个客户端各至少一个场景的录制请求。

### 6.2 阶段 1：IR、三个文本编解码器、转换引擎

目标：实现 `ir`、`codec/messages`、`codec/responses`、`codec/chat`、`convert`，全部是纯代码，不接入网关。推理连续性直接按 3.10 节的最终方案实现，包括信封、`CallMemory` 接口和 `PrepareNativeReplay`。

#### 6.2.1 `ir`

文件：`request.go`（含 `Extra` 及其 `IsZero`）、`response.go`、`event.go`、`usage.go`（含与 `protocol.TokenUsage` 的换算辅助，保持有效代码不少于 30 行）、`loss.go`、`validate.go`、`accumulator.go`、`sequencer.go`、`token.go`（重放令牌和信封的 `FormatCarrier`、`ParseCarrier`）、`essence.go`、`link.go`。各有对应的 `_test.go`，需导入 `testkit` 的外部测试放在 `essence_external_test.go` 或带 `package ir_test` 的 `*_scenario_test.go`。`token_test.go` 覆盖信封往返、六种方案、可选提供商 ID `p`、非法载荷和普通字符串（包括以 `gAAAA` 开头的裸令牌）都不被当成信封。

#### 6.2.2 编解码器

每个编解码器包的文件：`wire.go`（私有线结构体）、`request_decode.go`、`request_encode.go`、`response.go`（非流式的解码和编码）、`stream_decode.go`、`stream_encode.go`、`usage.go`、`finish.go`，必要时 `legalize.go`。按第 4 节的映射实现，避开重复包名前缀的文件名。`FuzzStreamDecode` 放进 `stream_decode_test.go`，跨多个源文件的 T2 定律测试放进 `conformance_scenario_test.go`（不单独建无配对源文件的 `fuzz_test.go` 或 `laws_test.go`）。实现顺序：Chat（最简单），Messages，Responses。

每完成一个编解码器，跑通 T2：Q1、Q2、Q5、S1、S2、S3、S6、S7、fuzz，以及该协议夹具的 IR golden。

#### 6.2.3 `convert`

文件：`exchange.go`、`stream.go`、`buffer.go`、`registry.go`、`dialect.go`、`tools_lower.go`、`tools_raise.go`、`tools_map.go`、`tools_responses.go`（`NeedsResponsesToolAdaptation`、`AdaptResponsesTools`、`RestoreResponsesTools`、`NewResponsesToolStream`）、`carriers.go`（`CallMemory`、`CallBinding`、`openCarriers`、`selectReplayTokens`、`sealCarriers`）、`native_replay.go`（`PrepareNativeReplay`）、`markers.go`、`output_meaning.go`（直接按结构化事件与编码器状态计算 `Semantic` 和 `Terminal`），达到 15 个源文件时加 `doc.go`。

工具降级的移植：从 3.11 列出的旧文件逐函数移植到 IR 变换与同协议工具适配，命名算法先原样复制。工具名差分对语料库里所有带 custom、namespace、tool_search 工具的请求，比较新旧两边发给上游的工具名集合和每个历史调用的名字，结果写进差分报告；不同的名字归入 D8，并检查 3.11 的命名要求。

`PrepareNativeReplay` 的测试：语料库里每个 Messages、Responses 请求（包括正文中含有字面量 `"trc1:"` 但令牌路径上无信封的请求，以及带无效 UTF-8 字节的请求）经过它之后逐字节不变；`reasoning_carrier_native` 场景覆盖同方案还原、异方案/异账号删除、空 assistant 内容占位、`passback` 保留明文 thinking、Responses 保留明文 `summary`、WS `response.create` / `session.update` 帧，以及 thinking 降级。

跑通 T3 和 T4：Q3、Q4、S4、S5、S8、M1，覆盖三个协议之间的六条路线。M1 包括跨方案的组合：两轮的上游方案不同、第二轮经 `PrepareNativeReplay` 走原生路径、客户端历史来自升级前（`legacy_history`）。

#### 6.2.4 与旧实现的差分和验收差异清单

在 `convert` 的测试里（`legacy_diff_scenario_test.go`），对每个场景和语料夹具乘路线：

- 请求：新旧输出各自用目标协议的解码器解成 IR，比较语义摘要；再对原始 JSON 做语义比较。
- 流：新旧输出的客户端流各自解码聚合，比较摘要；逐帧的 `Semantic`、`Terminal` 标记序列比较。
- 用量：新流解码器的 `ir.Usage` 换算成 `protocol.TokenUsage` 后，与旧读取循环使用的累加器在同一条上游流上的结果比较。
- 工具名：新旧两边发给上游的工具名和历史调用名逐个比较。

路线范围随阶段扩大：阶段 1 是三个文本协议之间的六条路线，阶段 3 加 Gemini，阶段 4 加 Antigravity，阶段 5 加同协议客户端工具轻量适配，阶段 6 加 Qoder。

每个差异归入以下之一：

1. 新实现的缺陷：修。
2. 验收差异清单里的类别：记入 `diff_ledger.json`，写明类别和夹具。
3. 其他差异：不允许自行接受。向旧行为对齐；确实认为旧行为是缺陷的，记入进度记录的"待确认差异"并询问用户。用户同意接受时，作为新类别加进下表，写明接受条件。

验收差异清单。表里是有意接受的全部差异，阶段 7 逐行核对接受条件，这是切换的门槛之一：

| 编号 | 有意接受的差异 | 接受条件 |
| --- | --- | --- |
| D1 | 旧实现重排了 part（例如把一条 assistant 消息里的推理、文本、调用重新分组，或把工具结果统一提前），新实现保持原顺序 | 新输出通过目标协议的严格请求校验器或流语法校验器 |
| D2 | 旧实现串联经过中间协议时丢了字段，新实现直接转换后保留 | 保留的字段在目标协议里合法。Responses、Chat 到 Gemini 原来经过 Messages 串联，这类差异会比较多，逐条记账 |
| D3 | 旧实现的输出违反目标协议的语法校验器或严格请求校验器，或旧实现漏判输出帧的 `Semantic`、`Terminal` 标记（如 custom/search 工具、refusal、`incomplete`/`failed`/`error` 终态）及忽略客户端 `IncludeUsage` 开关，新实现符合规范 | 对应的校验规则或协议规范有出处 |
| D4 | 旧实现在请求方向用随机 id，新实现用内容派生的 id | 定律 Q5 通过 |
| D5 | 旧实现直接丢弃的未知块，现在作为 `OpaqueItem` 丢弃并上报 | 发往上游的内容不变，只多了 Loss 记录 |
| D6 | 跨协议转换路线写给客户端的令牌字段改成信封（Messages 的 `signature` 和 `redacted_thinking.data`、Responses 的 `encrypted_content`，`openai` 和 `xai` 附带提供商 ID `p`）；Gemini 推理签名和没有令牌的明文推理也以信封写给 Messages、Responses 客户端，旧实现留空或不写 | 客户端可见的文本、工具调用和用量不变；T6 的 Claude Code、Codex、pi 能解析并在下一轮原样带回；`reasoning_carrier_roundtrip` 通过 |
| D7 | 发往上游的令牌按 3.10 节取舍：不再按 `gAAAA` 前缀判断，Messages 客户端历史里的裸签名不再写进 Responses 上游的 `encrypted_content`，跨账号的 `openai` / `xai` 信封在本地直接丢弃，`passback` Messages 上游在工具轮回传无签名 `thinking` 块 | 每个丢弃的令牌都有 `replay_token_dropped`；`legacy_history` 的请求通过严格请求校验器；阶段 7 的 live 金丝雀确认同方案同账号的信封令牌被真实上游接受 |
| D8 | 降级后发给上游的工具名与旧实现不同 | 满足 3.11 的命名要求；客户端看到的工具名与旧实现相同；差分报告逐个列出变化的名字 |
| D9 | Responses 客户端转 Chat 上游时，推理文本从 `plain` 信封取回，不再查 Redis | `reasoning_encrypted_only` 通过；方言规则 `deepseek-thinking` 无违规 |
| D10 | 原生 Messages、Responses 路径在请求含信封时还原或删除令牌（新增行为，旧实现没有这一步） | 不含 `trc1:` 的请求逐字节不变；`reasoning_carrier_native` 通过 |

清单之外，以下差异必须为零：用量；`Semantic`、`Terminal` 标记序列（D3 除外）；客户端可见的文本、工具名、工具参数和结束原因（D1 到 D3 除外）。

验收：

- T1 到 T4 全部通过，六条路线的定律无失败。
- 差分的未归类差异为零；`diff_ledger.json` 里每条都有类别，并满足该类别的接受条件。
- 用量差分为零。工具名差分报告入库。
- 每个解码器的 fuzz 在本地各跑 10 分钟无崩溃。
- 基准测试（2 MB 的请求体、5,000 帧的流）：新引擎的耗时不超过旧实现对应路径的 2 倍。超过时先分析再决定是否优化，结果写进进度记录。
- 新包没有被任何生产代码导入（阶段 2 才接入）。

### 6.3 阶段 2：接入 OpenAI 兼容平台和 Anthropic 平台

目标：Messages、Responses、Chat 三种上游协议的转换路径接入新引擎，按 3.13 合并流程。新路径放在接线开关后面，生产仍走旧路径。

步骤：

1. 接线开关（2.4 节）：在涉及转换的 7 组执行器与运行时的 `Options` 结构体（`OpenAITextExecutor`、`OpenAIResponseOutput`、`OpenAIWS`、`GrokExecutor`、`messageforward.Options`、`googleforward.Options`、`QoderRuntimeOptions` 及 `UnifiedTextExecutor`）上加 `UseIRConversion bool`，`internal/app` 的生产 `provide*` 保持默认 `false`，`newProtocolLabRouter` 按 `labOptions` 打开（不在 Wire 中注册裸 `bool`）。新增 `internal/app` 单元测试直接调用各生产 `provide*` / `*Options(cfg)` 断言该开关全部关闭（无需启动真实 DB/Redis，阶段 8 删除这个测试）。
2. `gateway/provider/model_attributes.go` 新增 `ConversionDialect`（3.7 节，接收 `modelrules.Resolution`）；`upstream/openai/encrypted_content.go` 的 `OpenAIEncryptedContentDigest` 在计算 SHA-256 前先调 `ir.ParseCarrier` 取出内部密文原文。
3. 逐个上游族接入，每个族一组提交，顺序如下。每个族的做法相同：新增转换入口和合并后的读取函数；在分派点按接线开关选择新旧；旧代码原样保留。
   - Responses 上游：`openaiforward/messages.go`、`chat.go`（保留对 `/v1/chat/completions` 上无 `messages` 但带 `input` 的 Cursor 请求分支处理）、`httpapi/openai_messages_execution_ports.go` 的 `Convert`、`openai_chat_execution_ports.go` 的 `ChatToResponses`；读取函数合并为 `upstream/openai/response_converted.go`（包含前导帧暂存窗口、`responseStreamLifecycleTracker`、`EmptyCompleted` 和 `ChatSilentRefusalDetector`）。`ApplyAnthropicCompatFullReplayGuard`、`TrimCompatResponsesInputToLatestTurn`、`AppendOpenAICompatClaudeCodeTodoGuard` 和 `session` 里的 Messages 提示缓存摘要当前接收旧子集结构体指针，本阶段同步提供基于字节/`jsontext` 或 `ir.Request` 的无损版本供新路径调用（避免经过旧子集结构体丢失 `Extra` 字段）。
   - Chat 上游：`openaiforward/raw_fallback.go`；读取函数合并进 `upstream/openai/response_raw_chat.go` 的新函数。新路径不使用 `ReasoningContentByID` 回调和 `ReasoningHistory`，推理文本来自 `plain` 信封（3.10 节），并在编码后补齐模型属性修正。
   - Messages 上游：`gateway/forward/conversion.go` 新增 `AsConverted`，`openaiforward/anthropic_flows.go` 新增合并后的入口（补齐 `PrepareModelAttributeParameters`，保持 `Mimic -> EnforceCacheControlLimit -> InjectAnthropicCacheControlTTL1h` 的编码后顺序，并增加跨 Claude 模型签名 400 的剥离重试）；读取函数合并成 `openaiforward` 里的一对（使用延迟 `OutputContext`，并在 `CommittedSSE(c)` 为真时发送无计时的本地终态 SSE 错误），Anthropic 平台的 `messageforward` 改为调用它。`messageforward` 同步补齐 `target.View().IsBedrock()` 的转换分支：复用 `bedrock` 的区域路由、`PrepareBedrockRequestBodyWithTokens`、SigV4/API Key 签名，以及二进制 AWS EventStream 到 Anthropic SSE 的解码桥，使实现与 `capability.SupportsProtocolConversion` 和 `anthropic_upstream.md` 一致。
   - Grok 的 Chat 转 Responses：`httpapi/grok_chat_execution.go`（`Dialect.OmitStoreAndInclude` 为真）。
4. 原生路径的信封处理：Messages 原生路径（Anthropic 平台的 `messageforward`、兼容平台的 Anthropic 端点）和全部 Responses 原生路径（OpenAI、Grok 等 HTTP Responses、`/v1/responses/input_tokens` 预检、专用 WS `gateway/ws/passthrough.go`、连接池 WS `gateway/ws/stream.go` 以及 WS HTTP 桥 `openai_ws_http_bridge.go` 的原生转发分支）在发往上游之前调用 `convert.PrepareNativeReplay`，同样放在接线开关后面。
5. 无接线开关的包级辅助函数（`gateway/provider/input_tokens.go`、`gateway/tokenestimate/estimate.go`、`gateway/provider/openai_responses_ingress_compat.go` 的 `convertLegacyResponsesMessages`）：本阶段先写出基于 `convert` 的新辅助函数并补齐与旧实现的差分和单元测试，生产调用点继续调旧实现（严格保证阶段 0 到 7 不改变生产行为），统一在阶段 8 切换调用点。注意 `PrepareAnthropicInputTokens` 发往官方 `https://api.openai.com/v1/responses/input_tokens` 时仍只投影 `{model, instructions, input, tools, tool_choice}` 五个计数字段并对 `input` 执行信封还原；`NormalizeOpenAIResponsesLegacyIngress` 只替换内部的 `convertLegacyResponsesMessages`，保留外层混合报文合并逻辑。
6. 每接入一个族：T5 在新路径下全绿，旧路径与基线一致；T6 相关格子全绿，且不比旧路径的基线差；用量一致性断言通过；混合分组的"转换后原生"（含 Bedrock、Vertex、OpenCode 和 `passback` 模型）场景通过。

验收：

- `make test-go`、`make test-clientlab` 通过；新路径的 T5、T6 结果不劣于旧路径的基线。
- 旧基线里记录的缺陷，在新路径下逐条说明是已修复还是仍存在。
- 生产装配下接线开关关闭的测试通过。

### 6.4 阶段 3：Gemini 编解码器和 Gemini 平台

1. 实现 `codec/generatecontent`（第 4.4 节）。schema 清洗、思考配置解析、文本增量计算、工具帧去重都从旧代码移植。跑通 T2。
2. 差分对象是 `bridge.NativeConvertClaudeMessagesToGeminiGenerateContent`（请求）和 `NativeGeminiMessagesStream`、`NativeGeminiCompatStream`（流）。Responses、Chat 到 Gemini 的旧路径经过 Messages 串联，属于 D2 的差异会比较多，逐条记账。Gemini 推理签名从留空变成信封属于 D6。
3. `CallMemory` 的网关实现：`gateway/session` 里包一层 `GeminiToolCalls`，按请求的 API Key、提供商和上游模型限定范围后作为 `Options.Calls` 传入（3.10 节）。
4. 网关：`googleforward/gemini_openai.go` 的两个 `ForwardAs*` 和 `gemini_attempts.go` 的 Messages 入口在接线开关后面合并成一个转换入口；`upstream/gemini` 的读取函数合并成一对。OpenCode 的 Gemini 路线（`unified_text_executor.go:304-310`）走同一入口。Gemini 原生客户端的 `ForwardNative` 不动。上游拒绝签名时用兼容格式重试的逻辑（`exchange_openai.go`、`exchange_messages.go`）留在 `upstream/gemini`。重试时用清空了 `Dialect.ReplayScheme`、不传 `Calls` 的选项重新创建 `Exchange`（并保留 `StripWireReasoningEffort`）：令牌全部丢弃，每个步骤的首个调用写占位签名，效果与现在的兼容格式重试相同，不需要新增选项。
5. `count_tokens` 到 Gemini（`httpapi/count_tokens_selected.go:109`）在接线开关后面用 `Exchange.UpstreamRequest`（`ReplayScheme` 和 `PlaceholderToken` 置空）得到 Gemini 请求体。`backend_modules.md` 加入 `protocol/codec/generatecontent`。
6. T5 加 `gemini` 提供商的全部场景（含 5 秒稀疏用量尾帧合并）；T6 的 Gemini 上游格子转绿。

验收同阶段 2 的形式，范围是 Gemini 上游。

### 6.5 阶段 4：Antigravity

Antigravity 的上游是包了一层信封的 Gemini 协议，同时承载 Gemini 和 Claude 模型。

1. `ConversionDialect` 为 Antigravity 返回 `SchemaDialect: antigravity` 和相应的 `Reasoning`（包含 `ForceBudget`），并按目标模型族拆分：`claude-*` 用 `ReplayScheme: "antigravity_claude"`、`PlaceholderToken: ""`；`gemini-*` 用 `ReplayScheme: "gemini"`、`PlaceholderToken: DummyThoughtSignature`。`googleforward.Antigravity` 同步挂载 `session.GeminiToolCalls` 作为 `CallMemory`。
2. 请求：`antigravity_messages.go` 和 `antigravity_openai.go` 的三个入口在接线开关后面合并。IR 经 `codec/generatecontent` 编码后，`upstream/antigravity` 继续做身份补丁、`CleanGeminiRequest`、`WrapV1InternalRequest`。调研发现旧代码里 Chat、Responses 来源按模型名分成两种转换实现，而 Messages 来源始终用一种。合并后只有一种编码器，差异由 `Dialect` 表达。差分要同时对照 `TransformClaudeToGeminiWithOptions` 和 `NativeConvertClaudeMessagesToGeminiGenerateContent` 两个旧实现，按"来源协议加模型族"分组记录差异（重点核对 `functionResponse` 的 `result` 对比 `content`、`functionCall.id`、`toolConfig: VALIDATED`、`mcpXMLProtocol` 和 `sessionId`）。这一组差异不预先归类，全部列出后询问用户以哪一种为准；用户的决定作为新类别加进 6.2.4 的清单。
3. 响应与恢复：`response_stream.go`、`response_compat_stream.go`、`response_compat_nonstream.go` 合并成一对读取函数。`recovery_gemini.go` 作用于上游 Gemini 字节，保持不变；`recovery_claude.go`（当前接收 `protocolanthropic.ClaudeRequest` 并调 `TransformClaudeToGeminiWithOptions`）在新路径上新增基于 `Exchange` / `ir.Request`（剥离思考或工具块并修正预算后重编）的恢复分支，同时为 `probe.go` 的 `BuildClaudeTestRequest` 准备基于 `convert` 的新实现（阶段 8 切换）。
4. T5 加 Antigravity 提供商夹具。Antigravity 走 OAuth，T6 不覆盖，靠 T3、T4、T5。

验收同阶段 2 的形式，范围是 Antigravity 上游。

### 6.6 阶段 5：同协议 Responses 客户端工具降级

目标：Responses 客户端到不支持全集客户端工具的 Responses 上游（OpenAI API Key、DeepSeek、Grok 及 Responses WebSocket HTTP 桥）时，custom、namespace、tool_search 工具的降级与还原改走 `convert` 的同协议轻量入口，退役 `bridge` 的线级客户端工具适配代码。

1. 按 3.8 节实现 `NeedsResponsesToolAdaptation`、`AdaptResponsesTools`、`RestoreResponsesTools` 和 `NewResponsesToolStream`。它们内部复用 `lowerTools` / `raiseTools` 的命名、包装和历史发现规则，但只改写请求中的 `tools`、`additional_tools` 及工具调用/输出条目，响应流只拦截并还原被降级的工具调用条目，保持其余请求字段、条目级 `prompt_cache_breakpoint`、`compaction` 条目、推理裸令牌以及 `partial_image` / `web_search_call.searching` / `mcp_*` 等原生托管工具中间流事件原样透传。顶层 `prompt_cache_options` 在非 OpenAI 平台上继续由 `openaiforward/transform.go` 剥离。
2. 在接线开关后面替换全部四个调用点：`openaiforward/passthrough.go:238`（OpenAI API Key HTTP 透传）、`openaiforward/prelude.go:299`（DeepSeek 等兼容 Responses）、`grokforward/responses.go:75` 的 `PatchGrokResponsesBodyWithClientTools`（同时兼容紧随其后的 `in.Compact == true` 压缩构造）、`gateway/ws/httpapi/openai_ws_http_bridge.go:636` 的 `AdaptResponsesClientToolsJSONWithMapping`。WebSocket 会话把每轮（包括本地直接返回的 `generate: false` 预热轮）得到的 `ToolMap`（含 `LoweredTools` 声明）存进连接状态，下一轮省略 `tools` 或 `model` 时通过 `inherited` 传入。
3. 响应侧：新路径使用 `RestoreResponsesTools` 和 `NewResponsesToolStream` 取代 `upstream.NewResponsesClientToolStreamBody` 与 `bridge.RestoreResponsesClientToolPayload`。注意 `gateway/httpapi/response_tool_restoration.go` 和 `gateway/requeststate/response_tools.go` 同时承载 `CodexToolNameReverse`（3.11 节保留不变）和原生命名空间扁平化开关，阶段 8 只清理其中客户端工具映射相关的函数与字段，不整文件删除。
4. T5 加"Responses 客户端到 OpenAI API Key、Grok、DeepSeek 的 Responses 上游（及 WS HTTP 桥预热续接），带 custom 和命名空间工具"的场景；T6 的 `lab_custom_tool` 在这些路线上通过；确认 `record` 模式录到的全部 Codex 请求在不带需降级工具时逐字节不变，带需降级工具时除工具条目外其余字段无损。

验收同阶段 2 的形式，范围是同协议客户端工具降级的路线。

### 6.7 阶段 6：Qoder

Qoder 是切换的前置条件之一，不能推迟到切换之后（2.5 节）。

1. 实现 `protocol/qoder` 和 `codec/qoderchat`（4.5 节），`backend_modules.md` 同步登记这两个新包。
2. 在接线开关后面，把 `upstream/qoder/execute.go` 的三个解析分支换成 `Exchange`（解析 `codec/qoderchat` 输出的 `protocol/qoder.PayloadRequest`，保留 `*qoder.RequestError` 本地 400 映射以抑制误切号），三个流写出器和三个缓冲构造器换成一对读取函数；同步为 `provider/provider/qoder_test_execution.go` 准备基于 `convert` 的连接测试构造分支（阶段 8 切换）。
3. 差分对象是三个 `ParseQoder*Payload` 和三个流写出器。Qoder 的旧流写出器里有自己的 Responses 事件构造（`WriteQoderResponsesStreamResponse`），差分时以语法校验器为准，旧输出违反校验的归入 D3。
4. 现有的 Qoder 测试夹具（`gateway/testkit` 的 Qoder 捕获客户端）接入 T5，覆盖无状态 Responses 的 400 拒绝与工具能力门禁。Qoder 的认证和会话较特殊，T6 不覆盖，靠 T3、T4、T5。

验收同阶段 2 的形式，范围是 Qoder 上游。

### 6.8 阶段 7：发布前总验收

前置条件：阶段 0 到 6 全部通过，进度记录里没有待确认差异。本阶段同样不改变生产行为。验收中发现的问题在新路径上修，修完重跑受影响的验收项。

步骤：

1. 重新执行 10.4 的导入方清点命令，确认每个 bridge 导入方在阶段 8 都有去处。
2. 按 `routes.txt` 逐行核对覆盖：每条路线都有 T3 的定律和 T5 的场景；有真实客户端的路线有 T6 的格子（Antigravity、Qoder 除外）。缺的先补。
3. 旧路径重跑全部基线（`conversion_matrix_scenario_test.go`、T5 旧路径、T6 旧路径各一次），新路径重跑 T1 到 T6，生成最终的差分报告。
4. live 金丝雀（`TOKENROUTER_CLIENTLAB_MODE=live`，只在一次性容器里运行）。需要真实凭据，由维护者执行，结果写进进度记录。组合如下，每个组合跑 `lab_reasoning_tool`，再用 `lab_switch_provider` 让第二轮落到原生路径一次：
   - Codex 到 Anthropic（`anthropic` 方案，信封里的 thinking 文本原样回到上游）；
   - Claude Code 到 OpenAI（`openai`）；
   - Claude Code 和 Codex 到 Gemini（`gemini` 推理令牌和 `CallMemory` 里的调用令牌）；
   - Claude Code 到 Grok，以及 Codex 经方言降级到 Grok（`xai`）；
   - Codex 到 DeepSeek 的 Chat 上游（`plain`，`reasoning_content` 被接受）。
5. 全部编解码器的基准测试。
6. 汇总升级说明的要点：2.5 节列出的后果，6.2.4 清单里客户端或运维能感知到的差异（D6、D7、D9、D10），首轮提示缓存可能未命中，Redis 里 `reasoning_content:` 前缀的旧键会自然过期，回滚的限制（2.4 节）。写进进度记录，阶段 8 照此写 `upgrade_notes.md`。

验收（切换的门槛）：

- `routes.txt` 的每一行都有覆盖。
- 新路径的 T1 到 T6（脚本模式）全绿；live 金丝雀的全部组合通过。
- 差分的未归类差异为零；每条差异都落在 6.2.4 的清单里并满足该行的接受条件；用量差分为零。
- 旧基线里记录的每个缺陷都标明在新引擎下已修复还是仍存在。
- 基准测试不超过旧实现对应路径的 2 倍，或者超出项已有分析并得到用户认可。

### 6.9 阶段 8：切换和退役

前置条件：阶段 7 通过；已和用户确认发布窗口（2.4 节）。本阶段的全部提交推送并通过验收之前，不打发布标签。

1. 切换：删除接线开关和"生产装配下关闭"的测试，所有分派点（含阶段 2、4、6 准备好的 `input_tokens.go`、`tokenestimate/estimate.go`、`openai_responses_ingress_compat.go`、`antigravity/probe.go`、`qoder_test_execution.go`）直接调用新入口；原生 Messages、Responses 路径（含 `/v1/responses/input_tokens` 和全部 WS 分支）始终调用 `PrepareNativeReplay`。
2. 删除旧的转换流程和读取函数（3.13 两张表的"现有"列），迁移 `openai_native_anthropic_ports.go:224` 的 `writeTextResponsesError` 后删除 `gateway/forward.Output` 接口和 `httpapi.ForwardConversionOutput`。删除 `upstream/client_tools_stream.go`（同步清理 `openai_forward_http_adapter.go:148`），并清理 `response_tool_restoration.go` 与 `requeststate/response_tools.go` 中的 `ResponsesClientToolMapping` 状态（保留其中的 `CodexToolNameReverse` 和原生命名空间开关逻辑）。删除旧文件时同步更新所属目录的 `doc.go`。
3. 删除推理连续性的旧机制：`gateway/session/reasoning_history.go`、`ReasoningContentCache` 接口、`rediscache` 里的 `SetReasoningContent`、`GetReasoningContent` 和 `reasoning_content:` 键前缀、`provideReasoningHistory` 及其装配（含 `openai_raw_response_output.go`、`openai_raw_fallback_ports.go`、`openai_chat_execution_ports.go` 里的回调绑定）和 `OpenAIResponseOutput.Reasoning` 字段。Redis 里的旧键自然过期，不写清理脚本。
4. 删除 `protocol/bridge`。删除前把仍被原生或保留路径使用的函数与常量移到对应的协议包：
   - 移到 `protocol/openai`：续接策略（字节级/`jsontext` 版的最新轮裁剪与 `TodoGuard`）、原生流与非流式重构使用的 `BufferedResponseAccumulator`、`ResponsesStreamOutputItems`、`NormalizeResponsesStreamingTerminalOutput`、`ReconstructResponseOutputFromSSE`（`response_nonstream.go`、`response_passthrough.go`），以及原生命名空间开关与 Compact 仍在用的 `StripOpenAIResponsesInputNamespaces`、`FlattenResponsesNamespacesExcept`、`RestoreResponsesNamespaceCalls`。
   - 移到 `protocol/anthropic`：完整重放尾部裁剪策略。
   - 移到 `protocol/gemini`：`NativeExtractGeminiUsage`、`NativeUsageProjection`、`NativeNormalizeGeminiRequestForAIStudio`（`upstream/gemini/request.go`）、`NativeIsGeminiInlineImageMIMEType`（`image_observation.go`）、`DummyThoughtSignature`（`native_gemini_attempts.go`、`antigravity_attempt.go`）、`CleanJSONSchema` 与 `DeepCleanUndefined`（`antigravity.CleanGeminiRequest`）；`upstream/gemini/response_completion.go:181` 和 `upstream/antigravity/execute.go:289,298` 原生路径对 `CompatJSONHasContent` / `CompatOutputMeaning` 的调用切到 `protocol/gemini`（`ObservePayload`）或 `convert`。
   - 迁移或清理 `bridge` 之外导入 `bridge` 的 22 个测试文件，用 `rg 'protocol/bridge"'` 确认全仓（含测试）没有剩余导入。
5. 删除只被旧转换器使用的线类型：在 `antigravity/recovery_claude.go` 和 `probe.go` 全部切完后删除 `protocol/anthropic/claude_types.go` 的 `Claude*` 一族；核对 `session/anthropic_prompt_cache.go` 等外部引用后，清理 `protocol/openai/types.go`、`protocol/anthropic/types.go` 里不再有调用方的类型。仍有调用方的保留。
6. 删除 `testkit/converter.go`（同步更新 `protocol/testkit/doc.go`）、`legacy_baseline.json`、`diff_ledger.json`、`conversion_matrix_scenario_test.go`、差分测试，以及 T5 的旧路径分支和 `labOptions` 里的接线开关选项。差分台账的最后一版留在 git 历史里。
7. 架构规则：从 `catalog.go` 的 `leafDependencies`、`moduleDependencies`（`internal/gateway`、`internal/upstream`）以及五个 `platformDependencies` 条目中删除 `internal/protocol/bridge` 的全部许可；确认文件许可的使用方测试没有过时条目。
8. 文档按第 7 节更新，`docs/operations/upgrade_notes.md` 按阶段 7 汇总的要点写升级说明。
9. 按 `AGENTS.md` 的要求搜索旧名字和描述旧行为的关键词（`bridge`、`ReasoningHistory`、`reasoning_content:`、`ReasoningContentByID`、接线开关的名字、`gAAAA`），处理残留的注释和文档。

验收：

- `make verify` 通过；T6 脚本模式全绿。
- `rg -n "bridge\." backend/internal --type go` 没有指向旧包的引用；第 9 步的关键词没有过时的残留。
- 发布由用户决定。发布说明的升级说明取自 `upgrade_notes.md` 的这一条。

## 7. 架构规则和文档改动清单

架构规则（`tools/architecture/catalog.go`、`policy.go`）：

| 阶段 | 改动 |
| --- | --- |
| 0 | `pureStandard` 加 json v2 两个路径；`sse`、`ir`、`testkit`、`testkit/mockupstream`、`testkit/cmd/corpus` 的 `leafDependencies`（每个 `Production` 均含自身包路径）；`policy.go` 把 `fixture` 分支移到整个 `pure` 分支之前；`internal/protocol/bridge` 的 `Tests` 加 `internal/protocol/testkit`（不动 `internal/app`） |
| 1 | `codec/{messages,responses,chat}` 和 `convert` 的 `leafDependencies`；`internal/protocol/ir`、`internal/protocol/convert`、各 codec 的 `Tests` 加 `internal/protocol/testkit`；`convert` 的 `Tests` 加 `internal/protocol/bridge` 和 `internal/protocol/{openai,anthropic,gemini}`（差分用，`bridge` 许可在阶段 8 删除） |
| 2 到 6 | 阶段 3、6 分别补 `codec/generatecontent`、`protocol/qoder`、`codec/qoderchat` 的 `leafDependencies`；`moduleDependencies` 里的 `internal/gateway`、`internal/upstream`，以及 `platformDependencies` 里的 `internal/upstream/{openai,gemini,antigravity,grok,qoder}`（规则合取，两张表都必须加；阶段 6 还需在 `internal/provider` 加 `internal/protocol/convert` 供 `qoder_test_execution.go` 使用）加 `internal/protocol/convert`、`internal/protocol/sse`、`internal/protocol/ir`（Qoder 另加 `internal/protocol/qoder`） |
| 8 | 从 `leafDependencies`、`moduleDependencies` 和 `platformDependencies` 删除 `internal/protocol/bridge` 相关的全部条目 |

Project Doc：

文档描述的是当前状态。阶段 8 之前生产走旧引擎，描述网关行为的文档到阶段 8 才改；只描述新包本身的文档随包落地。每次改文档或 `@project-doc` 锚点后运行 `make check-docs`。

| 文档 | 改动 | 阶段 |
| --- | --- | --- |
| 新增 `docs/architecture/protocol_conversion.md` | IR 的形状和不变量、扩展机制和三条防滥用规则、Loss、Dialect、转换管线、推理连续性（信封、`CallMemory`、原生路径的处理）、工具降级、新增协议的步骤。带稳定锚点，在 `convert/exchange.go` 的 `NewExchange` 上加 `@project-doc` | 1 起草，写明网关尚未接入；随各阶段补充；8 删掉"尚未接入"的说明 |
| `docs/architecture/index.md` | 列入上面的新文档 | 1 |
| `docs/architecture/backend_modules.md` | 随各阶段新建包同步加入包地图；阶段 8 删除 bridge，静态依赖图里的 `protocol/bridge` 换成 `protocol/convert`；`gateway/session` 一行去掉"推理历史" | 0、1、3、6、8 |
| `docs/architecture/gateway_request_lifecycle.md#protocol_conversion_boundary` | "转发与流式输出"一节改写为按上游协议合并后的结构（保留 `<a id="protocol_conversion_boundary"></a>` 锚点）；删除对 `protocol/bridge` 的引用 | 8 |
| `docs/interfaces/protocol_capabilities.md` | "客户端用量与缓存"里关于转换读取器的描述对齐新结构；说明同协议 Responses 客户端工具轻量适配 | 8 |
| `docs/interfaces/{anthropic,openai,gemini,antigravity,grok,qoder,opencode}_upstream.md` | 各自涉及转换的段落。`openai_upstream.md` 删掉 `ReasoningHistory` 一段（第 100 行）并更新第 89 行包名；`gemini_upstream.md` 改写"签名字段留空"和"内存中保留调用关联"两段（第 45、65-66 行），按 3.10 节描述信封和 `CallMemory`；`grok_upstream.md` 对齐 `xai` 信封和工具适配 | 8 |
| `docs/operations/development_workflow.md` | 测试分层加 T1 到 T6；验证策略的"上游平台""网关"条目加入定律、语料和客户端实验室的要求；缺陷回归流程（5.9）；依赖规则里 protocol 一段（开发期临时要求只留在本计划文件，不写进长期文档） | 0、8 |
| `docs/operations/upgrade_notes.md` | 一条升级说明：转换引擎替换、客户端令牌字段改为信封、旧会话可能丢失的推理连续性、首轮提示缓存可能未命中、`reasoning_content:` 旧键自然过期、回滚的限制 | 8 |

## 8. 风险和应对

| 风险 | 应对 |
| --- | --- |
| 旧实现的隐含行为很多，新实现漏掉 | 阶段 0 的基线和各阶段的差分把每个差异摆出来；接受的差异只限 6.2.4 清单里的类别，其余必须对齐或询问用户 |
| 一次切换覆盖全部路线，没有运行时回退 | 阶段 7 按 `routes.txt` 逐行验收，再加 live 金丝雀；切换放在和用户确认过的发布窗口里；需要回退时整体回滚版本（2.4 节） |
| 接线开关被误打开，半成品随常规发布上线 | 开关只是装配选项，不读配置和环境变量；"生产装配下关闭"的测试每次提交都跑 |
| 开发周期长，期间旧引擎还在接收线上修复（例如 `d91aacd8e` 到 `4b05d79a5` 这一组 Gemini 修复） | 修复照常进旧引擎，同时把复现夹具加进语料，新引擎必须通过；基线测试一直运行，旧引擎行为变化会让它失败，提醒更新基线和差分（2.4 节） |
| 旧会话在升级后丢失推理连续性 | 2.5 节列出的后果是有意接受的；`legacy_history` 场景保证这类请求仍能通过上游校验，升级说明写明 |
| 信封进入客户端历史后，客户端绕过网关直连官方 API 时签名被拒 | 接受。旧引擎写进历史的外来裸令牌有同样的问题；升级说明写明 |
| 计费受影响 | 计费用量的解析路径不改；客户端可见用量复用同一批归一化函数；用量差分必须为零；T5 核对客户端用量和结算用量 |
| 工具名变化影响会话 | 降级名只出现在上游请求里，每个请求按完整历史重新降级；同一版本内由 Q5 和 3.11 的命名要求保证稳定；跨版本只造成一次提示缓存未命中 |
| 请求输出不稳定影响缓存命中 | Q5 定律；请求方向禁止随机 id；`Deterministic(true)` |
| `encoding/json/v2` 比 v1 严格，多出 400 | 请求解码和 `PrepareNativeReplay` 显式放宽无效 UTF-8；重复键保持拒绝（与入口 `wirejson.ValidateRequestObject` 一致）；语料回放覆盖真实请求 |
| 性能回退 | 阶段 1 的基准门槛；`jsontext.Value` 避免对 schema 和媒体的重复解析 |
| 测试自洽但对协议理解有误 | 语料和 T6 提供事实来源；原生对照组用真实客户端校验编码器；live 模式校验真实上游 |
| T6 不稳定或太慢 | 脚本模式完全确定；客户端重试关到 0；锁定版本；`latest` job 不阻塞合并 |
| 客户端新版本改变请求形状 | 普查 golden 让新字段可见；`latest` job 每晚跑；每个版本重录语料 |
| 同协议流量改写工具时破坏其他字段或原生流事件（阶段 5） | 同协议走 `AdaptResponsesTools` 和 `NewResponsesToolStream`，只改写工具声明和工具调用条目，其余请求字段、缓存断点和原生托管工具中间流事件原样透传；录制的 Codex 请求全量验证 |
| 计划的映射表与真实协议有出入 | 映射表是起点；实施时以官方文档和录制语料为准，发现出入就改表并记入进度记录 |

## 9. 决策记录

### 9.1 中间表示的形状

考虑过的方案：

- 以 OpenAI Chat 为枢纽（LiteLLM 的做法）。Chat 表达不了推理条目、重放令牌、平铺的工具条目，只能靠 `provider_specific_fields` 这类无类型口袋。否决。
- 以 Responses 为枢纽（现在的代码事实上在这么做）。Responses 有 30 多种 item，表达不了 Messages 的块内顺序约束和 `cache_control`，还会把 OpenAI 的概念强加给所有编解码器。Bifrost 的 Responses 适配成本可以说明这一点。否决。
- 两套并行的表示（Bifrost）。两套之间的互转本身就是新的丢失来源。否决。
- 独立设计的"消息加有序 part"（pi-ai、Vercel AI SDK 的方向）。语义核心小，part 级别可以挂重放令牌和扩展。采用。

### 9.2 编解码器使用私有线结构体

不复用 `protocol/openai` 等包里的结构体：它们是子集，带为旧转换器定制的序列化，调用方很多。私有结构体让"线格式长什么样"成为每个编解码器的内部信息，可以自由加 embed 回退字段。代价是开发期间有两套线结构体，阶段 8 清掉只被 bridge 使用的那部分。

### 9.3 规范事件流是顺序的

让解码器负责把交错的 part 顺序化，编码器全部按"一次一个 part"实现。备选是允许交错、由需要顺序的编码器各自缓冲，那样每个编码器都要处理一遍交错。顺序化的代价是交错的并行工具参数会延迟到前一个调用结束后才流出，文本不受影响。

### 9.4 转换路径先按上游协议合并，不直接并入原生管线

更彻底的做法是让每个平台执行器只懂原生协议，转换作为外层中间件改写请求、包装输出。这能消掉"转换流程重新实现原生管线步骤"的重复。没有在本计划里做的原因：原生执行器的错误输出、响应头提交、客户端识别目前都与客户端协议耦合，改造面是整个网关执行层，风险和工作量都超出"重构协议转换"。按上游协议合并是它的前置步骤，完成后每个上游族只剩两条路径，届时再评估。

### 9.5 后续方向（本计划不实施）

- 原生管线与转换管线合并（9.4）。
- Gemini 客户端到非 Gemini 上游的路线：编解码器的两半都已具备，需要路线表条目、Gemini 入口的转换入口和 T5、T6 覆盖。
- 引用和 grounding 的结构化互转：IR 已有 `Annotations`，目前编码器只保留旧行为。
- 把 `Loss` 计数接入 Ops 指标，按路线观察客户端实际在用哪些会被丢弃的特性。

### 9.6 一次发布切换，首版采用最终的推理连续性方案（2026-11-14 修订）

修订前的做法：阶段 2 加进程配置 `gateway.conversion_engine`，按上游族逐个把默认值切到新引擎，观察一个发布周期后再删除旧引擎；推理连续性在阶段 1 到 6 与旧路线逐条等价，阶段 7 再换成信封，并保留嗅探来兼容加固之前的历史；Qoder 可以推迟到删除旧引擎之后。

修订后：开发和测试照旧分阶段，全部路线通过阶段 7 的总验收后一次切换，旧引擎随同一版本删除；首版就用 3.10 节的信封方案；跨版本兼容按 2.5 节逐项取舍。

理由：两套引擎在生产中并存时，同一类缺陷要修两处，配置和文档要描述两种行为，迁移期的等价规则写了还要再删。用户接受旧会话在升级后丢失一部分由旧引擎生成的转换状态，嗅探和服务端的推理文本副本也就不再需要。代价是没有运行时回退，只能整体回滚版本，所以发布前的门槛提高到按路线清单逐行验收，再加 live 金丝雀。

### 9.7 实施前代码对齐与边界收口（2026-11-21 修订）

对照 `164bc487b` 之前合入主干的协议与门禁变更，在实施前敲定五项决策：

1. 阶段 5 同协议客户端工具降级采用轻量线级适配：同协议 Responses（含 OpenAI API Key、DeepSeek、Grok 及 WS HTTP 桥）使用 `convert` 内的 `AdaptResponsesTools` 和 `NewResponsesToolStream`，内部共享 `lowerTools` / `raiseTools` 规则，但只改写工具声明和调用条目，避免整包 IR 编解码冲掉条目级 `prompt_cache_breakpoint`、`compaction` 条目和 `partial_image` / `web_search_call.searching` 等托管工具中间流事件。
2. Bedrock 跨协议转换在阶段 2 补齐：在 `messageforward` 的合并转换路径上接入 Bedrock 区域路由、请求体转换、SigV4/API Key 签名及二进制 AWS EventStream 转 SSE 解码，与 `capability.SupportsProtocolConversion` 和文档声明对齐。
3. 账号绑定密文在信封内携带签发提供商 ID：`trc1:` 信封增加可选 `p`（`ProviderID`），`openai` 和 `xai` 方案同号命中时解封回传，跨号切换时本地直接丢弃并上报 `replay_token_dropped`，省去一次必败的 `400 invalid_encrypted_content` 往返；`OpenAIEncryptedContentDigest` 哈希前先解封，对齐 HTTP 与 WS 的失效密文表。
4. Antigravity 双实现细节留到阶段 4 差分表裁决：先按模型族拆开 `antigravity_claude` 与 `gemini` 的 `Dialect`、修好 `recovery_claude.go` 和 `probe.go` 对 `ClaudeRequest` 的依赖，`result` 对比 `content`、`toolConfig` 等字段分歧等阶段 4 跑出完整差分后再决定基准。
5. 无开关包级辅助函数留到阶段 8 切换：`input_tokens.go`、`tokenestimate`、`openai_responses_ingress_compat.go` 等包级函数在阶段 2 先写新实现做对照测试，阶段 8 统一切换，严守阶段 0 到 7 不改生产行为的约束。

## 10. 附录

### 10.1 旧代码到新结构的对照

| 旧位置 | 新位置 |
| --- | --- |
| `bridge/anthropic_to_responses.go`、`responses_to_anthropic_request.go`、`chatcompletions_to_responses.go`、`chatcompletions_anthropic_bridge.go` 的请求部分、`chatcompletions_responses_bridge.go` 的请求部分 | `codec/{messages,responses,chat}` 的 `request_decode.go`、`request_encode.go`、`legalize.go` |
| `bridge/*` 的六个流状态机和非流式转换函数 | 各编解码器的 `stream_decode.go`、`stream_encode.go`、`response.go` |
| `bridge/responses_client_tools.go`、`responses_namespace.go`、`responses_tool_identity.go`、`responses_tool_history.go`、`responses_tool_search_discoveries.go`、`responses_input_namespace.go`、`client_tools_json.go`、`responses_tool_adaptation.go` | `convert/tools_lower.go`、`tools_raise.go`、`tools_map.go`、`tools_responses.go`（原生命名空间开关与 Compact 仍用的三个函数迁至 `protocol/openai`） |
| `bridge/gemini_*.go` | `codec/generatecontent`（原生路径共用的 `DummyThoughtSignature`、AI Studio 归一化、schema 清洗与用量提取迁至 `protocol/gemini`） |
| `bridge/response_format.go` | Chat 和 Responses 编解码器内部 |
| `bridge/compat_output_observation.go` | `convert/output_meaning.go`（结构化标记计算）及 `protocol/gemini` |
| `bridge/compat_continuation.go`、`compat_replay_guard.go` | `protocol/openai`、`protocol/anthropic`（改为字节级/`jsontext` 或 IR 实现，避免旧子集结构体丢未知字段） |
| `bridge/runtime.go`、`request_options.go` | `convert.Runtime`、`convert.Dialect` |
| `gateway/forward/conversion_sse.go` 等各处 SSE 扫描和写出 | `protocol/sse`（转换路径使用；原生路径的扫描器不强制替换） |
| `gateway/provider.ModelConversionOptions`、`GeminiThinking` | `gateway/provider.ConversionDialect` |
| `bridge` 里散落的令牌规则（`gAAAA` 判断、Gemini 签名留空、Responses 令牌写进 Messages `signature`） | 不移植，按 3.10 节在 `convert/carriers.go` 重写 |
| `gateway/session.ReasoningHistory` 及 `rediscache` 里的 `reasoning_content:` 存取 | 阶段 8 删除，由 `plain` 信封取代 |
| `gateway/session.GeminiToolCalls` | 保留，作为 `convert.CallMemory` 的实现（`Gemini` 与 `Antigravity` 共用） |
| `upstream/qoder/payload.go` 的三个解析器、`stream_conversion.go` 的写出器 | `protocol/qoder`、`codec/qoderchat` 加 `upstream/qoder` 里的一对读取函数 |

### 10.2 实施时容易踩的仓库规则

- 测试文件 `X_test.go` 和 `X_external_test.go` 都必须有同目录的 `X.go`；跨多个源文件的测试用 `<场景>_scenario_test.go`（外部包测试直接在文件内写 `package <pkg>_test`，不要起名 `*_scenario_external_test.go`），文件开头注释列出覆盖的源文件；每个包只有一个 `helpers_test.go`。
- 源文件名不能是 `helper.go`、`util.go`、`common.go` 这类看不出主题的名字，也不能重复包名前缀（注意多词目录同样检查拼接前缀：`generatecontent/` 禁 `generate_content_*.go`，`qoderchat/` 禁 `qoder_chat_*.go`，`mockupstream/` 禁 `mock_upstream_*.go`）；去除空行、注释和 import 后不足 30 行且无独立主题的文件须并入相邻主题文件。
- 手写源文件达到 15 个的包要有 `doc.go`（`doc.go` 正文中出现的任何 `[a-z0-9_]+\.go` 都必须存在于同目录；删除文件时同步更新 `doc.go`）。
- 文件内声明顺序是 import、const、var、type、func，`make fmt` 会重排，重排后检查文件总览注释的位置。`internal/` 下的非测试文件（含 `testkit/cmd/corpus/main.go`）禁止导入标准库 `log`。
- 纯角色的包不能导入 `os`、`embed`、`net/http`。需要读文件的测试通过 `testkit` 间接完成。
- 新包默认只能导入 `internal/protocol` 根包，其余依赖要在 `leafDependencies` 里登记（每个条目的 `Production` 必须包含自身包路径）；`upstream` 各具体平台的依赖需要同时在 `moduleDependencies["internal/upstream"]` 和 `platformDependencies` 里登记。
- `testdata/` 下不放 `.go` 文件；含 CRLF 或行尾空格的 `.sse` 夹具在 `.gitattributes` 标 `-text -whitespace`；未脱敏录制目录 `testdata/corpus/_incoming/` 加入 `.gitignore`；`tools/clientlab/` 避开 `.gitignore` 未锚定的 `CLAUDE.md`、`.claude`、`tests`、`scripts`、`output`、`tmp` 名字。
- 注释和 docstring 用英文；godot 要求顶层声明的文档注释以句号结尾。

### 10.3 常用命令

```bash
# 单个包
(cd backend && go test ./internal/protocol/...)

# 更新 golden
(cd backend && TOKENROUTER_UPDATE_GOLDEN=1 go test ./internal/protocol/...)

# fuzz 一个解码器 10 分钟
(cd backend && go test ./internal/protocol/codec/chat -run '^$' -fuzz FuzzStreamDecode -fuzztime 10m)

# 网关路由测试
(cd backend && go test ./internal/app -run 'TestProtocolLab' -count=1)

# 客户端实验室，脚本模式
make test-clientlab

# 客户端实验室，录制语料
TOKENROUTER_CLIENTLAB=1 TOKENROUTER_CLIENTLAB_MODE=record go -C backend test ./internal/app -run '^TestClientLab' -count=1 -v
(cd backend && go run ./internal/protocol/testkit/cmd/corpus import ./internal/protocol/testkit/testdata/corpus/_incoming)

# 架构、lint 与文档检查
make lint-go
make check-docs
```

### 10.4 bridge 导入方清点

阶段 8 删除 `protocol/bridge` 之前，下面每一组（68 个非测试文件、22 个外部测试文件及 4 个间接调用方）都要有去处。清单取自 `rg -l 'internal/protocol/bridge"' backend`，阶段 7 重新执行一次核对。"去处"一列写的是新路径在哪个阶段接入（都在接线开关后面，无开关的包级函数留到阶段 8 切换）；旧路径对 bridge 的导入在阶段 8 统一删除。

| 导入方 | 用到的内容 | 去处 |
| --- | --- | --- |
| `gateway/httpapi`（15 个文件，其中 `count_tokens_selected.go` 属阶段 3）、`gateway/provider/openaiforward`（6 个文件，含 `passthrough.go`）、`gateway/forward`（3 个文件）、`gateway/provider/messageforward`（1 个文件）、`gateway/provider/grokforward`（1 个文件） | 请求转换、流状态机、`ResponsesClientToolMapping`、原生命名空间处理、`Runtime` | 阶段 2、3、5 随流程合并改用 `convert`；`prelude.go` 和 `response_tool_restoration.go` 原生命名空间与 Compact 仍用的函数在阶段 8 迁至 `protocol/openai`，`CodexToolNameReverse` 原地保留 |
| `upstream/openai`（7 个文件） | 流状态机、`BufferedResponseAccumulator`、`ReconstructResponseOutputFromSSE`、终态输出修复 | 阶段 2 转换路径改用 `convert.Stream`；原生路径仍用的 `BufferedResponseAccumulator`、`ResponsesStreamOutputItems`、`NormalizeResponsesStreamingTerminalOutput`、`ReconstructResponseOutputFromSSE` 在阶段 8 移到 `protocol/openai` |
| `upstream/gemini`（7 个文件）、`gateway/provider/googleforward`（5 个文件，含 2 个 Antigravity 文件）、`gateway/httpapi/textattempt`（1 个原生文件） | Gemini 请求和流转换、用量提取、思考配置、AI Studio 归一化、图片 MIME、`DummyThoughtSignature`、`CompatJSONHasContent` | 阶段 3、4 接入转换路径；原生共用的 `NativeExtractGeminiUsage`、`NativeUsageProjection`、`NativeNormalizeGeminiRequestForAIStudio`、`NativeIsGeminiInlineImageMIMEType`、`DummyThoughtSignature` 在阶段 8 移到 `protocol/gemini` |
| `upstream/antigravity`（9 个文件，加间接调用方 `probe.go`） | `BuildContents`、schema 清洗、流处理器、`GeminiThinkingConfig`（`recovery_claude.go`）、原生流内容检测（`execute.go`） | 阶段 4 合并转换路径并重做 `recovery_claude.go` 与 `probe.go`；`CleanGeminiRequest` 共用的 `CleanJSONSchema`、`DeepCleanUndefined` 阶段 8 移到 `protocol/gemini` |
| `upstream/grok`（2 个文件）、`upstream/client_tools_stream.go`（加间接调用方 `openai_forward_http_adapter.go`）、`gateway/requeststate/response_tools.go`、`gateway/ws/httpapi` | 客户端工具的降级和还原 | 阶段 5 改用 `AdaptResponsesTools` 与 `NewResponsesToolStream`；阶段 8 保留 `response_tools.go` 中的 `CodexToolNameReverse` |
| `upstream/qoder`（3 个文件，加间接调用方 `provider/provider/qoder_test_execution.go`） | 工具声明转换、`CompatOutputMeaning`、三个请求解析器 | 阶段 6 接入 `protocol/qoder` 与 `codec/qoderchat`，阶段 8 切换 `qoder_test_execution.go` |
| `gateway/provider/model_attributes.go`（加间接调用方 `googleforward/model_attributes.go`） | `RequestOptions`、`GeminiThinkingOptions` | 阶段 2、3，换成 `ConversionDialect`（接收 `modelrules.Resolution`） |
| `gateway/provider/input_tokens.go`、`gateway/tokenestimate/estimate.go` | 为估算 token 和官方 `/v1/responses/input_tokens` 预检转换请求 | 阶段 2 写基于 `convert` 的对照函数并测试（发往官方预检时只投影 5 个计数字段并还原信封），阶段 8 切换生产调用点 |
| `gateway/provider/openai_responses_ingress_compat.go` | 把混合 Responses 请求体里旧式的 `messages` 字段转成 `input` | 阶段 2 写基于 `convert` 的 `convertLegacyResponsesMessages` 对照实现做差分，保留外层混合字段合并，阶段 8 切换 |
| `gateway/session/reasoning_history.go` | `ExtractResponsesReasoningItem` | 阶段 8 随 `ReasoningHistory` 删除，不迁移 |
| 外部测试文件（22 个 `*_test.go` 文件） | 旧转换器、`ResponsesClientToolMapping`、`DummyThoughtSignature`、`ReconstructResponseOutputFromSSE` 等 | 阶段 8 随被测旧实现删除，或更新到 `convert` 与迁入 `protocol/{openai,gemini}` 后的新位置 |

## 进度记录

（实施时在此追加：日期、阶段、完成的步骤、局部决策、待确认差异。）

- 2026-11-14，计划修订，尚未开始实施。用户的要求：

  > 请把协议转换重构计划改成一次发布完成。开发和测试可以分阶段，所有现有转换路线（包括 Qoder）验收后再统一切换。旧引擎随同一版本退役，取消双引擎并行和回退开关。
  >
  > 首版直接采用最终的推理签名方案。可以接受旧会话在升级后丢失旧引擎生成的部分转换状态。请按这个原则顺手检查其他类似的兼容要求，比如旧缓存和工具名必须与旧版完全一致，评估哪些可以省掉。正常的客户端协议支持和新引擎下的多轮续聊仍要保证。
  >
  > 保留发布前的基线和差分测试，把有意接受的差异写进验收条件。这次只改计划，先别开始实现。

  改动：新增 2.4（发布方式）和 2.5（版本兼容边界）；3.10 改为首版即信封方案，`Sidecar` 收窄为 `CallMemory`，新增 `PrepareNativeReplay`；3.11 放宽工具名要求；3.13 的迁移开关换成接线开关；6.2.4 改为验收差异清单（D1 到 D10）；阶段 7 改为发布前总验收，阶段 8 改为切换和退役；Qoder 不再可推迟；第 7、8、9、10 节相应调整。

- 2026-11-21，实施前全面审查与修订（基准提交 `164bc487b`），尚未开始写代码。改动要点：
  - 对齐 11 月 14 日后合入主干的代码事实：移除 `AllowDuplicateNames(true)` 并接入 `wirejson` 用量校验；`ir.Usage` 和 `ir.Event` 补齐 30m/12h 缓存写入桶、图片输入与音频/预测明细、`ServiceTier`（含 `ultrafast`）与 `Speed`；`ConversionDialect` 改为接收 `modelrules.Resolution` 并补齐 `StripEffort`、采样限制和按模型族推导的 `ReplayScheme` / `ReasoningHistory`（覆盖 OpenCode、`passback` 模型以及 Antigravity `claude-*` 与 `gemini-*` 拆分）；补齐 Qoder 无状态 Responses 400 门禁、`protocol/qoder` 包及会话/工具选择字段；补齐提前提交 200 SSE 后的终态错误输出、Responses 前导帧暂存窗口、Gemini 5 秒尾帧合并与 `Close(nil)` 终态顺序、Cursor `/v1/chat/completions` 的 `input` 分支。
  - 落实 5 项实施前决策（9.7 节）：阶段 5 同协议客户端工具降级采用 `AdaptResponsesTools` / `NewResponsesToolStream` 轻量适配（覆盖 OpenAI API Key、DeepSeek、Grok、WS 预热续接，保留 `CodexToolNameReverse`）；阶段 2 合并 Messages 转换入口时补齐 Bedrock 签名与 AWS EventStream 解码；`trc1:` 信封增加可选提供商 ID `p` 并对齐 `OpenAIEncryptedContentDigest` 哈希与 `PrepareNativeReplay` 路径扫描；修正 Antigravity `recovery_claude.go` 与 `probe.go` 依赖，具体字段分歧留到阶段 4 差分裁决；无开关包级辅助函数留到阶段 8 切换。
  - 修正架构检查与工具链门禁：`policy.go` 角色判定顺序、`leafDependencies` 自引用、`platformDependencies` 双表登记、`testkit/doc.go`、测试与源文件名门禁、`.gitignore` `_incoming/`、`.gitattributes` `.sse` 豁免、`TOKENROUTER_UPDATE_GOLDEN=1`、`newProtocolLabRouter` 空指针修复，以及阶段 8 迁出 `bridge` 的全部 9 类原生路径符号和 22 个测试文件。
