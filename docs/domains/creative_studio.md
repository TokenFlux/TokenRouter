# 创作台

TokenRouter 创作台（Creative Studio）提供面向个人用户的图片生成、编辑与局部重绘异步任务：浏览器上传素材并创建任务，服务端只保存任务元数据，worker 从 Redis 队列消费任务并调用上游图片模型，结果在 Redis 中短暂留存，由前端取回并保存到浏览器本地。

本文覆盖创作台 API、任务生命周期、幂等、隐藏执行 Key、计费、Redis 临时数据、审核无留存、前端本地存储边界、配置和检查清单。它不定义批量图片作业（见[批量图片作业](batch_image_jobs.md)），不承诺生产价格数值，也不描述上游供应商自己的内容政策。

边界声明（用户与运维都必须理解）：

- 服务端不持久化用户素材：原图、mask、生成图、prompt 明文和 provider 原始响应都不进入 PostgreSQL；prompt 只存 sha256，请求幂等指纹也是 sha256。
- 生成期间服务端临时接收并转发：在服务端侧素材与 prompt 明文只存在于 Redis 临时键，TTL 默认 30 分钟，到期即不可恢复。
- 浏览器可在当前创作台 IndexedDB 中保存画布快照、图片 Blob 和当前表单草稿（包括提示词）；这些数据只留在当前浏览器配置文件，提交任务时才会上传提示词与素材。
- 上游供应商可能有自己的留存策略：素材与 prompt 会按上游 API 要求发送给对应平台，供应商侧的数据边界不受 TokenRouter 控制。
- 断线/过期后结果可能丢失且不算成功：客户端未及时取回输出时任务降级为 `result_lost`，服务端绝不明示成功，也不会从服务端恢复素材；上游已成功但结果丢失的任务仍保持计费。
- 浏览器本地存储不保证永久：输出图片只保存在当前浏览器的 IndexedDB 中，清理站点数据、换浏览器或换设备都会丢失素材，且无跨设备同步。任务历史与详情同时按登录用户和浏览器工作区隔离，不同浏览器不会互相看到任务行。

## 章节导航

