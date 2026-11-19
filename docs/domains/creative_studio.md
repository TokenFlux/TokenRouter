# 创作台

TokenRouter 创作台（Creative Studio）提供面向个人用户的图片生成、编辑与局部重绘异步任务。浏览器上传素材并创建任务，服务端只持久保存任务元数据；worker 从 Redis 队列消费任务并调用上游图片模型，结果在 Redis 短暂留存，由前端取回并保存到浏览器本地。

本文覆盖创作台 API、模型与分组策略、生命周期、幂等、隐藏执行 Key、计费、Redis 临时数据、审核无留存、提供商执行、前端本地存储、配置与检查清单。批量图片作业见[批量图片作业](batch_image_jobs.md)；生产价格数值与上游供应商的内容留存政策不在本文范围内。

## 存储与数据留存边界

| 存储位置 | 保存内容 | 留存与恢复语义 |
| --- | --- | --- |
| PostgreSQL | 任务元数据、输出元数据、prompt 与请求指纹的 sha256 | 不存原图、mask、生成图、prompt 明文或 provider 原始响应 |
| Redis 临时键 | 输入载荷（含 prompt 明文）、源图、mask 与输出图片本体 | TTL 默认 30 分钟；ack 或终态清理可提前删除，过期或删除后不可恢复 |
| 浏览器 IndexedDB | 画布快照、图片 Blob、表单草稿及最近 200 次提交的提示词与参数 | 仅留当前浏览器配置文件，无跨设备同步；清理站点数据、换浏览器或设备即丢失 |
| 上游供应商 | 按平台协议提交的 prompt 与源图/mask | 留存策略由上游平台决定，TokenRouter 无法控制 |

客户端未及时取回输出时，任务降级为 `result_lost`（非成功，服务端不可恢复）；上游已成功但结果丢失的任务仍保持计费。任务历史与详情按登录用户和浏览器工作区隔离，不同浏览器互不可见。

## 章节导航

