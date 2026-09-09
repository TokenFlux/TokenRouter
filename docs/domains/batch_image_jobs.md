# 批量图片作业

TokenRouter 通过统一 API 提供异步 Gemini 批量图片生成，底层由 Redis worker、PostgreSQL 状态和提供商专用批处理后端共同实现。

本文覆盖公共资源形状、持久化作业生命周期、队列协调、计费预留、提供商执行、清理、安全边界、配置和验证。不定义生产价格、特定部署的 Google Cloud IAM 策略或普通同步图片生成。

支持的提供商：

- `gemini_api`
- `vertex`

API 用户不会看到 Gemini 文件名、Vertex 作业名、GCS 路径、签名 URL、API Key 或服务账号材料。当前实现通过 TokenRouter 代理下载。

## 章节导航

- [API 路由](#api-路由)：说明公共资源和请求限制。
- [生命周期](#生命周期)：说明持久化状态和公共状态投影。
- [Redis](#redis)：说明队列协调与恢复边界。
- [计费](#计费)：说明预留、捕获、释放和结算幂等。
- [清理](#清理)：说明输入输出留存与安全删除。
- [提供商说明](#提供商说明)：说明受支持的上游凭据形状。
- [配置](#配置)和[运维检查清单](#运维检查清单)：说明运行时启用条件。
- [安全检查清单](#安全检查清单)和[测试命令](#测试命令)：说明变更验证要求。

## API 路由

```text
POST   /v1/images/batches
GET    /v1/images/batches/{id}
GET    /v1/images/batches/{id}/items
GET    /v1/images/batches/{id}/items/{custom_id}/content
GET    /v1/images/batches/{id}/download
POST   /v1/images/batches/{id}/cancel
DELETE /v1/images/batches/{id}/outputs
```

提交请求示例：

```json
{
  "model": "gemini-2.5-flash-image",
  "provider": "gemini_api",
  "items": [
    {
      "custom_id": "cover_001",
      "prompt": "干净的产品主视觉图片",
      "output_count": 1,
      "reference_images": [
        {
          "id": "product-front",
          "type": "subject",
          "mime_type": "image/png",
          "data": "<不带 data URL 前缀的 Base64 图片字节>"
        },
        {
          "id": "style",
          "type": "style",
          "mime_type": "image/jpeg",
          "file_uri": "gs://internal-managed-bucket/batch-image/refs/style.jpg"
        }
      ]
    }
  ],
  "image_size": "1K",
  "response_mime_type": "image/png"
}
```

每个条目的 `reference_images` 可选。内联 `data` 是由后端解码的 Base64 字符串；`file_uri` 保留给内部 Google Cloud Storage 引用，且必须是 `gs://` URI。每张参考图片只能使用 `image/png`、`image/jpeg` 或 `image/webp`。当前模型限制：

- `gemini-2.5-flash-image` 和其他 Flash Image 别名：每个条目最多 3 张参考图片。
- `gemini-3-pro-image` 和其他 Pro Image 别名：每个条目最多 14 张参考图片。
- 每个批量作业：所有条目按 `output_count` 展开后，参考图片附件总数最多 1000。这是 TokenRouter 用于控制请求大小和成本的内部限制，不是生成图片上限，也不是 Pro Image 单条目能力。每个作业的生成结果上限为 200 张图片。
- 每个批量作业：解码后的内联参考图片数据总计最多 128 MB。大批量或重复参考图应优先使用 `gs://` `file_uri`，也可以拆分为多个作业。

每个条目的 `output_count` 可选，默认为 `1`。它表示“用同一提示词和参考图集合重复 N 次”，而不是依赖 Gemini 在一次上游请求中返回多张图片。后端会把每次重复展开为独立提供商 JSONL 行，并使用 `cover_001_01`、`cover_001_02` 等带后缀的自定义 ID。当前限制：

- 每个提示词条目最多生成 4 张图片。
- 展开后每个批量作业最多生成 200 张预期图片。这是单个作业的硬性生成结果上限；客户端和 Codex 技能必须在提交前拆分更大的工作量。
- 输出图片上限有意与默认 ZIP 条目上限保持一致，使新提交作业按条目数都能下载为一个 ZIP。ZIP 字节大小仍单独受 `max_download_bytes_per_request` 限制。

公共批量响应示例：

```json
{
  "id": "imgbatch_0123456789abcdef0123456789abcdef",
  "object": "image.batch",
  "status": "queued",
  "model": "gemini-2.5-flash-image",
  "provider": "gemini_api",
  "item_count": 1,
  "success_count": 0,
  "fail_count": 0,
  "estimated_cost": 0.25,
  "actual_cost": null,
  "created_at": 1783123200,
  "submitted_at": 1783123201,
  "settled_at": null
}
```

公共条目响应示例：

```json
{
  "object": "list",
  "data": [
    {
      "custom_id": "cover_001",
      "status": "succeeded",
      "mime_type": "image/png",
      "file_extension": "png",
      "image_count": 1,
      "error": null
    }
  ],
  "has_more": false
}
```

<a id="job_lifecycle"></a>
## 生命周期

内部生命周期：

```text
created -> uploading -> submitted -> running -> indexing -> settling -> completed
```

终态和清理状态：

```text
failed
cancelled
completed -> output_deleted
```

公共状态映射：

```text
created/uploading/submitted -> queued
running                    -> running
indexing                   -> processing_results
settling                   -> settling
completed                  -> completed
failed                     -> failed
cancelled                  -> cancelled
output_deleted             -> output_deleted
```

手工删除输出或 TTL 清理后，状态从 `completed` 变为 `output_deleted`。

任务提交时必须同时快照三种模型身份：`requested_model` 保存客户端提交值（复合 Key 场景包含自定义分组前缀），`internal_model` 保存复合 Key 选组和 API Key 模型重定向完成后、渠道与账号映射前的内部模型，`model` 保存最终提交给提供商的上游模型。异步结算写使用记录时以 `internal_model` 作为 `usage_logs.model`，并把 `model` 写入 `upstream_model`；迁移前任务没有内部模型快照时，才兼容回退到上游模型。

## Redis

Redis 用于唤醒、重试、worker 协调、单作业锁和下载限制。PostgreSQL 始终是权威数据源。

`batch_image.queue_enabled` 默认为 `false`。设为 `true` 后，应用启动会运行 `BatchImageWorker` 的 Redis 就绪队列、延迟队列搬运和过期活跃作业恢复循环。worker 从 Redis 就绪队列预留作业；没有作业时会阻塞等待。

Redis 结构：

- 就绪队列：`batch_image.queue_ready_key`
- 延迟队列：`batch_image.queue_delayed_key`
- 活跃集合：`batch_image.queue_active_key`
- 执行中键：`batch_image.inflight_key_prefix`
- 单作业锁键：`batch_image.lock_key_prefix`
- 队列幂等键：`batch_image.idempotency_key_prefix`
- 由下载限流器管理的下载限制键

worker 必须从 Redis 预留作业，不应以数据库扫描循环方式运行。只有 Redis 队列预留返回具体批量作业 ID 后才读取数据库。

## 计费

计费规则：

- 提交时按所选分组、渠道计费模型来源和图片尺寸解析单张价：分组价卡优先于渠道，未配置、或未命中尺寸且缺少默认单价时使用内置按张默认价，显式零价保留。token 价卡不能把每 token 单价作为单张价格，仍回退内置按张价；模型列表使用同一解析规则。
- 提交时冻结 API Key 的 `billing_mode` 和可选 `preferred_subscription_id`，后续冻结、捕获和释放都使用该任务快照，不能读取后来编辑后的 Key 配置。
- `auto` 模式提交时先预留适用的订阅额度，只冻结未被订阅覆盖部分所需的钱包余额；`subscription` 模式只预留指定订阅且必须完整覆盖，不能改扣余额或其它订阅；`balance` 模式跳过订阅预留。
- 定价快照遵循普通图片计费：每份订阅分配使用其套餐分组倍率；订阅覆盖后剩余的基础成本使用快照中的用户专属按量倍率。
- 结果索引完成后执行结算；已提交任务保留原价格和资金快照，即使升级清除了旧分组价格和独立倍率也不重算。
- 只对成功图片计费。
- 失败条目不计费。
- 结算按提交时预留的订阅和余额快照捕获；只有 `auto` 的预留允许同时包含两种来源。失败或取消时通过幂等路径释放所有未使用预留。
- 参考图片作为输入发送给 Gemini，可能产生少量上游输入 Token 和临时存储成本。`output_count > 1` 时，每个展开后的输出请求都会计算一次参考图，但公共计费模型不额外收取参考图费用。用户可见的估算、冻结和结算金额仍根据输出图片数量和已配置的批量图片单价计算。
- 结算请求 ID 为 `batch_image_settlement:{batch_id}`。
- 结算必须幂等，重复执行不能重复扣费。
- 结算计费失败会在有限次数内重试。达到重试上限后作业标记为失败，并通过幂等释放路径释放剩余冻结金额。

生产环境准确价格通过模型定价配置解析，本文不定义价格数值。

## 清理

默认值：

- 进入终态后的输入留存时间：24 小时。
- 进入终态后的输出留存时间：72 小时。
- 输出最长留存时间：7 天。
- 清理间隔：30 分钟。
- 单批清理数量：100。

手工删除输出：

```text
DELETE /v1/images/batches/{id}/outputs
```

输出清理后，下载返回 `410 Gone` 和 `BATCH_IMAGE_OUTPUT_DELETED`。

清理接口从不接受用户提供的提供商路径。提供商清理只能使用服务端生成的引用，并执行前缀安全删除。

使用受管 Vertex/GCS 批量存储桶时，应关闭 Cloud Storage 软删除或谨慎配置生命周期，避免隐藏的保留存储成本。

## 提供商说明

`gemini_api`：

- 使用 JSONL 文件模式的 Gemini Batch API。
- 支持配置了 API Key 的 Gemini `apikey` 上游账号。
- 结果文件引用只在内部使用。
- 永不返回 API Key。
- 管理员配置符合条件的 Gemini API Key 上游账号后，可以通过 TokenRouter 选择并提交该提供商。

`vertex`：

- 使用基于受管 GCS JSONL 的 Vertex `BatchPredictionJob`。
- 支持包含有效服务账号 JSON 的 Gemini `service_account` 上游账号。
- GCS 存储桶和前缀由服务端管理。
- Vertex 作业名和 GCS 路径只在内部使用。
- 当前实现中的批量图片输出只能按 `1K` 或默认值处理。
- 不得承诺 `2K` 或 `4K`。

其他 Gemini 账号或登录类型不会被当前批量图片提供商选择，除非它们通过相同提供商流程公开等价的 API Key 或服务账号凭据。

## 启用 Google 官方能力

运维人员为任何分组开启 TokenRouter 批量图片前，必须先在 Google 官方控制台启用 Gemini 或 Vertex 能力。TokenRouter 功能开关和分组开关不会自动创建 Google 侧访问权限。

推荐生产路径：

- 使用已启用结算的 Google Cloud 项目。
- 为项目启用相应 Gemini API 或 Vertex AI API。
- TokenRouter 运行时使用服务账号或应用默认凭据。
- 为批量图片输入输出创建固定 Cloud Storage 存储桶，并向运行时和 Vertex 服务代理授予最低必要存储桶权限。
- 在 TokenRouter 中配置项目 ID、区域、受管存储桶、提供商账号、模型白名单和价格。
- 全局启用 `BATCH_IMAGE_ENABLED`，在目标 Gemini 分组上启用图片生成，再为该分组启用 `allow_batch_image_generation`。非 Gemini 分组不支持批量图片；只有 Gemini 分组先启用图片生成后，管理界面才显示批量图片开关。

API Key 路径：

- Google API Key 适合 Gemini API 开发和受支持的 Gemini 方法。
- TokenRouter 的 `x-goog-api-key` 兼容请求头仍要求 TokenRouter Key，而不是普通 Google Key。
- 不应把普通 Google API Key 记录为 Vertex 服务账号批量作业的默认生产凭据。
- 管理员配置 Gemini API Key 上游账号后，应在 Google 账号具备必要结算或预付状态时执行一次低成本批量图片验证。没有预付时，只能记录提供商可被选择和调用，以及提交失败会释放预留，不应推断更多能力。

官方参考：

- Gemini API Key 指南：https://ai.google.dev/gemini-api/docs/api-key
- Gemini API Batch API：https://ai.google.dev/gemini-api/docs/batch-api
- Gemini API 图片生成与批量图片说明：https://ai.google.dev/gemini-api/docs/image-generation
- Vertex/Gemini 批量推理：https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/capabilities/batch-inference
- Vertex 批量预测 API：https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/models/batch-prediction-api

## 配置

以下配置键定义在 `backend/internal/config/config.go`：

```yaml
batch_image:
  enabled: false
  max_items_per_job_default: 200
  max_items_per_job_trial: 50
  max_output_images_per_job: 200
  max_output_images_per_item: 4
  max_prompt_chars_per_item: 8000
  max_reference_images_per_job: 1000
  max_reference_inline_bytes_per_job: 134217728
  default_response_mime_type: "image/png"
  default_image_size: "1K"

  max_download_items_zip: 200
  max_download_bytes_per_request: 536870912
  max_download_duration_seconds: 600
  max_download_concurrency_per_user: 1

  input_retention_after_terminal_hours: 24
  output_retention_after_terminal_hours: 72
  output_retention_max_days: 7
  cleanup_interval_minutes: 30
  cleanup_batch_size: 100

  queue_enabled: false
  queue_ready_key: "batch_image:queue:ready"
  queue_delayed_key: "batch_image:queue:delayed"
  queue_active_key: "batch_image:queue:active"
  inflight_key_prefix: "batch_image:queue:inflight:"
  lock_key_prefix: "batch_image:queue:lock:"
  idempotency_key_prefix: "batch_image:queue:idem:"
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

  vertex_enabled: false
  vertex_project_id: ""
  vertex_location: "global"
  vertex_managed_gcs_bucket: ""
  vertex_managed_gcs_prefix: "batch-image/{env}/{batch_id}"
  vertex_input_retention_hours: 24
  vertex_output_retention_hours: 72
  vertex_batch_prediction_base_url: ""
  vertex_gcs_base_url: ""
```

所有功能开关默认关闭。

## 运维检查清单

- 启用 `batch_image.enabled`。
- 配置 Redis。
- 需要 worker 消费队列作业时启用 `batch_image.queue_enabled`。
- 配置提供商账号。
- 使用 Vertex 时配置受管 GCS 存储桶。
- 确认存储桶权限正确。
- 关闭或妥善管理 GCS 软删除。
- 配置清理 worker。
- 配置单作业最大条目数。
- 配置下载并发。
- 确认计费价格。
- 启用前执行冒烟测试。

## 安全检查清单

- 公共响应不包含提供商引用。
- 不暴露 GCS URI。
- 不暴露签名 URL。
- 不暴露服务账号。
- 不暴露 API Key。
- PostgreSQL 不保存图片字节或 Base64。
- 日志不记录 Base64。
- 状态、条目、下载、取消和删除路由都受所有者范围约束。
- 输出删除受所有者范围约束。
- 清理路径只能由服务端生成。

## 测试命令

核心冒烟和编译命令：

```bash
go test -tags=unit ./internal/service -run 'BatchImage' -count=1
go test -tags=unit ./internal/config ./internal/service ./internal/repository -count=1
go test ./internal/config ./internal/service ./internal/repository ./internal/handler ./internal/server/routes -run '^$'
go test ./... -run '^$'
```

这些命令不应依赖 Docker、Testcontainers、Redis、GCP、Gemini、Vertex 或 GCS。

相关 Project Doc：[路由与结算](routing_and_billing.md)、[复合 API Key](composite_api_keys.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)和[领域目录](index.md)。