- [API 路由](#api-路由)：说明路由、multipart 字段和请求限制。
- [生命周期](#生命周期)：说明任务状态机与 `result_lost` 语义。
- [幂等](#幂等)：说明 Idempotency-Key、请求指纹和部分唯一索引。
- [隐藏执行 Key](#隐藏执行-key)：说明托管 Key 的供应、可见性与级联。
- [计费](#计费)：说明预占、捕获、释放和结算幂等。
- [Redis 临时数据](#redis-临时数据)：说明临时键、TTL、ack 即删和队列协调。
- [审核无留存](#审核无留存)：说明创作台送审的无媒体留存模式。
- [提供商说明](#提供商说明)：说明 openai/grok/gemini 三个平台的执行契约。
- [前端本地存储边界](#前端本地存储边界)：说明 IndexedDB、收割流程和丢失边界。
- [配置](#配置)、[运维检查清单](#运维检查清单)和[安全检查清单](#安全检查清单)：说明运行时启用条件与验证要求。

## API 路由

生产图由 app 直接构造 `creative.Public`、共享的 `creative.Results` 和原生 worker，HTTP 与设置管理使用同一个 Public。账号目录、托管 Key 和订阅读取绑定各模块现有存储，资金动作直接调用唯一 `billing.Funds`；旧公开服务、结果、恢复和 worker 包装已经删除，原规则/状态/并发测试直接验证 creative；隐藏 Key 测试归 apikey。任务执行也直接使用 app 装配的 creative.Executor，旧 CreativeExecutor/CreativeExecution 已删除。provider.Target 负责实际平台分派。app 直接绑定原生选择器与 gateway/provider.CreativeTargets；目标工厂按本次账号绑定 OpenAI/Grok/Gemini 的凭据、代理、Header 和传输，执行时才读取凭据。请求构造、HTTP 池和 Gemini token 源与现有入口共享，不再调用旧网关服务的任务投影。worker 首轮沿用完整设置的批量读取与解析，热更新继续由设置应用器驱动。

新创作任务按操作使用统一协议：OpenAI/Grok 的 generate 对应 Images 生成，edit/inpaint 对应 Images 编辑；Gemini 对应 GenerateContent。目录和提交只提供分组已开放的操作，执行器复用账号原生集合及分组指定转换目标筛选候选。Responses 图片策略不阻断 Images 内部适配。已创建任务的读取、下载和清理仍遵循原资源权限。

创作台路由挂在用户 JWT 面板前缀下（`backend/internal/creative/httpapi/routes_user.go` 与 `backend/internal/app/http_routes_user.go`），响应统一 envelope `{code, message, data}`；`POST /creative/runs` 额外经过面板 heavy 限流：

```text
GET  /api/v1/creative/models
GET  /api/v1/creative/capabilities
POST /api/v1/creative/runs
GET  /api/v1/creative/runs
GET  /api/v1/creative/runs/active?limit=100&cursor=<opaque>
GET  /api/v1/creative/runs/{id}
GET  /api/v1/creative/runs/{id}/outputs/{index}/content
POST /api/v1/creative/runs/{id}/outputs/{index}/ack
```

除 `GET /creative/models` 与 `GET /creative/capabilities` 外，以上任务创建、历史、活动、详情、输出读取和 ack 路由都必须携带 `X-Creative-Workspace-ID` 请求头。请求头必须是非空 UUID；服务端会规范化为小写，缺失返回 `400 CREATIVE_WORKSPACE_REQUIRED`，格式非法返回 `400 CREATIVE_WORKSPACE_INVALID`。工作区 ID 是浏览器数据分区标识，不替代 JWT 用户权限校验；同源标签页共享同一个值，不同浏览器、无痕窗口或清除站点数据后会使用不同值。

`GET /creative/models` 返回当前用户可用分组与图片模型的组合。每项除分组、模型、操作、尺寸和图片单价（`price_512`、`price_1k`、`price_2k`、`price_4k`）外，还按具体模型返回 `aspect_ratios`、`qualities`、`output_formats`、`output_compression`、`background_options`、`thinking_levels`、`max_output_count` 与 `max_reference_images`；不支持的集合返回空数组，压缩范围返回对象或 `null`。创作台不提供输出格式选择，因此 `output_formats` 对所有模型为空数组、`output_compression` 为 `null`，这两个字段仅作为能力协议保留；输出格式由供应商实际返回决定，任务输出 metadata 的 `mime_type` 保留真实 MIME（例如 `image/png` 或 `image/jpeg`），前端按该 MIME 保存和下载。`max_output_count` 固定为 1，创作台每次任务只生成一张图片。`price_512` 仅用于支持 Gemini 512 档位的模型：若渠道配置了 `512` 分层价格则优先使用，否则使用渠道默认价格。前端只按这些服务端能力渲染参数，不根据模型名自行猜测。列表只包含用户可绑定、已启用图片生成、平台支持创作台操作且能解析图片价格的分组。OpenAI 分组支持 `generate`/`edit`/`inpaint`，Gemini（含 Vertex 账号）与 Grok 分组支持 `generate`/`edit`。功能关闭（进程配置 `creative.enabled` 或数据库运行时开关 `creative_enabled` 关闭）时，该接口返回空数组而非错误，前端据此展示"已停用"空态；其余写/读接口返回 404 `CREATIVE_DISABLED`。

管理员还可以在系统设置中配置全局生图模型白名单。`creative_model_settings` 是 `settings` 表中的 JSON 数组，每项精确绑定一个分组和模型，并声明允许的能力：

```json
[
  {"group_id": 123, "model": "gpt-image-2", "operations": ["generate", "edit", "inpaint"]}
]
```

`generate`、`edit`、`inpaint` 分别表示文生图、图生图和局部重绘。空数组（新安装和升级后的默认值）表示创作台没有任何可用生图模型；目录请求和新任务创建都 fail-closed。目录中的能力是管理员配置与平台执行器能力的交集：Gemini/Grok 不会暴露 `inpaint`，管理员保存时也会移除已解析为 Gemini 的旧 `inpaint`，而 OpenAI 的 `inpaint` 保留。配置不绑定外键，分组或账号暂时下线时保留设置，恢复后自动重新生效；已经排队的任务不因后续配置变更取消。

`POST /creative/runs` 接受 `multipart/form-data`，只接受上传文件，不接受远程 URL：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | 文本 | 必填，目标分组 ID |
| `model` | 文本 | 必填，须在该分组可选模型集合内 |
| `operation` | 文本 | 必填，`generate`/`edit`/`inpaint`（小写归一） |
| `prompt` | 文本 | 必填，去空白后非空且不超过 `/creative/capabilities.max_prompt_chars` 个 Unicode code point |
| `source_images` | 文件，可多个 | 源图；字段名兼容 `source_images`、`source_images[]`、`source_images[i]` |
| `mask` | 文件，单个 | 局部重绘蒙版，仅 `inpaint` 允许携带 |
| `image_size` | 文本 | 可选，默认 `1K`；Gemini 3.1 Flash Image 可包含 `512`，其余实际档位以模型目录为准 |
| `aspect_ratio` | 文本 | 可选，须位于模型目录返回的 `aspect_ratios`；缺失时优先使用 `auto`，不支持 `auto` 时使用模型能力第一项 |
| `quality` | 文本 | 可选，OpenAI GPT Image 支持 `low`/`medium`/`high`/`auto`，Grok 仅 `grok-imagine-image-2.0` 支持 `low`/`medium`；支持质量参数时缺失默认 `medium` |
| `background` | 文本 | 可选，OpenAI 支持 `auto`/`opaque`/`transparent`；支持背景参数时缺失默认 `auto` |
| `thinking_level` | 文本 | 可选，Gemini 3.1 图片模型按目录能力支持 `minimal`/`high`；支持思考强度时缺失默认 `minimal` |

请求限制（默认值来自 `creative.*` 配置，可通过 `/creative/capabilities` 获取）：

- 单文件（源图）不超过 `max_asset_bytes`（默认 32 MiB），mask 不超过 4 MiB，单次任务输入总量（全部源图 + mask）不超过 `max_total_input_bytes`（默认 64 MiB）；multipart 使用配置上限加 1 字节检测，不会静默截断。
- 上传文件 MIME 只接受 `image/png`、`image/jpeg`、`image/webp`，缺失或非法时按字节魔数嗅探。
- `edit` 必须至少携带一张源图，源图数量不得超过模型目录返回的 `max_reference_images`；Grok 上限为 3，Gemini 3.1 图片模型为 14，OpenAI GPT Image 为 16。`inpaint` 仅 OpenAI 支持，必须携带源图和 PNG mask，且 mask 尺寸必须与第一张源图一致；非 `inpaint` 操作携带 mask 直接拒绝。
- 除 `group_id` 外的字段缺失或非法返回 `400` 系列业务错误（如 `CREATIVE_INVALID_PARAMS`、`CREATIVE_MASK_REQUIRED`、`CREATIVE_MASK_SIZE_MISMATCH`）；文件或总输入超限返回 `413 CREATIVE_ASSET_TOO_LARGE`/`CREATIVE_INPUT_TOO_LARGE`；分组不可用返回 `403`；余额不足以预占返回 `402 CREATIVE_INSUFFICIENT_BALANCE`；审核命中返回 `403 CREATIVE_CONTENT_BLOCKED`。
- `Idempotency-Key` 请求头可选，最长 255 字符，语义见[幂等](#幂等)；幂等范围为当前用户与当前浏览器工作区。

任务 ID 为 `crun_` 前缀加 16 字节随机 hex。任务公共投影包含 `id`、`status`、`model`、`requested_model`、`operation`、`requested_output_count`、`image_size`、`aspect_ratio`、`output_format`、`group_id`、`estimated_cost`、`hold_amount`、`actual_cost`、错误字段、时间戳和 `outputs` 输出元数据数组（`index`、`status`、`mime_type`、`byte_size`、`transient_expires_at`、`acked_at`）；幂等重放响应额外带 `idempotent_replay=true`。

`GET /creative/runs` 按 `created_at` 倒序返回当前用户当前浏览器工作区的历史任务，支持 `status`、`limit` 查询参数。`GET /creative/runs/active` 使用不透明 cursor 分页，覆盖 `queued`、`running`、`provider_succeeded`、`settlement_pending`、`release_pending`，返回 `{items,next_cursor,has_more}`，不受历史页数量限制。详情、输出 content 和 ack 也要求工作区匹配；工作区不匹配统一返回 `404 CREATIVE_RUN_NOT_FOUND`，不泄露任务是否存在。迁移前 `workspace_id` 为空的旧任务不会返回给任何工作区，也不能读取详情、图片或 ack，但数据库记录保留，后台 worker 仍可完成、结算和清理。

`GET .../outputs/{index}/content` 在临时有效期内返回图片二进制（`Cache-Control: private, no-store`）；输出已 ack、已过期或临时键已丢失时返回 410 语义错误（`CREATIVE_OUTPUT_EXPIRED`/`CREATIVE_RESULT_LOST`），并把仍处 `succeeded` 的任务降级为 `result_lost`，绝不明示成功。

`POST .../outputs/{index}/ack` 用于客户端确认输出已保存到本地：先把输出标记为 `acked`，再删除对应临时输出键，删除失败由 transient reconciler 重试，重复 ack 幂等成功。只有结算完成并进入可交付终态的 run 才能读取/ack 输出。

<a id="creative_task_lifecycle"></a>
## 生命周期

任务状态机（`creative_run_outbox` 持久化 provisioning、settle、release 动作）：

```text
queued -> running -> provider_succeeded -> settlement_pending -> succeeded
queued -> running -> failed/release_pending -> failed
queued -> running -> cancelled/release_pending -> cancelled
queued -> running -> result_lost/release_pending -> result_lost
succeeded -> result_lost
```

`succeeded`、`failed`、`cancelled`、`result_lost` 均为终态。`result_lost` 的语义必须按丢失处理，不能当作成功：

- 客户端在 TTL 内未取回并 ack 输出、worker 发现输入载荷已过期时，任务进入 `result_lost`；服务端不保留、也不能恢复图片本体。
- worker 加载不到 payload 或输入（TTL 过期，provider 未执行）时标记 `result_lost` 并释放预占；上游已确认成功但结果丢失的路径保持计费（见[计费](#计费)）。
- 输出读取路径发现临时输出过期或缺失时，把 `succeeded` 任务降级为 `result_lost`（错误码 `RESULT_EXPIRED`）并返回 410。

worker 从 Redis 预留任务后先读取用户最新并发配置，并通过现有用户并发槽位执行一次非阻塞准入；随后由平台对应的现有账号调度器选择账号并预占账号槽位。两类槽位任一暂时不可用时，任务保持 `queued`，不增加执行次数、不改变计费预占，按约 1 秒短延迟重排以释放 worker。只有用户和账号都准入后才幂等推进 `running`，provider 返回结果后在结算前持久化真实执行账号；执行前检查任务是否已处于 `cancelled`。历史竞态任务若 provider 已成功，仍按实际成功输出捕获费用并记录用量，但终态保持 `cancelled`，绝不回写为 `succeeded`。

执行错误的重试边界：网络层错误、429 与 5xx 视为可重试，按 `max_execute_attempts`（默认 3，含首次）递增尝试并重排；其余 4xx 不可重试直接进入 `release_pending`。provider 成功后先原子记录成功元数据与 outbox，后续只恢复结果保存与 settle/capture/usage log，不重新调用 provider；结算失败保持 `settlement_pending`，绝不 ACK 非终态任务。

## 幂等

- 客户端可对 `POST /creative/runs` 携带 `Idempotency-Key` 头；同一用户同一工作区同一键重放时，若请求指纹一致则直接返回原任务（`idempotent_replay=true`），不重复建单、不重复计费。
- 同一用户同一工作区同一键但请求体不同（指纹不一致）返回 `409 CREATIVE_IDEMPOTENCY_CONFLICT`；不同工作区即使使用相同键也会创建独立任务。
- 请求指纹是规范化 JSON（分组、模型、操作、prompt sha256、各源图 sha256、mask sha256、尺寸、比例、质量、背景、思考强度和固定单张输出）的 sha256；输出格式不是用户输入，不参与指纹，实际输出 MIME 由供应商决定并写入输出 metadata；`creative_runs.request_fingerprint` 只用于比较同一幂等键的请求体，不设置全局唯一约束。
- `(user_id, workspace_id, idempotency_key)` 部分唯一索引只约束带工作区的非空键；迁移前 `workspace_id IS NULL` 的旧任务不参与新的幂等查询，键本身最长 255 字符。
- 计费与结算请求 ID 全部经由 `usage_billing_dedup` 幂等表去重，worker 重试、重复回调不会产生重复资金动作。

## 隐藏执行 Key

创作台任务需要一个 API Key 作为计费与用量归属主体，但用户不应看到或操作它，因此服务端自动供应隐藏执行 Key：

- `api_keys.managed_by` 标记托管来源，CHECK 约束只允许 `'creative_studio'` 或 NULL；任务创建时按用户 + 分组幂等供应（名称 `creative-studio:{group_id}`，`billing_mode` 固定 `auto`，停用分组回退关闭）。
- 普通 Key 列表查询在仓储层过滤 `managed_by IS NULL`；按 ID 的 get/update/delete 命中托管 Key 一律按不存在处理（404 语义），不泄露存在性。
- 创作台写 `usage_logs` 时以隐藏 Key 的 ID 满足 `usage_logs.api_key_id` 非空约束；用户删除账号时任务元数据随 `user_id` 级联删除。

## 计费

创作台资金规则由 `creative.Funding` 拥有，调用唯一 `billing.Funds.Reserve/Capture/Release`。`creative/postgres.FundingParticipant` 使用 billing 本次 SQL 事务写入冻结分配和预记标记；billing 不再选择任务表。app 登记任务 scope 与参与工厂，引用显式携带原预占动作 ID，新字段不加入历史指纹。创作台按所选尺寸基础单价估价，快照订阅/余额倍率；没有批量折扣与账号倍率。质量、背景和思考强度不参与创作台价格计算，输出格式不参与价格计算且不由客户端指定，实际 MIME 以供应商返回为准。每次任务固定只生成一张图片。资金动作的请求 ID 前缀固定，全部经 `usage_billing_dedup` 幂等：

```text
creative_hold:{run_id}      创建任务时预占
creative_capture:{run_id}   成功时按成功输出数捕获
creative_release:{run_id}   失败/内部取消/未执行丢失时释放
creative_settle:{run_id}    写 usage_logs 的结算记录 ID
```

- 创建任务时先估价并冻结（auto 模式先预留订阅额度、只冻结未覆盖部分的钱包余额）；免费分组（估价为 0）跳过资金动作。
- 成功时按实际成功输出数捕获，并写一条 `usage_logs`：`BillingMode` 为 `"image"`，`ImageCount` 为成功输出数，`request_id` 为 `creative_settle:{run_id}`，入站端点记录 `/v1/creative/runs`，上游端点记录 `creative:{operation}`。
- 失败与内部取消状态通过幂等路径释放全部未使用预占；指纹冲突按已释放处理，避免毒消息循环。
- provider 已成功但结果丢失（`result_lost` 且已捕获）时保持计费；payload 过期导致 provider 未执行的 `result_lost` 释放预占。
- 任务执行期间进入 `cancelled` 但 provider 已成功：费用按实际成功输出捕获、用量照写，终态保持 `cancelled`。

单张价格按分组模型价卡、渠道模型价卡、内置按张价格解析；图片/按次价卡使用尺寸分档与显式默认单价；未匹配尺寸且缺少默认单价时回退内置按张价，显式零价保留。token 价卡回退内置按张价格，不按实际 token 结算。资金分配采用普通分组、用户及订阅倍率；既有任务保留创建时快照。本文不定义价格数值。

## Redis 临时数据

输入载荷、源图字节、mask 与输出图片本体只保存在 Redis 临时键，TTL 为 `creative.transient_ttl_seconds`（默认 1800 秒）；PostgreSQL 只存任务与输出元数据。Redis 不可用时创建任务 fail-close 拒绝：

| 键 | 内容 | 清理 |
| --- | --- | --- |
| `creative:payload:{run_id}` | 任务执行载荷 JSON（含 prompt 明文、模型、操作、计数、指纹；图片字节不内联） | TTL 或 `DeleteRunTransient` |
| `creative:input:{run_id}:{idx}` | 单张源图字节 | TTL 或 `DeleteRunTransient` |
| `creative:mask:{run_id}` | mask 字节 | TTL 或 `DeleteRunTransient` |
| `creative:output:{run_id}:{index}` | 单张生成图字节 | TTL、ack 即删或 `DeleteRunTransient` |

输出保存时同时把 `transient_expires_at` 写入输出元数据，客户端据此知道取回截止时间；ack 立即删除对应输出键。`creative.ResultDelivery` 先在 PostgreSQL 的同一事务记录供应商成功时间、实际账号、输出元数据和 settle outbox，再保存 Redis 输出；成功事实不等于可交付成功。保存最多三次、间隔一秒、总预算五秒，停止或租约丢失终止保存。保存耗尽或明确丢失时按已成功图片捕获一次费用并进入 `result_lost`，不会重新推理；Redis 读取故障保留待恢复，不直接当作永久丢失。只有结果可读取且结算完成时才进入 `succeeded`。

队列协调（`creative:queue:*`）与批量图片同构：ready 列表、delayed 有序集合、active 有序集合、单任务 inflight 键（默认 TTL 7 天）、单任务锁键（默认 TTL 300 秒）；入队与预留用 Lua 脚本原子执行。每次领取生成 lease token，心跳、锁续期、重排、ACK 和 stale recovery 都校验 token；失去租约的 worker 取消执行 context，不得写任务、输出、计费或队列状态。`creative_run_outbox` reconciler 负责 provisioning/settle/release 恢复，transient reconciler 负责终态 Redis 清理。队列、临时存储和元数据实现分别位于 `creative/rediscache`、`creative/postgres`，创建与目录位于 `creative.Public`，单次尝试位于 `creative.Executor`，结果推进、恢复与公开查询位于 `creative.Results`/`Queries`，平台请求由 `creative/provider` 使用绑定的技术能力执行。app 固定唯一生产实例，账号目录复用 creative/provider 对原生 Record 的只读适配；公开、结果、worker、账号选择与平台目标都由app直接绑定原生能力。`creative.queue_enabled` 默认开启，应用启动时运行 `creative_worker_count` 个任务 worker（默认 128）、一个 delayed mover、一个 stale active recovery 和两个 reconciler；worker 数量通过管理端功能设置热更新，详见[接口](../interfaces/http_api.md)。

## 审核无留存

创建任务时，prompt 与全部上传图（含 mask）会构造 OpenAI Images 协议报文送内容审核，且必须开启 `ContentModerationCheckInput.NoMediaRetention`：

- 无媒体留存模式下审核日志只保留输入 hash、分类、分数和决策等元数据；不做命中媒体快照，不落 `input_excerpt` 与正文输入项，因此审核记录与 Ops 日志不会保存 base64 图片或 prompt 明文。
- 审核服务自身失败不阻断创作台（fail-open 记日志）；命中阻断时返回 `403 CREATIVE_CONTENT_BLOCKED`。

审核模式、规则优先级与失败语义的通用约定见[内容审核与风险处置](content_moderation.md)。

## 提供商说明

参数能力依据各提供商官方文档维护：[OpenAI Image Generation](https://developers.openai.com/api/docs/guides/image-generation)、[Gemini Generate Content API](https://ai.google.dev/api/generate-content?hl=en)、[Gemini 图片生成](https://ai.google.dev/gemini-api/docs/generate-content/image-generation?hl=en) 和 [xAI Image Generation](https://docs.x.ai/developers/model-capabilities/images/generation)。

执行器按分组平台直接构造上游 HTTP 请求，不经过本地 HTTP 回环；执行超时为 `creative.execute_timeout_seconds`（默认 300 秒）。单张输出不超过 32 MiB，同一任务内按 sha256 去重重复输出：

- `openai`：`generate` 走 `/v1/images/generations`（JSON）；`edit`/`inpaint` 走 `/v1/images/edits`（multipart，多源图 + mask）。内部固定 `output_format: "png"`、单张 `n=1`；仅 DALL-E 路径发送 `response_format: "b64_json"`，GPT Image 路径省略该字段。
- `grok`：`generate` 走 `/v1/images/generations`；`edit` 走 `/v1/images/edits` 的 JSON 契约，单张源图放入 `image: {type: "image_url", url: "data:image/...;base64,..."}`，多张放入 `images` 数组，最多 3 张；两条路径都透传分辨率、比例和 `grok-imagine-image-2.0` 的质量，并固定请求单张 `n=1` 与 `response_format: "b64_json"`；`inpaint` 直接拒绝。
- `gemini`：`generate` 与普通参考图 `edit` 统一使用原生 `generateContent`，prompt 与源图以 inlineData 放入 parts，不发送独立 mask；图片尺寸与比例位于 `generationConfig.imageConfig`，支持的 3.1 图片模型可附加 `generationConfig.thinkingConfig`，`includeThoughts` 固定为 false；执行器取最后一个图片 part 作为最终输出。凭据按账号类型选择：API Key 账号用 `x-goog-api-key`，Vertex 服务账号与 OAuth 用 Bearer token。

当前不暴露无法与异步任务、存储或计费边界稳定对应的上游参数：OpenAI `moderation`、`input_fidelity`、`stream`、`partial_images`，Gemini `includeThoughts`、`temperature`、`topP`、`topK`、`seed`、Google Search grounding 和通用 `candidateCount`，以及任意自定义 OpenAI `WxH` 尺寸。审核策略由服务端统一控制，`gpt-image-2` 固定高保真，Gemini 中间 thought image 固定不返回。

模型候选：Gemini 复用批量图片的账号模型映射展开（含 Vertex），并额外内置 `nano-banana-pro`/`nano-banana-2` 两个代理别名；`nano-banana-*` 别名族按 Gemini 图片模型处理；OpenAI 候选为 `gpt-image-1`/`gpt-image-2`；Grok 候选为 `grok-imagine` 系列。账号未配置模型映射时等价于网关全量透传语义，按平台默认候选回退，并额外纳入账号显式 `model_whitelist` 中匹配图片模型谓词的变体，再执行账号最终模型白名单过滤。尺寸档位只由平台与模型能力决定，不以是否填写单价限制能力；GPT Image 2 开放 `4K`；Gemini 3.1 Flash Image 额外开放 `512`，该档位优先使用分组或渠道价卡的 `512` 分层价格，未配置时回退价卡默认价格；`gemini-2.5-flash-image` 与 `gemini-3.1-flash-lite-image` 固定为 `1K`。接口同时返回按模型广场分组倍率计算的各尺寸展示单价，创作台预估费用按所选尺寸单价计算，每次任务固定单张输出。

管理员候选接口 `GET /api/v1/admin/settings/creative-model-candidates` 返回当前 active、启用图片生成且存在可调度图片模型的全部分组和模型，不按管理员用户分组权限过滤，因此可以配置 exclusive 分组。OpenAI 候选返回 `generate`/`edit`/`inpaint`，Gemini/Grok 候选返回 `generate`/`edit`。

## 前端本地存储边界

前端 `/creative` 页面要求登录，simple 模式隐藏入口，路由带 `requiresCreative` 守卫（公开设置 `creative_enabled === false` 时用户跳 `/dashboard`、管理员跳 `/admin/settings`），侧栏入口由公开设置 `creative_enabled !== false` 门控；模型目录为空时控制面板展示空态文案（功能关闭提示联系管理员，否则提示分组未配置图片生成）。页面是无限画布工作台（左侧面板 + 满幅画布，移动端面板在上、画布在下）：

- 画布交互：空白拖拽平移视角；普通 wheel（包括触控板双指滚动）按跟手方向平移，浏览器标记 `ctrlKey` 的触控板捏合以光标为中心缩放；移动端原生双指手势以两指中点为锚同时缩放/平移，缩放范围统一为 `0.2–3`；图片可点选、拖动、删除；支持从操作系统文件夹拖入 PNG/JPEG/WebP 图片，也支持把历史记录中的本地输出缩略图拖到画布，图片中心对齐拖放落点，多图从落点按固定间距斜向错开；外部拖入绕过裁剪弹窗并保存为源素材，历史拖入复用已有 output 素材；历史默认折叠为画布右上角悬浮列表，点击任务行时若其输出已在本画布上（按 runId + outputIndex 匹配对象 data）则视角平移过去；进行中的任务只显示加载状态，不提供素材操作或取消入口。
- 生成输入：文生图不需要选图；图生图 / 局部重绘以画布当前选中的图片为源图，局部重绘另附画笔导出的 mask（白底透明 PNG，尺寸拉伸回源图自然尺寸）；画笔是唯一画布工具，白色轨迹作为 mask path 画在图片上层。
- 输出上板与历史恢复：全部进行中任务通过 `GET /creative/runs/active` 游标轮询，每 3 秒同步完整 active 集合；历史页只加载任务元数据，输出状态由服务端批量查询。刷新发现的新任务自动加入追踪，终态 run 从 `pollStates` 清理。`succeeded` 时逐个取回未 ack 的输出，先写入 IndexedDB 再调用 ack，下载使用 300 秒超时并对网络/5xx 做有限指数退避，随后自动把输出图片放上画布。进入创作台或手动刷新历史时，对 `succeeded` 且未 ack 的输出执行同一收割流程；本地已有素材时只重试 ack。单个输出取回失败（410/`result_lost`）或本地保存失败只标记该输出缺失，不中断其它输出；ack 失败不抹掉已经保存的本地素材，后续刷新继续重试。
- 本地存储：IndexedDB 库名 `tokenrouter-creative-studio`（版本 1），对象仓库为 `assets`（源图/输出 blob）、`scenes`（画布 JSON 快照，图片 src 以 `asset://<key>` 占位、刷新后回 assets 取 blob 恢复，缺失的图跳过不阻塞）和 `settings`（模型、操作、尺寸、比例、画质、背景、思考强度与提示词草稿）；画布恢复完成前不会执行外部上板或用空画布覆盖已有快照，画布变更防抖约 1 秒存快照，并在页面隐藏/卸载时刷新待写入内容，恢复时重建 runId + outputIndex → 画布对象的注册表；图片绝不以 base64 进入 localStorage。另用 localStorage 的 `creative:workspaceId` 持久化高熵 UUID，同源标签页共享；清空本机创作数据会删除 IndexedDB 内容并旋转工作区 ID，因此旧历史立即隐藏，后续任务进入新工作区。
- 丢失边界：历史自动恢复只适用于服务端仍为 `succeeded`、输出未 ack 且 transient 尚未过期的任务；服务端已 ack、transient 已过期或本地保存失败后仍无对应 blob 的输出显示“素材缺失”。本地配额不足时提示用户下载备份；清理浏览器站点数据会清空全部本地素材并创建新的工作区，且没有任何跨设备同步。
- 幂等重试：创建任务失败重试复用同一 Idempotency-Key，成功后重置。

## 配置

以下配置键定义在 `backend/internal/config/config.go`（默认值与 `deploy/config.example.yaml` 一致）：

```yaml
creative:
  enabled: true                       # 创作台 HTTP API 开关
  queue_enabled: true                 # 创作台队列 worker 开关
  transient_ttl_seconds: 1800         # 输入载荷与临时输出保留时间（秒）
  max_asset_bytes: 33554432           # 源图单文件上传上限（32 MiB）
  max_total_input_bytes: 67108864     # 单次任务输入总量上限（64 MiB）
  max_prompt_chars: 8000              # prompt 最大 Unicode code point 数
  default_response_mime_type: "image/png"  # 供应商未声明输出 MIME 时的默认值
  default_image_size: "1K"
  queue_ready_key: "creative:queue:ready"
  queue_delayed_key: "creative:queue:delayed"
  queue_active_key: "creative:queue:active"
  inflight_key_prefix: "creative:queue:inflight:"
  lock_key_prefix: "creative:queue:lock:"
  inflight_ttl_seconds: 604800
  job_lock_ttl_seconds: 300
  default_requeue_delay_seconds: 30
  error_retry_delay_seconds: 60
  lock_conflict_delay_seconds: 5
  stale_active_after_seconds: 600
  delayed_mover_interval_seconds: 5
  recovery_interval_seconds: 300
  delayed_move_limit: 100
  recover_limit: 100
  execute_timeout_seconds: 300        # 单次上游执行调用超时
  max_execute_attempts: 3             # provider 瞬时错误最大执行次数（含首次）
```

校验约束：`max_total_input_bytes` 不得小于 `max_asset_bytes`；启用队列时所有队列键非空。创作台每次任务固定生成一张图片，按所选尺寸单价预占。与批量图片不同，创作台的 `enabled` 与 `queue_enabled` 默认开启，但缺少 Redis 时任务创建会失败。

除进程配置外，创作台还有数据库运行时开关 `creative_enabled`（默认 true，管理端"功能特性"页可切换，经公开设置下发给前端）以及 `creative_worker_count`（默认 128，要求为正整数，管理端保存后热更新当前 worker 池）：仅当进程配置 `creative.enabled` 与运行时开关同时开启时创作台才可用，管理服务 `enabled()` 判定在请求期读取该开关。创作台不使用 HTTP 网关的进程级图片 limiter；实际执行并发由 worker 池、用户 Redis 并发槽位和账号调度器的账号槽位共同约束。

## 运维检查清单

- 确认 Redis 可用（临时存储与队列都依赖 Redis）。
- 确认 `creative.enabled`、数据库运行时开关 `creative_enabled` 与 `creative.queue_enabled`。
- 确认目标分组启用图片生成；未配置图片尺寸价格或账号模型映射时会按平台默认值回退，GPT Image 2 缺少 4K 覆盖价时仍使用默认价开放 4K。
- 确认上游账号凭据有效（Gemini apikey/Vertex/OAuth、OpenAI、xAI）。
- 确认分组模型价卡与有效倍率，验证估价的 hold/capture/release 行为。
- 明白临时输出默认 30 分钟过期：通知用户及时取回，或按需调大 `transient_ttl_seconds`。
- 排查 `result_lost` 时先检查客户端是否在 TTL 内完成取回与 ack，再检查 worker 日志。

## 安全检查清单

- PostgreSQL 不保存图片字节、mask、prompt 明文或 provider 原始响应；prompt 只存 sha256，幂等指纹同样不可逆。
- 日志与审核记录不含 base64 图片或 prompt 明文（审核走无媒体留存模式）。
- 备份不包含创作台素材本体（素材不在 PostgreSQL 中）；恢复数据库不会恢复 Redis 临时输出。
- 全部用户任务路由按用户 + 浏览器工作区隔离资源归属；隐藏执行 Key 不暴露存在性。
- 内部取消与失败路径释放预占并清理临时键；ack 即删输出。
- 不向客户端泄露上游凭据、代理或 provider 原始响应；错误消息截断到 500 字符并脱敏。

相关 Project Doc：[批量图片作业](batch_image_jobs.md)、[内容审核与风险处置](content_moderation.md)、[路由与结算](routing_and_billing.md)、[HTTP 接口边界](../interfaces/http_api.md)、[配置边界](../interfaces/configuration.md)、[系统架构](../architecture/system_architecture.md)、[可观测性与数据生命周期](../operations/observability_and_data_lifecycle.md)和[领域目录](index.md)。