- [API 路由](#api-路由)：路由清单、工作区隔离、白名单、multipart 字段与错误码。
- [模型与分组策略](#creative_model_policy)：模型解析、分组白名单阶段与协议隔离。
- [生命周期](#creative_task_lifecycle)：状态机、worker 准入流程与重试策略。
- [幂等](#幂等)：`Idempotency-Key`、请求指纹与部分唯一索引。
- [隐藏执行 Key](#隐藏执行-key)：托管 Key 供应、隐藏规则与级联删除。
- [计费](#计费)：单价解析、预占/捕获/释放生命周期与边界处理。
- [Redis 临时数据](#redis-临时数据)：临时键、输出交付重试、队列租约与清理任务。
- [审核无留存](#审核无留存)：无媒体留存模式与阻断行为。
- [提供商说明](#提供商说明)：OpenAI、Grok、Gemini 执行协议、参数限制与尺寸档位。
- [前端本地存储](#前端本地存储)：路由守卫、画布与 GIF 交互、输出取回及 IndexedDB 结构。
- [配置](#配置)、[运维检查清单](#运维检查清单)与[安全检查清单](#安全检查清单)：运行时配置、热更新与核查项。

## API 路由

创作台路由挂载在用户 JWT 面板前缀下，响应统一 envelope `{code, message, data}`；`POST /creative/runs` 额外受面板 heavy 限流：

| 路由 | 用途 | 工作区头与关键约束 |
| --- | --- | --- |
| `GET /api/v1/creative/models` | 查询当前用户可用分组与图片模型目录 | 免工作区头；功能关闭时返回空数组 |
| `GET /api/v1/creative/capabilities` | 获取上传字节、prompt 长度等运行时上限 | 免工作区头；功能关闭返回 `404 CREATIVE_DISABLED` |
| `POST /api/v1/creative/runs` | 创建异步创作任务（`multipart/form-data`） | 必填工作区头；经面板 heavy 限流；支持 `Idempotency-Key` |
| `GET /api/v1/creative/runs` | 按 `created_at` 倒序查当前工作区历史 | 必填工作区头；支持 `status`、`limit` |
| `GET /api/v1/creative/runs/active` | 不透明 cursor 分页查进行中任务 | 必填工作区头；`limit=100&cursor=<opaque>`，覆盖 `queued`/`running`/`provider_succeeded`/`settlement_pending`/`release_pending`，返回 `{items,next_cursor,has_more}` |
| `GET /api/v1/creative/runs/{id}` | 查询单个任务详情 | 必填工作区头；工作区不匹配返回 `404 CREATIVE_RUN_NOT_FOUND` |
| `GET /api/v1/creative/runs/{id}/outputs/{index}/content` | 下载临时输出图片二进制 | 必填工作区头；`Cache-Control: private, no-store`；仅已结算终态可读 |
| `POST /api/v1/creative/runs/{id}/outputs/{index}/ack` | 确认输出已存本地并触发删除临时键 | 必填工作区头；先标 `acked` 再删 Redis 键，重复 ack 幂等成功 |

### 工作区隔离与功能开关

- 工作区头 `X-Creative-Workspace-ID`：除 `/models` 与 `/capabilities` 外必填，须为非空 UUID（服务端规范化为小写）。缺失返回 `400 CREATIVE_WORKSPACE_REQUIRED`，格式非法返回 `400 CREATIVE_WORKSPACE_INVALID`。
- 隔离语义：工作区 ID 仅作浏览器数据分区，不替代 JWT 鉴权。详情、下载与 ack 校验工作区归属，不匹配统一返回 `404 CREATIVE_RUN_NOT_FOUND` 以防探测存在性。迁移前 `workspace_id` 为空的旧任务对所有工作区不可见，但保留数据库记录供后台 worker 完成、结算与清理。
- 功能关闭：进程配置 `creative.enabled` 或数据库开关 `creative_enabled` 任一关闭时，`GET /creative/models` 返回空数组（前端展示“已停用”空态），其余接口返回 `404 CREATIVE_DISABLED`。

### 模型目录与管理员白名单

`GET /creative/models` 返回当前用户可绑定、启用图片入口且能解析精确图片单价的分组与模型组合：

- 能力字段：包含分组、模型、操作、尺寸、各档单价（`price_512`/`price_1k`/`price_2k`/`price_4k`）、`aspect_ratios`、`qualities`、`output_formats`、`output_formats_by_operation`、`output_compression`、`background_options`、`thinking_levels`、`max_output_count`（固定 1）与 `max_reference_images`。
- 能力渲染约定：不支持的集合返回空数组，压缩范围返回对象或 `null`；前端严格按服务端返回的能力渲染参数，不按模型名猜测。
- 输出格式与 GIF：默认请求 PNG。`output_formats_by_operation` 按已开放的 `generate`/`edit`/`inpaint` 返回可用格式，仅存在可用 GIF 路线时提供 GIF（GIF 仅支持 `generate` 与静态参考图 `edit`，`inpaint` 保持 PNG）。
- 旧版格式字段兼容：`output_formats` 保留合并选项（任一操作支持 GIF 时返回 PNG/GIF 及 `gif_frame_count`、`gif_frame_duration_ms` 范围，否则空列表）。输出元数据 `mime_type` 与 `frame_count` 以实际结果为准。
- 价格与平台操作：`price_512` 仅用于 Gemini 512 档位模型（价卡 `512` 分层价优先于默认单价，再回退内置按张价）。按张价须来自完整型号的手动价卡、目录或静态价，缺价不用通用单价顶替，提交在预占前拒绝；手动零价有效。OpenAI 支持 `generate`/`edit`/`inpaint`，Gemini（含 Vertex）与 Grok 支持 `generate`/`edit`。
- 全局白名单 `creative_model_settings`：存放在 `settings` 表的 JSON 数组，每项绑定 `group_id`、`model` 与 `operations`（如 `{"group_id": 123, "model": "gpt-image-2", "operations": ["generate", "edit", "inpaint"]}`）。新安装与升级默认为空数组（fail-closed）。
- 白名单生效规则：目录能力取白名单与平台能力的交集（Gemini/Grok 不暴露 `inpaint`，保存时移除已解析为 Gemini 的旧 `inpaint`）。设置不绑外键，分组或提供商暂时下线仍保留；变更不取消已排队任务，但执行时仍校验当前分组策略。

### 创建任务字段、限制与错误码

`POST /creative/runs` 仅接受 `multipart/form-data` 上传文件，拒绝远程 URL：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | 文本 | 必填，目标分组 ID |
| `model` | 文本 | 必填，须在该分组可选模型集合内 |
| `operation` | 文本 | 必填，`generate` / `edit` / `inpaint`（小写归一） |
| `prompt` | 文本 | 必填，去空白后非空，不超过 `/creative/capabilities.max_prompt_chars` 个 Unicode 码点 |
| `source_images` | 文件（可多个） | 源图；兼容 `source_images`、`source_images[]`、`source_images[i]` |
| `mask` | 文件（单个） | 局部重绘蒙版，仅 `inpaint` 允许携带 |
| `image_size` | 文本 | 可选，默认 `1K`；Gemini 3.1 Flash Image 可含 `512`，其余以目录为准 |
| `aspect_ratio` | 文本 | 可选，须在目录 `aspect_ratios` 内；缺失优先 `auto`，否则取第一项 |
| `quality` | 文本 | 可选，OpenAI GPT Image 支持 `low`/`medium`/`high`/`auto`，Grok 仅 `grok-imagine-image-2.0` 支持 `low`/`medium`；支持时默认 `medium` |
| `background` | 文本 | 可选，OpenAI 支持 `auto`/`opaque`/`transparent`；支持时默认 `auto` |
| `output_format` | 文本 | 可选，默认 `png`；可用 GIF 路线可选 `gif` |
| `frame_count` | 整数 | 仅 GIF，2 至 16，默认 9 |
| `frame_duration_ms` | 整数 | 仅 GIF，20 至 2000 毫秒，默认 200，按 10 毫秒舍入 |
| `thinking_level` | 文本 | 可选，Gemini 3.1 图片模型支持 `minimal`/`high`；支持时默认 `minimal` |

上传限制与响应结构：

- 体积上限：单张源图不超过 `max_asset_bytes`（默认 32 MiB），mask 不超过 4 MiB，总输入（全部源图 + mask）不超过 `max_total_input_bytes`（默认 64 MiB）。服务端按上限多读 1 字节判断是否超限。
- MIME 与参考图数量：仅接受 `image/png`、`image/jpeg`、`image/webp`，缺失或非法时按字节魔数嗅探。`edit` 至少 1 张源图，数量上限由目录 `max_reference_images` 决定（Grok 3、Gemini 3.1 14、OpenAI GPT Image 16）。
- 蒙版校验：`inpaint` 仅 OpenAI 支持，须带源图与 PNG mask 且 mask 尺寸须与首张源图一致；非 `inpaint` 携带 mask 直接拒绝。
- 任务响应字段：任务 ID 为 `crun_` + 16 字节随机 hex，返回 `id`、`status`、`model`、`requested_model`、`operation`、`requested_output_count`、`image_size`、`aspect_ratio`、`output_format`、`group_id`、`estimated_cost`、`hold_amount`、`actual_cost`、错误字段与时间戳。
- 输出元数据与重放：`outputs` 数组包含 `index`、`status`、`mime_type`、`byte_size`、`transient_expires_at`、`acked_at`；幂等重放响应额外附带 `idempotent_replay=true`。

主要业务错误码：

| HTTP 状态 | 错误码 | 触发场景 |
| --- | --- | --- |
| 400 | `CREATIVE_WORKSPACE_REQUIRED` / `CREATIVE_WORKSPACE_INVALID` | 缺失或非法的 `X-Creative-Workspace-ID` |
| 400 | `CREATIVE_INVALID_PARAMS` / `CREATIVE_INVALID_MODEL` / `CREATIVE_MASK_REQUIRED` / `CREATIVE_MASK_SIZE_MISMATCH` | 参数缺失非法、模型含通配符或不可用、mask 缺失或尺寸不符 |
| 402 | `CREATIVE_INSUFFICIENT_BALANCE` | 余额不足以完成预占 |
| 403 | `CREATIVE_GROUP_FORBIDDEN` / `CREATIVE_CONTENT_BLOCKED` / 分组不可用 | Claude Code 专用分组、内容审核命中、用户无权访问分组 |
| 404 | `CREATIVE_DISABLED` / `CREATIVE_RUN_NOT_FOUND` | 功能已关闭，或任务不存在/工作区不匹配 |
| 409 | `CREATIVE_IDEMPOTENCY_CONFLICT` | 同用户同工作区复用 `Idempotency-Key` 但请求指纹不同 |
| 410 | `CREATIVE_OUTPUT_EXPIRED` / `CREATIVE_RESULT_LOST` | 输出已 ack、已过期或临时键丢失（同时把 `succeeded` 降级为 `result_lost`） |
| 413 | `CREATIVE_ASSET_TOO_LARGE` / `CREATIVE_INPUT_TOO_LARGE` | 单文件或总输入超过字节上限 |

<a id="creative_model_policy"></a>
## 模型与分组策略

目录查询、任务提交与后台执行共用同一套网关提供商与分组策略规则（无共享价格配置时规则仍生效），完整分组策略见[分组独立策略](gateway_policy_controls.md#group_routing_policy)：

1. 候选生成与目录校验：从提供商白名单、映射（源与目标）、平台执行规则、分组策略及已保存创作台配置汇总有限候选，通配符在这些有限候选中匹配。请求型号依次经分组映射、提供商映射与平台规范化，最终型号必须明确声明 image 输出（未知模态不按名称猜测，未知尺寸不生成档位）并通过协议与白名单检查。
2. 提交校验与白名单保存：提交时直接验证用户选中的具体型号。含 `*` 或 `?` 的设置项视为无法解析，目录自动过滤、提交返回 `CREATIVE_INVALID_MODEL`。保存多项设置时同分组共用一次分组与提供商读取，综合设置更新保护覆盖此校验，无法解析的历史设置按原样保留。
3. 执行前复核与模型固定：每次任务执行前深拷贝最新分组策略副本（读取失败停止本次执行，副本不改分组原数据）。调度器收原请求模型，执行器把已解析的分组模型交给所选提供商并复核实际发送上游模型的白名单，通过后固定执行模型，组包时不再重复映射；透传提供商同样按实际发送模型检查。

协议路由、分组隔离与快照规则：

- 协议映射：OpenAI/Grok 的 `generate` 走 Images 生成，`edit`/`inpaint` 走 Images 编辑；Gemini 走原生 `GenerateContent`。Responses 图片策略不阻断 Images 内部适配。
- 分组隔离：Claude Code 专用分组不进目录，提交在创建托管 Key 与预占前返回 `CREATIVE_GROUP_FORBIDDEN`。创作台不走客户端限制回退；worker 只用任务持久化的分组与 `provider`，原组受限时停止执行。已创建任务的读取、下载与清理继续按原资源权限放行。
- 供应商选择：同一公开名映射多供应商时，按 OpenAI -> Gemini -> Grok 稳定顺序取首个可用路线（需暴露不同能力时应配独立别名）。创建时记录目录对应的 `provider`，调用上游前写入实际提供商 ID 与 `provider`；迁移 278 已回填历史任务，未绑定的旧排队任务在准备时按候选能力选取。
- 价格与策略快照：策略与价格分别读取，已提交任务保留创建时的价格与资金快照，不因执行前策略复核重算历史金额。
- 分组白名单阶段：按 `requested`、`group_mapped` 或 `upstream` 阶段检查；开启限制且名单为空时拒绝全部型号；关闭分组策略后，草稿在重新启用前保持停用。

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

终态与降级语义：

| 状态 | 性质 | 计费与恢复语义 |
| --- | --- | --- |
| `succeeded` | 终态 | 结果已存 Redis 且结算完成，可下载与 ack |
| `failed` | 终态 | 不可重试错误或重试耗尽，释放全部预占 |
| `cancelled` | 终态 | 内部取消释放预占；若竞态下 provider 已成功，仍捕获费用并写用量，终态保持 `cancelled` |
| `result_lost` | 终态（非成功） | payload 过期未执行时释放预占；provider 已成功但结果保存/取回丢失（含 410 降级，错误码 `RESULT_EXPIRED`）保持计费，服务端无法恢复图片 |

worker 执行与准入顺序：

1. 预留任务并校验租约，读取用户最新并发配置，通过用户并发槽位做一次非阻塞准入。
2. 由平台对应的提供商调度器选择提供商并预占提供商槽位。用户或提供商任一槽位暂不可用时，任务保持 `queued`、不计入尝试次数、不改计费预占，按约 1 秒短延迟重排以释放 worker。
3. 两类槽位均准入后，检查任务是否已 `cancelled`，再幂等推进 `running`，调用上游前持久化实际提供商与 `provider`（凭据在执行时按本次提供商实时读取）。
4. 调用上游执行生图。
5. 上游成功后，原子记录成功元数据、输出元数据与 `settle` outbox，再保存 Redis 输出并推进结算。

重试与错误边界：

- 可重试错误：网络层错误、429 与 5xx 按 `max_execute_attempts`（默认 3，含首次）递增尝试并重排。
- 不可重试错误：其余 4xx、OpenAI 仅流式端点（`stream=true` + `partial_images=0`）的流错误/缺终态图/转换失败，直接进入 `release_pending`，不重新提交生图。
- 单次上游调用保证：provider 一旦成功并写库，后续恢复只涉及结果保存、settle/capture 与 `usage_logs`，绝不再次调用模型；结算失败保持 `settlement_pending`；任务到达终态前不 ACK 队列消息。

## 幂等

- 请求幂等：`POST /creative/runs` 可选携带 `Idempotency-Key`（最长 255 字符），作用域为当前用户 + 当前工作区。同键且请求指纹一致时直接返回原任务（`idempotent_replay=true`），不重复建单和计费；指纹不一致返回 `409 CREATIVE_IDEMPOTENCY_CONFLICT`。不同工作区使用同键互不干扰。
- 请求指纹构成：对规范化 JSON（分组、模型、操作、prompt sha256、各源图 sha256、mask sha256、尺寸、比例、质量、背景、思考强度与固定单张输出）做 sha256。`creative_runs.request_fingerprint` 无全局唯一约束。
- GIF 指纹兼容：GIF 请求额外包含规范化后的输出格式、帧数和帧间隔；默认或显式 PNG 省略这三个字段以兼容历史静态任务。实际返回的 MIME 和帧数写入输出元数据，不改写请求指纹。
- 索引与计费去重：部分唯一索引 `(user_id, workspace_id, idempotency_key)` 仅约束带工作区的非空键，迁移前 `workspace_id IS NULL` 的旧任务不参与新幂等查询。全部资金与结算请求经 `usage_billing_dedup` 去重。

## 隐藏执行 Key

创作台需要 API Key 作为计费与用量归属主体，但对用户完全不可见：

- 托管供应：`api_keys.managed_by` 的 CHECK 约束仅允许 `'creative_studio'` 或 `NULL`。创建任务时按用户 + 分组幂等供应隐藏 Key（名称 `creative-studio:{group_id}`，`billing_mode` 固定 `auto`，停用分组回退关闭）。
- 可见性隔离：普通 Key 列表过滤 `managed_by IS NULL`；按 ID 的 get/update/delete 命中托管 Key 时按不存在返回 404，不泄露存在性。
- 用量与级联：写 `usage_logs` 时使用隐藏 Key ID 满足 `usage_logs.api_key_id` 非空约束；删除用户关联时任务元数据随 `user_id` 级联删除。

## 计费

创作台按所选尺寸基础单价估价并快照普通分组、用户与订阅/余额倍率，不含批量折扣和提供商倍率。资金操作与任务冻结分配、预记标记在同一 SQL 事务中原子写入（资金引用中携带的原预占动作 ID 不加入历史指纹）。

### 单价与帧数规则

- 价格解析顺序：先查共享价格配置价卡（图片/按次价卡的尺寸分档 -> 该价卡手动默认单价），未命中回退内置按张价格。手动填写的零价按零价生效；仅有 token 价卡时也回退内置按张价，不按实际 token 结算。
- 排除参数与 GIF 计费：质量、背景、思考强度不参与价格计算。静态图按单张价计费；GIF 按单帧价乘帧数（提交按请求帧数预占，结算按实际验证帧数捕获）。上游返回的有效 GIF 帧数与请求不同仍接受，请求范围不限制结果帧数。`image_count` 记输出文件数（固定 1），`image_frame_count` 单记动画计费帧数，实际 MIME 以供应商返回为准。

### 资金动作与边界处理

| 请求 ID 前缀 | 触发时机 | 行为与边界处理 |
| --- | --- | --- |
| `creative_hold:{run_id}` | 创建任务时 | `auto` 模式先预留订阅额度、仅冻结未覆盖的钱包余额；免费分组（估价 0）跳过 |
| `creative_capture:{run_id}` | 上游成功结算时 | 按成功输出数（GIF 按实际帧数）捕获；实际帧数更少则释放多余预占 |
| `creative_release:{run_id}` | 失败、内部取消或未执行丢失时 | 幂等释放全部未使用预占；指纹冲突按已释放处理，避免无限重试 |
| `creative_settle:{run_id}` | 写入 `usage_logs` 时 | `BillingMode="image"`、`ImageCount` 为成功数、`billing_key` 为本值、入站 `/v1/creative/runs`、上游 `creative:{operation}` |

关键计费边界：

- GIF 超额补扣：实际 GIF 费用高于预占时（含请求 PNG 但上游返回有效 GIF），先耗尽保存的订阅与余额预占，剩余基础费用按任务保存的余额倍率补扣。订阅不超扣，余额不足记为欠费且结果照常交付；余额、Key 用量、去重表与预记标记同事务更新，补扣失败整体回滚。恢复时用原价格快照与持久化实际帧数，不重调上游；静态任务与未超量 GIF 的资金指纹保持兼容。
- 终态与异常计费：provider 已成功但进入 `result_lost`（已捕获）或竞态 `cancelled` 时，均保持按实际成功输出计费并写用量；payload 过期导致 provider 未执行的 `result_lost` 释放预占。

## Redis 临时数据

输入载荷、源图、mask 与输出图片本体仅存于 Redis 临时键，TTL 为 `creative.transient_ttl_seconds`（默认 1800 秒）。Redis 不可用时创建任务 fail-closed 拒绝：

| 键 | 内容 | 清理方式 |
| --- | --- | --- |
| `creative:payload:{run_id}` | 执行载荷 JSON（含 prompt 明文、模型、操作、计数、指纹；不含图片字节） | TTL 或 `DeleteRunTransient` |
| `creative:input:{run_id}:{idx}` | 单张源图字节 | TTL 或 `DeleteRunTransient` |
| `creative:mask:{run_id}` | mask 字节 | TTL 或 `DeleteRunTransient` |
| `creative:output:{run_id}:{index}` | 单张生成图字节 | TTL、ack 即删或 `DeleteRunTransient` |

### 输出交付、队列协调与后台清理

- 输出写入事务顺序：先在 PostgreSQL 单事务记录供应商成功时间、实际提供商、输出元数据（含 `transient_expires_at` 上限）和 `settle` outbox，再保存 Redis 输出（最多 3 次、间隔 1 秒、总预算 5 秒；进程停止或失租立即中止）。
- 输出丢失与恢复：保存重试耗尽或确认丢失时，按成功图片计一次费并转 `result_lost`，不重调模型；Redis 读取故障时任务保持待恢复；仅输出可读且结算完成后才进入 `succeeded`。
- 队列与租约：`creative:queue:*` 与批量图片同构，含 ready 列表、delayed/active 有序集合、inflight 键（TTL 7 天）和锁键（TTL 300 秒），入队与预留经 Lua 原子执行。每次领取生成 lease token，失租 worker 立即取消执行 context 并停止写任何状态。
- 后台 worker 池：`creative.queue_worker_enabled` 默认开启，启动 `creative_worker_count`（默认 128）个 worker、1 个 delayed mover、1 个 stale active recovery 和 2 个 reconciler（outbox 与 transient）；worker 数量可通过管理端热更新，见[HTTP 接口](../interfaces/http_api.md)。
- 清理任务职责：`creative_run_outbox` reconciler 恢复 provisioning/settle/release；transient reconciler 每 5 分钟扫描终态任务，删除 `completed_at` 距今至少 10 分钟的临时键（即使未 ack 且在 TTL 内），实际删除受轮询、批次上限与失败重试影响。

## 审核无留存

创建任务时，prompt 与全部上传图（含 mask）构造为 OpenAI Images 协议报文送内容审核，且强制开启 `ContentModerationCheckInput.NoMediaRetention`：

- 无媒体留存：审核日志仅记输入 hash、类别、分数与决策元数据，不做命中媒体快照，不保存 `input_excerpt` 与正文输入项，避免审核记录和 Ops 日志留存 base64 图片或 prompt 明文。
- 失败与阻断：审核服务自身异常 fail-open（记日志不阻断创作台）；命中拦截策略返回 `403 CREATIVE_CONTENT_BLOCKED`。

通用审核模式与优先级规则见[内容审核与风险处置](content_moderation.md)。

## 提供商说明

参数能力参照官方文档维护：[OpenAI Image Generation](https://developers.openai.com/api/docs/guides/image-generation)、[Gemini Generate Content API](https://ai.google.dev/api/generate-content?hl=en)、[Gemini 图片生成](https://ai.google.dev/gemini-api/docs/generate-content/image-generation?hl=en)、[xAI Image Generation](https://docs.x.ai/developers/model-capabilities/images/generation)。

执行器直接向本次提供商发起上游 HTTP 请求（不走本地 HTTP 回环），执行超时 `creative.execute_timeout_seconds`（默认 300 秒），单张输出上限 32 MiB，同任务内按 sha256 去重：

| 平台 | 端点与协议 | 关键参数与凭据 | 输出解析与限制 |
| --- | --- | --- | --- |
| `openai` | `generate` 走 `/v1/images/generations`（JSON）；`edit`/`inpaint` 走 `/v1/images/edits`（multipart，多源图 + mask） | 默认 `output_format: "png"`、`n=1`；GIF 传格式/帧数/帧间隔；仅 DALL-E 传 `response_format: "b64_json"`，GPT Image 省略；仅流式端点传 `stream=true` 与 `partial_images=0` | 优先解码 Base64，否则下载 URL（静态图最多 3 次，失败 `IMAGE_DOWNLOAD_FAILED`；GIF 经共享读取器下载验证，失败或无可解析图返回 `INVALID_IMAGE_RESPONSE`）；两类错均终止任务、释放预占、不重调生图；下载限制与凭据隔离见 [OpenAI 上游](../interfaces/openai_upstream.md#images_url_backfill) |
| `grok` | `generate` 走 `/v1/images/generations`；`edit` 走 JSON 格式 `/v1/images/edits` | 单源图用 `image: {type: "image_url", url: "data:image/...;base64,..."}`，多源图用 `images` 数组（最多 3 张）；透传分辨率、比例与 `grok-imagine-image-2.0` 质量；固定 `n=1` 与 `response_format: "b64_json"` | 拒绝 `inpaint` |
| `gemini` | `generate` 与参考图 `edit` 统一走原生 `generateContent` | prompt 与源图经 `inlineData` 放入 `parts`（不传独立 mask）；尺寸比例在 `generationConfig.imageConfig`；3.1 模型可附 `thinkingConfig` 且 `includeThoughts=false`；API Key 用 `x-goog-api-key`，Vertex/OAuth 用 Bearer token | 取最后一个图片 part 为最终输出；不支持 `inpaint` |

能力边界与管理员候选：

- 未开放上游参数：OpenAI `moderation`/`input_fidelity`/`stream`/`partial_images`/自定义 `WxH`；Gemini `includeThoughts`/`temperature`/`topP`/`topK`/`seed`/Search grounding/`candidateCount`。审核由服务端控制，`gpt-image-2` 固定高保真，Gemini 不返回中间 thought image。
- 尺寸档位与自定义别名：可选尺寸仅由平台和模型能力决定，与是否配覆盖价无关。GPT Image 2 开放 `4K`，Gemini 3.1 Flash Image 开放 `512`（优先价卡 `512` 分层价，否则价卡默认价），`gemini-2.5-flash-image` 与 `gemini-3.1-flash-lite-image` 固定 `1K`。自定义别名按最终上游型号判定，目录外型号可由管理员配置加入。
- 管理员候选接口：`GET /api/v1/admin/settings/creative-model-candidates` 返回所有 active、启用图片生成且有可调度图片模型的分组与模型，不按管理员分组权限过滤（支持配置 exclusive 分组）。OpenAI 返 `generate`/`edit`/`inpaint`，Gemini/Grok 返 `generate`/`edit`。

## 前端本地存储

### 路由守卫与画布交互

- 入口与守卫：`/creative` 要求登录并带 `requiresCreative` 守卫（`creative_enabled === false` 时用户跳 `/dashboard`、管理员跳 `/admin/settings`），侧栏由 `creative_enabled !== false` 控制。目录为空时提示功能关闭或分组未配置图片生成。
- 无限画布布局：顶部居中工具条、左上设置菜单、右上历史入口三块同高浮层；底部居中输入框，顶部状态行展示错误、最近失败、缺少的画布输入或进行中任务的阶段与计时；空画布中央展示上传入口与示例提示词。
- 视图与拖放：空白拖拽平移；普通滚轮/双指滚动跟手平移，`ctrlKey` 触控板捏合以光标缩放，移动端原生双指以中点缩放/平移，缩放范围 `0.2–3`。图片可点选、拖动、删除；支持从系统拖入 PNG/JPEG/WebP（绕过裁剪存为源素材）或从历史拖入本地缩略图（复用已有输出素材），多图自落点斜向错开。
- 历史侧栏与任务行：默认收起在右上角；`md` 起停靠画布右侧并将顶部工具条、空画布指南与输入框居中于左侧剩余区域；窄屏带遮罩覆盖并在导入画布后自动收起。任务行模型名下显两行提示词摘要，点击原地展开完整提示词、参数标签、复制与“填入输入框”（模型下线保留当前模型，超能力参数回默认）及输出导入/下载；进行中任务仅显加载态，无素材操作与取消按钮。
- 生成输入与蒙版：文生图无需选图；图生图与局部重绘以当前选中图为源图，画布工具含上传、下载选中、框选、局部重绘画笔组与删除选中。局部重绘笔迹绘于独立 mask 画布（半透明紫色），导出为白底透明 PNG 并拉伸回源图自然尺寸。

### GIF 封面与播放

- 存储与画布表现：原始 GIF 字节存于 IndexedDB，历史列表直接动画播放；画布默认显独立 PNG 封面，选中后居中显播放按钮，点击转圈解码并按实际帧间隔循环播放，缩小后的停止按钮移至左上角（拖动、缩放、旋转与层级保持不变）。
- 释放与限制：停止、取消选中、移除、页面隐藏或离开创作台时恢复封面并释放播放资源。下载保留原始 GIF；GIF 不可作为编辑、裁剪或蒙版重绘输入，防止隐式转静态图。格式、帧数与帧间隔随草稿和历史参数保存。

### 输出取回、余额同步与幂等

- 轮询与取回：每 3 秒经 `GET /creative/runs/active` 游标同步进行中任务，历史页仅拉元数据与批量输出状态，终态从 `pollStates` 清理。`succeeded` 任务拉取未 ack 输出（300 秒超时、网络/5xx 指数退避），先写 IndexedDB 再调 ack 并自动上画布。
- 刷新恢复与容错：进入页面或手动刷新对未 ack 的 `succeeded` 输出走同一取回流程（本地已有只重试 ack）。单输出 410/`result_lost` 或本地写入失败仅标该输出缺失；ack 失败保留本地素材待后续重试。
- 余额与幂等重试：任务新增、状态变化或移出活动列表时，串行刷新共享用户资料以更新顶栏余额与冻结额（排序与 ack 变化不触发，与图片下载独立，失败仅记日志）。创建失败重试复用同一 `Idempotency-Key`，成功后重置。

### IndexedDB 结构与数据丢失边界

- 数据库对象仓库：IndexedDB `tokenrouter-creative-studio`（v1）含 `assets`（源图/输出 Blob）、`scenes`（画布 JSON 快照，以 `asset://<key>` 占位，恢复时缺失图跳过并重建 `runId + outputIndex -> 画布对象` 映射）和 `settings`（模型/参数/提示词草稿及 `run:<runId>` 最近 200 条提交快照）。
- 快照刷盘与安全边界：画布恢复完成前不执行外部上板或覆写快照，变更防抖约 1 秒并在页面隐藏/卸载时刷盘；图片绝不以 base64 进入 localStorage。
- 工作区与清空：`localStorage` 的 `creative:workspaceId` 存高熵 UUID 供同源标签共享；清空本机数据会删除 IndexedDB 并旋转工作区 ID，旧历史立即隐藏，新任务进入新工作区。
- 历史参数来源：操作、尺寸、比例与输出张数取服务端字段；提示词、分组名、画质、背景、思考强度与参考图张数仅取本机 `run:<runId>`（发请求前采样、创建成功后写入，读写失败仅记日志；缺失记录显示“本机没有这次任务的提示词记录”）。
- 丢失条件：自动恢复仅适用于服务端 `succeeded`、未 ack 且 transient 未过期的输出；已 ack、已过期或本地写入失败且无 Blob 的输出显示“素材缺失”。本地配额不足提示下载备份；清理站点数据将清空全部素材并新建工作区，无跨设备同步。

## 配置

进程配置定义在 `backend/internal/config/runtime_domains.go`（默认值见 `backend/internal/config/defaults.go` 与 `deploy/config.example.yaml`）：

```yaml
creative:
  enabled: true                       # 创作台 HTTP API 开关
  queue_worker_enabled: true          # 创作台队列 worker 开关
  transient_ttl_seconds: 1800         # 输入载荷与临时输出保留时间（秒）
  max_asset_bytes: 33554432           # 源图单文件上传上限（32 MiB）
  max_total_input_bytes: 67108864     # 单次任务输入总量上限（64 MiB）
  max_prompt_chars: 8000              # prompt 最大 Unicode code point 数
  default_image_size: "1K"
  execute_timeout_seconds: 300        # 单次上游执行调用超时
  max_execute_attempts: 3             # provider 瞬时错误最大执行次数（含首次）
  queue:
    ready_key: "creative:queue:ready"
    delayed_key: "creative:queue:delayed"
    active_key: "creative:queue:active"
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
```

配置与并发约束：

- 校验规则：`max_total_input_bytes` 不得小于 `max_asset_bytes`；启用队列时 `creative.queue` 下全部键非空。`enabled` 与 `queue_worker_enabled` 默认开启，但缺少 Redis 时任务创建失败。
- 运行时开关：数据库 `creative_enabled`（默认 `true`，管理端“功能特性”页切换，经公开设置下发）须与 `creative.enabled` 同时开启才可用，请求期读取判定；`creative_worker_count`（默认 128，正整数）保存后热更新 worker 池。
- 并发控制：创作台不使用 HTTP 网关的进程级图片 limiter，并发由 worker 池、用户 Redis 并发槽位与提供商调度槽位共同约束。

## 运维检查清单

- 确认 Redis 可用（临时存储与队列均依赖 Redis）。
- 确认 `creative.enabled`、数据库开关 `creative_enabled` 与 `creative.queue_worker_enabled` 已启用。
- 确认目标分组已启用图片生成；未配尺寸价时按完整型号查目录价，提供商模型映射按已保存配置执行，GPT Image 2 无 4K 覆盖价时仍以默认价开放 4K。
- 确认上游凭据有效（Gemini API Key / Vertex / OAuth、OpenAI、xAI）。
- 确认价格配置价卡与分组有效倍率，验证 hold / capture / release 行为。
- 临时输出默认 TTL 30 分钟，终态清理最早在完成 10 分钟后提前删除；调大 `transient_ttl_seconds` 仅延长 Redis 过期上限，客户端应及时取回。
- 排查 `result_lost` 时先核对取回与 ack 时机、Redis TTL 与终态清理，再查 worker 日志。

## 安全检查清单

- PostgreSQL 不存图片字节、mask、prompt 明文或 provider 原始响应；prompt 与幂等指纹仅存不可逆 sha256。
- 日志与审核记录不含 base64 图片或 prompt 明文（审核强制开启无媒体留存）。
- 数据库备份不含创作台素材本体；恢复数据库不会恢复 Redis 临时输出。
- 全部用户任务路由按用户 + 浏览器工作区隔离归属；隐藏执行 Key 返回 404 掩盖存在性。
- 内部取消与失败路径释放预占并清理临时键；ack 即删临时输出。
- 返回客户端的错误消息须由调用方剥离上游凭据、代理信息与 provider 原始响应；`SanitizeCreativeMessage` 仅做去空白、替换部分控制字符、处理无效 UTF-8 并截断至 500 Unicode 码点，不执行敏感信息脱敏。

相关 Project Doc：[批量图片作业](batch_image_jobs.md)、[内容审核与风险处置](content_moderation.md)、[路由与结算](routing_and_billing.md)、[HTTP 接口](../interfaces/http_api.md)、[配置](../interfaces/configuration.md)、[系统架构](../architecture/system_architecture.md)、[可观测性与数据生命周期](../operations/observability_and_data_lifecycle.md)和[领域目录](index.md)。
