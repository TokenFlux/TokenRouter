# 版本升级说明

本文按迁移编号从新到旧，记录各次需要特别处理的升级：停机要求、缓存版本变化、升级后的验证和回退方式。迁移执行机制和通用的升级回退规则见[部署与数据库迁移](deployment_and_migrations.md)。

各节里提到的认证缓存版本（v34 到 v46）、调度缓存命名空间，都是对应迁移发布时的值。当前代码的认证缓存版本是 46，调度缓存命名空间是 `sched:v4:`，以[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)为准。后面的迁移可能取代前面的字段，所以不能只看某一节判断当前的接口；"当前状态"一列标出了已被取代的专题。

<a id="finite_model_catalogue"></a>
## 模型目录候选收窄（无需数据库迁移）

模型列表使用已保存的白名单、映射、分组与创作台配置中的具体名称，以及平台执行规则能枚举的型号。models.dev 提供价格、属性和搜索建议，对外列表由这些有限配置生成。

升级后，只有空白名单或通配规则、缺少具体型号的提供商可能返回空目录，相应分组也可能从模型广场隐藏。管理员可填写具体型号，或使用现有的同步上游操作并保存。空白名单仍允许尝试直接请求未知 ID，执行时继续检查分组、协议及平台资格。未知型号的价格规则保持原样。

这次升级需要更新前后端。已有提供商白名单、映射和同步表单直接使用，数据库结构保持现状。`gateway.models_list_cache_ttl_seconds` 保留解析兼容，目录候选短缓存已停用。升级后核对模型列表、分组选择、创作台生成和设置保存；回退二进制及前端会恢复旧的目录枚举行为。

<a id="request_id_unification"></a>
## 请求 ID 与使用记录去重（迁移 293、294）

迁移 293 在同一事务中添加请求摘要表、使用记录的 `billing_key` 列和计费去重唯一索引，并创建按索引查找请求 ID 的函数。请求详情查询共用一次解析得到的候选 ID。

迁移 294 更新请求 ID 查询函数，使父请求能够通过关联子记录的计费键找到升级前的费用记录。迁移使用 `CREATE OR REPLACE FUNCTION` 替换查询函数，历史记录保持现有 ID。

升级后检查 `idx_usage_logs_billing_key_api_key` 有效，再验证普通请求的响应头与用量 ID 一致、历史 ID 可查，以及任务重放共用一笔费用和一条用量记录。请求 ID 响应头返回本地 ID，供应商 ID 从请求详情中的外部标识查询。

完整规则见[请求 ID 与请求查询](request_lookup.md)。

## Responses WS 设置与在线调参（迁移 292）

本次升级需要一起更新前后端，停止旧实例后执行迁移。提供商的连接方式统一保存为 `responses_ws_connection_mode`，旧的透传选择转为 `per_session`，其他普通连接模式转为 `pooled`。之前受旧全局开关影响而未生效的透传选择，会在升级后生效。

旧的 WS 关闭字段退出运行判断。需要关闭客户端 WS 的分组，应在升级前取消分组的 WebSocket 协议许可。提供商已保存的协议集合保持明确选择，空数组继续禁止提供商承接新调用。旧 HTTP 桥接或提供商强制 HTTP 会移除上游 WS 能力，之后根据已有的 HTTP 能力和分组转换规则选择提供商。

升级前可查询旧 HTTP 桥接提供商所在分组，核对 HTTP 能力和转换许可；查询只返回标识和协议配置：

```sql
SELECT p.id AS provider_id, g.id AS group_id, g.name AS group_name,
       p.credentials->'upstream_protocols' AS upstream_protocols,
       g.allowed_protocols, g.protocol_fallbacks
FROM providers p
JOIN provider_groups pg ON pg.provider_id = p.id
JOIN groups g ON g.id = pg.group_id
WHERE p.platform = 'openai'
  AND (p.extra->>'openai_oauth_responses_websockets_v2_mode' = 'http_bridge'
    OR p.extra->>'openai_apikey_responses_websockets_v2_mode' = 'http_bridge'
    OR p.extra->'openai_ws_force_http' = 'true'::jsonb);
```

提供商缺少 `openai_responses`，或分组的 `protocol_fallbacks.openai_responses_websocket` 为 `[]` 时，需要管理员调整配置后才能通过 HTTP 承接 WS。迁移保持分组限制。调度缓存命名空间更新为 `sched:v5`，新实例从已迁移数据重建缓存。

在线设置首次没有覆盖值时继续使用环境变量、YAML 和代码默认值。上线后验证池化和单会话连接、HTTP 转换、分组禁用、参数保存与恢复默认，以及多实例刷新。回退需要恢复升级前的数据库备份，切换旧二进制不会撤销字段迁移。

## 总览

| 迁移 | 专题 | 升级方式 | 当前状态 |
| --- | --- | --- | --- |
| 291 | [OpenAI OAuth 导入模板下线](#openai_oauth_import_defaults_removal) | 前后端同时升级 | 有效 |
| 290 | [用户可见内容多语言](#user_localization_migration) | 停机，前后端同时升级 | 有效 |
| 289 | [用户活动汇总](#user_activity_migration) | 回填期间暂停用量写入 | 有效 |
| 286 | [Antigravity 静态提供商停用](#antigravity_static_retirement) | 停机 | 有效 |
| 284 | [模型属性档案与统一目录](#模型属性档案与统一目录) | 可滚动 | 有效 |
| 无 | [文件价格覆盖退役](#文件价格覆盖退役) | 可滚动，需手工迁移价格 | 有效 |
| 283 | [产品名称兼容](deployment_and_migrations.md#product_name_compatibility) | 可滚动 | 有效 |
| 282 | [提供商名称统一](#provider_name_migration) | 停机 | 有效 |
| 281 | [分组价格设置归入价格配置](#分组价格设置归入价格配置) | 停机 | 有效 |
| 280 | [Messages 专用模型覆盖下线](#messages-专用模型覆盖下线) | 前后端同时升级 | 有效 |
| 279 | [Messages 系列默认映射下线](#messages-系列默认映射下线) | 前后端同时升级 | 被 280 取代 |
| 276–278 | [分组跨平台与统一价格配置](#分组跨平台与统一价格配置) | 停机 | 有效 |
| 275 | [移除重复的价格和图片设置](#移除重复的价格和图片设置) | 停机 | 有效 |
| 274 | [价格配置与分组策略拆分](#价格配置与分组策略拆分) | 停机 | 有效 |
| 273 | [统一协议能力](#统一协议能力) | 停机 | 有效 |
| 272 | [独立媒体定价下线](#独立媒体定价下线) | 停机 | 有效 |
| 270–271 | [OpenAI 能力探测下线](#openai-能力探测下线) | 不支持混跑 | 有效 |
| 269 | [分组 Fast 与 Ultra Fast 策略](#分组-fast-与-ultra-fast-策略) | 不支持混跑 | 有效 |
| 265 | [数据共享功能下线](#数据共享功能下线) | 停机 | 有效 |
| 264 | [分组 OpenAI Fast 按 Standard 计费](#分组-openai-fast-按-standard-计费) | 先升级后端再开放 | 字段已由 281 移到价格配置 |
| 263 | [分组推理强度超限动作](#分组推理强度超限动作) | 先升级后端再开放 | 平台限制已由 277 取消 |
| 262 | [分组 OpenAI Fast 强制策略](#分组-openai-fast-强制策略) | 先升级后端再开放 | 被 269 取代 |
| 261 | [缓存写入 1 小时分档](#缓存写入-1-小时分档) | 先升级后端再开放 | 有效（渠道已由 274 改名为价格配置） |
| 258 | [API Key 结算字段的缓存失效](#api_key_billing_cache_invalidation) | 可滚动 | 有效 |
| 257 | [创作台持久状态与 outbox](#创作台持久状态与-outbox) | 先迁移再部署 | 有效 |
| 248 | [国产供应商用户平台额度](#国产供应商用户平台额度) | 可滚动 | 被 277 删除 |
| 246 | [分组逐模型定价](#分组逐模型定价) | 先升级后端再开放 | 被 281 删除 |
| 242–245 | [Grok 媒体、搜索和 Voice 定价](#grok-媒体搜索和-voice-定价) | 可滚动 | 分组媒体价格被 272、281 删除 |
| 241 | [OpenAI 提供商级长上下文开关下线](#openai-提供商级长上下文开关下线) | 停机 | 有效 |
| 238–240 | [通用高级调度器](#通用高级调度器) | 停机 | 有效 |
| 237 | [API Key 结算模式与批量图片快照](#api-key-结算模式与批量图片快照) | 先升级后端再开放 | 有效 |
| 236 | [上游声明倍率探测下线](#上游声明倍率探测下线) | 停机 | 有效 |
| 235 | [分组客户端协议](#分组客户端协议) | 一次性切换 | 字段已由 273 改名 |
| 234 | [自研异步图片任务下线](#自研异步图片任务下线) | 停机 | 有效 |

"停机"表示需要先停止全部旧实例、备份并验证数据库，再由一个新实例执行迁移，验证后才扩容其他新实例，新旧二进制不能同时运行。这类迁移的回退方式都是：停止全部新实例，恢复升级前的数据库备份（需要时还有 Redis 和配置），再启动旧版本；只回退二进制、手工补数据或删除迁移记录都不能代替数据库恢复。下面各节不再重复这段说明，只写各自不同的地方。

<a id="openai_oauth_import_defaults_removal"></a>
## OpenAI OAuth 导入模板下线

迁移 `291_remove_openai_oauth_import_defaults.sql` 删除 `settings` 中的 `openai_oauth_import_defaults`。网关设置中的模板编辑入口及其 GET、PUT 接口已下线。创建和导入 OpenAI OAuth 提供商时，配置来自创建表单或导入文件，缺省字段按提供商创建规则处理。已创建提供商的配置保持当前值。

前后端需要同时升级。升级前备份该设置，回退到需要模板的版本时，从备份恢复。历史迁移 271 对模板的清理仍按迁移顺序执行，随后由迁移 291 删除模板。

<a id="user_localization_migration"></a>
## 用户可见内容多语言

迁移 `290_user_localization.sql` 新增账户语言和公告、套餐、分组的文案 JSONB，把站点中英文文案和独立运营设置转为原文与译文。`zh` 邮件模板键和历史偏好转换为 `zh-Hans`。无法判断语言的历史内容使用 `source_locale=null`，管理员首次修改原文时选择语言。站点标题和副标题的历史空值在导入时跳过，页面使用对应语言的内置文案。

升级前停止全部服务并备份数据库、settings 和 `pages/` 文件。新前后端同时发布，旧站点 `_zh`、`_en` 字段写入会被拒绝。迁移保持公告 ID 与已读、协议原文与确认、业务 ID 和订单订阅关联。网关错误使用英文单文本配置。恢复旧版本需要恢复升级前数据库备份。

升级后分别用中英文检查首页、登录协议、公告、分组、套餐、支付和用户邮件。历史订单使用原快照；译文保存冲突返回 409。文件页面的译文和图片目录也应进入现有文件备份范围。内容模型和路径规则见[用户侧国际化](../interfaces/user_localization.md)。

<a id="user_activity_migration"></a>
## 用户活动汇总

迁移 289 创建 `usage_user_activity`，安装用量变更触发器并回填最近使用时间。迁移事务持有用量表写锁，回填按用户使用时间索引读取。发布前在备份环境测量迁移耗时，安排可容纳写入等待的维护窗口。失败会回滚表、触发器和回填结果。

更新后检查用户搜索、最近使用时间双向排序、分组最新探测状态和公开 `/usage` 摘要。校验活动汇总与原始记录按用户计算的最大时间相等。备份的用量选项会同时包含或排除活动汇总。

恢复入口在恢复事务开头移除活动汇总表，解除它对用户主键的外键依赖。当前备份会重建汇总表和数据；恢复迁移 289 之前的备份后，需要重启服务，由启动迁移创建并回填汇总。恢复失败时，移除汇总表的操作也会回滚。

回退应用时可保留新增结构。再次升级前，在维护事务内执行 `SELECT usage_rebuild_user_activity()` 并校验结果；回退期间直接删除分区或操作子分区都需要此项检查。分区清理应使用 `usage_drop_partition_with_activity` 完成删除与重建。

<a id="antigravity_static_retirement"></a>
## Antigravity 静态提供商停用

迁移 286 将未删除的 Antigravity `apikey` 和 `upstream` 提供商设为 `status=inactive`、`schedulable=false`，更新提供商时间并写入调度 outbox。提供商 ID、类型、凭据、协议配置和分组关联保持原值。迁移访问 `providers` 和 `scheduler_outbox`，usage 表及其索引保持原状，历史用量继续通过原 ID 查询。

停止旧实例并备份数据库后，由一个新实例执行迁移，再启动其他新实例。调度事件和启动重建刷新提供商快照。升级后检查 OAuth 的流式及非流式请求，并确认旧静态提供商已停用。重复执行迁移时，已经停用的记录不会再产生事件。

需要继续连接中转网关时，按它提供的协议创建 Anthropic 或 Gemini API Key 提供商，Base URL 填写完整前缀（例如 `https://gateway.example/antigravity`），重新配置分组、模型和额度。Google 签发的 Gemini API Key 使用 Gemini 提供商。旧静态提供商可供历史查询或按常规流程删除。

## 模型属性档案与统一目录

迁移 284 新增属性档案表和分组关联表，配置名称和分组关联都有数据库唯一约束。属性规则用 JSONB 保存，关联使用外键和级联删除；迁移不改写现有的分组、价格配置和账单。新增的表不影响旧二进制的读取。

生产环境的目录切换到 models.dev：已知的 Wei-Shaw、BerriAI 公共旧地址，在加载配置时映射到新地址，运行时只使用统一目录的条件请求。新的缓存文件 `models_dev_catalog.json` 和旧缓存分开保存；程序内嵌了离线目录，升级不要求第一次下载就成功。默认的媒体补充文件随资源目录发布，本地补充只填目录的缺口，用户售价由价格配置管理。发布包不再带旧的完整价格文件；缺失的旧默认打包路径，映射到新的补充文件，已有的自定义文件保持原路径。旧的轮询间隔键和环境变量兼容到 `pricing.check_interval_minutes`，旧的 hash URL 和按小时的更新间隔被忽略。回退二进制后，仍可以使用原有的价格配置和旧缓存，新增的属性档案保留在数据库里。

## 文件价格覆盖退役

`pricing.override_file` 和 `PRICING_OVERRIDE_FILE` 不再参与目录构建；旧文件不会被读取、改写或删除，配置了非空的旧键时，启动会提示已弃用。升级前，把要保留的用户售价转进价格配置并关联分组，一份配置可以关联多个分组。旧文件如果还负责修正提供商成本，需要另外设置提供商成本规则，用户售价不会被自动当作成本。

这次没有新增数据库结构或全局默认价卡，也不会自动迁移文件。没有价格配置的分组使用目录价格；已保存任务的资金快照和历史账单不重算。迁移完成后，删除旧配置键；回退旧二进制之前，核对旧文件里的价格和当前的价格配置，避免恢复不再需要的覆盖。

<a id="provider_name_migration"></a>
## 提供商名称统一

迁移 `282_rename_accounts_to_providers.sql` 把上游接入的实体统一命名为 Provider，管理端路径是 `/admin/providers`，API 是 `/api/v1/admin/providers`。相关的请求、响应、查询和配置键都使用 `provider`。旧的管理入口和旧的请求结构字段没有兼容；启动配置兼容读取旧名称，规则见[配置来源](../interfaces/configuration.md#configuration_sources)。第三方协议里的 `account_id`、`account_uuid`、Service Account 和控制台登录账户，保持各自的原义。

数据库的表、列、约束、索引和序列原地改名，历史用量和费用数值不变，不回填 `usage_logs`，也不重建索引或预聚合。现有配置只迁移明确属于本项目的键和路径，以及额度告警邮件模板里的占位符；模型别名、自定义请求头、第三方 JSON 和其他设置键保持原样，新旧两个自有键冲突时，整个事务回滚。历史日志原文不改；旧的调度 outbox 和错误事件里的实体字段，在读取时转换；错误阶段的筛选同时匹配历史的 `account_auth`，对外显示 `provider_auth`。创作台和批量图片旧的 `provider` 平台字段，改名为 `platform`。

SQL 事务的锁等待上限是 10 秒，执行上限是 120 秒，失败时全部回滚。锁等待失败时，先检查占用表的事务，再重试启动；应用不会主动终止其他会话。升级前停止全部旧实例并等待在途请求结束，备份 PostgreSQL、Redis 和配置。YAML 和环境变量里旧的提供商配置名、旧的 OpenAI 调度配置名和连接池隔离模式，加载时会自动转换，不需要先手动改名。备份的耗时不计入改名迁移的时间。

新实例在开放流量、装配后台任务之前，分批迁移 Redis 里的并发、会话、限流、临时停调和窗口费用键，保留数据类型和剩余的 TTL。目标键冲突时，启动停止，双方的数据都保留；解决冲突后，根据 `migration:provider-names:v1` 里记录的批次进度继续。这个标记不是让新旧实例同时运行的机制，升级期间旧实例要保持停机。调度快照使用 `sched:v4`，API Key 认证快照版本是 46，仪表盘统计缓存使用 v2；旧快照不参与新版本的查询。

JSON 导出格式的标识仍是 `tokenrouter-data`，版本是 2，集合名是 `providers`。导入拒绝缺少版本、旧版本、旧格式标识和 `accounts` 集合；CRS、Codex 等外部格式，由专门的入口按对方的协议读取。新版的 JSON 导出仍然不包含分组关联，以及 Spark 影子的独立配置。

先启动一个新实例完成迁移和抽样验证，再恢复其他实例。回退时，停止全部新实例，恢复升级前的数据库、Redis 和配置。在隔离的 PostgreSQL 18 测试里，100 万条用量记录连同索引大约 470 MB，元数据迁移耗时约 0.08 秒，表和 26 个索引的物理文件标识不变；生产环境的停机窗口，仍要按实际的配置量、Redis 键数量和锁等待演练，更早还没执行的历史迁移的耗时另算。

## 分组价格设置归入价格配置

迁移 `281_pricing_config_billing_settings.sql` 给价格配置增加计费设置，删除分组上对应的价格列和模型覆盖价。已有价格配置的价卡、提供商成本规则和分组关联保留，新增的设置使用默认值；旧的分组价格不会复制过去，需要保留的价格策略，由管理员在价格配置里重新设置。

升级方式：停机。旧二进制依赖已经删除的列，不能和新版本同时运行。认证缓存版本升到 v45，旧的价格快照不再被读取。升级后检查关联配置、公开报价和新请求的扣费；已经提交的图片任务按原来的价格和资金快照结算，历史用量不重算。

## Messages 专用模型覆盖下线

迁移 `280_remove_messages_dispatch_model_config.sql` 删除分组的专用模型覆盖列，旧规则直接停止生效，不会被自动复制到通用映射。现有的 `routing_policy.model_mapping`、分组身份和其他策略保持不变，迁移可以重复执行。

创建、编辑、复制分组和认证快照都不再带这项配置，旧的管理请求返回 400。认证快照升到 v44，重新回源；前后端需要同时升级，回退时使用升级前的数据库备份和旧版本。

## Messages 系列默认映射下线

迁移 `279_remove_messages_family_mapping.sql` 删除分组 `messages_dispatch_model_config` 里 Opus、Sonnet、Haiku 的系列目标字段，保留精确的模型覆盖、分组 ID 和其他策略。迁移可以重复执行；旧的系列规则不会转成通配规则，升级后不再按系列自动改写模型。之后迁移 280 删除了整列。

管理端的创建和编辑表单去掉了这项设置，提交旧字段返回 400。认证快照升到 v43，旧快照重新回源。前后端需要同时升级；回退时使用升级前的数据库备份和旧版本。

## 分组跨平台与统一价格配置

迁移 `276_platform_independent_pricing.sql` 和 `277_platform_independent_groups.sql` 删除价格的平台、分组的平台、默认分组和用户平台额度，旧实例不能和新版本同时运行。

1. 升级前，核对价卡冲突、没有绑定分组的 Key 和提供商的模型范围，处理下面说的不可比较的价格规则。
2. 备份并验证完整的 PostgreSQL 数据，停止所有旧实例和 worker。历史上没有绑定分组的 Key，需要管理员手动选组，迁移不会替它选默认分组。
3. 启动新版本执行前向迁移。每个 SQL 文件在单独的事务里提交；276 成功而 277 失败时，保持停机，修复后重试，不能重新启动旧版本。
4. 确认迁移完成后，重建调度快照和认证缓存，再恢复流量。抽样检查混合分组的路由、报价和实扣、平台统计、历史任务，以及没有绑定分组的 Key 的错误提示。
5. 回退时，停止新版本，把升级前的数据库备份和旧二进制一起恢复。迁移归档只用于核对，不能当作 Down 迁移直接执行。

同一配置、同一模型的可比较手动单价，逐项取最大值，输入和输出可能来自不同的旧平台。全部为空的价格桶保持继承，手动填写的零价保留。以下情况会阻断迁移：空值和手动值并存，计费模式、区间、倍率或分时规则不同，模型通配规则重叠但不相同。提供商成本规则按各自的匹配条件和排序分别合并，不跨规则取最大。当时的缓存使用认证快照 v42 和 `sched:v3:`，新版本不再读取旧的命名空间。

价格的归档保存在 `platform_independent_pricing_archive`。分组归档 `platform_independent_group_archive` 保留原来的平台、默认分组标记和完整的策略，其中没有生效的平台草稿不会转成生效的配置。平台额度和它的默认设置，归档到 `removed_platform_quota_archive` 后退出运行时。旧的 `allow_ungrouped_key_scheduling` 设置单独保存在 `platform_independent_setting_archive`，然后从运行设置里删除。

旧的使用记录先按升级前的统计方式固定 `platform`，旧的错误记录只补齐空的平台；新记录使用实际提供商的平台。迁移不会清空预聚合、重算账单，也不会改变已提交任务的 provider 和资金快照。创作台的迁移 `278_creative_provider_snapshot.sql` 增加 `provider`，从已经绑定的提供商回填历史任务；新任务在调用上游之前保存供应商和提供商。协议 fallback 旧的单个目标改成数组，没有配置的项明确设为空数组，以保持只走原生协议；存量的 `allowed_protocols` 原样保留。

## 移除重复的价格和图片设置

迁移 `275_remove_redundant_pricing_controls.sql` 删除 `pricing_configs.apply_pricing_to_provider_stats`，并清理 `groups.routing_policy.features_config.codex_image_generation_bridge`。提供商成本保留独立的规则和网关默认模型价的回退，不再提供复用用户自定义价的开关。分组的图片设置只保留协议控制，优先级是分组的协议设置、提供商覆盖、全局默认值；旧的默认值直接删除，不会提升成覆盖提供商设置的分组策略。

升级方式：停机。迁移可以重复执行，不修改价格条目、提供商成本规则、历史账单和任务的定价快照。

## 价格配置与分组策略拆分

迁移 `274_split_pricing_configs_and_group_policy.sql` 把 channels 和它下属的表改名为 pricing_configs 和 pricing_config_*，关联列和 usage_logs 的 channel_id 改名为 pricing_config_id。ID、关联、价卡排序、金额、NULL 和零值都保持不变。计费来源 channel_mapped 改名为 group_mapped。

非价格字段先写进 pricing_policy_migration_archive，再按渠道原来的关联关系复制到 groups.routing_policy。启用的渠道，策略继续启用；停用的渠道，策略作为关闭的草稿保留；没有关联分组的原策略，留在归档里。模型白名单从原来主模型的价格条目里一次性提取，之后和价格配置无关。重复执行不会覆盖分组之后的修改。原来的模型映射、限制、功能和展示字段，随后从价格表里删除。

升级方式：停机，并等待在途请求结束。认证快照升到 v41，旧快照重新回源。完成分组策略、价格、历史用量和管理员 API 的抽样后，再恢复其他实例。旧的渠道 API 已经删除，前端和管理脚本需要同步升级。

批量图片和创作台持久化的定价快照只有金额和倍率，没有渠道标识，所以不需要改写；已提交的任务按原快照结算。

## 统一协议能力

迁移 `273_unify_protocol_capabilities.sql` 把提供商旧的文本路由和工作负载，转成 `credentials.upstream_protocols`，把分组的文本集合改名为 `allowed_protocols`，回填已有的媒体、Live、Voice、搜索入口和手动配置的转换映射，并保留 CN 按协议分开的地址。原来图片开关关闭的 OpenAI 和 Grok 分组，迁移成 Responses 图片的 `block`，开启的迁移成 `inherit`。旧的内部布尔列保留为派生的镜像；新的管理响应只提供统一的配置。

升级方式：停机。迁移后重建认证缓存 v40 和 `sched:v2:` 调度缓存，抽样确认 CN 的自定义地址、OpenAI 和 PAT 的原生能力、分组转换、媒体入口和已有任务的管理，再扩容。迁移可以重放，不会覆盖已经保存的新的空集合或转换配置。旧的 CN 固定协议和 OpenAI 强制协议，只迁移实际存在的字段；混合提供商在同一分组里共用同一个转换目标，要抽样确认目标提供商已经启用了这个协议。

## 独立媒体定价下线

迁移 `272_remove_group_media_pricing.sql` 删除分组的图片三档单价、视频三档单价、视频模型族价格，以及图片和视频的独立倍率开关和数值，共 11 列。旧值直接清除，不转换，也不改写 `model_pricing`；图片和视频统一使用分组或渠道的模型价卡（281 之后只有价格配置的价卡），没有价卡时按内置的媒体默认价结算。旧的媒体独立倍率不再覆盖普通的分组、用户或订阅倍率，所以之前配置过的分组，升级后费用可能变化。

升级方式：停机。迁移可以重复执行，不修改历史账单、模型价卡、批量任务和创作台的资金快照。已提交的图片任务按原快照结算；还没有形成价格快照、尚未完成的视频，按完成结算时的规则收费。升级后验证价卡、普通倍率、异步预占的释放和幂等行为；认证缓存版本 39 会重建旧的价格快照。

## OpenAI 能力探测下线

迁移 `270_openai_manual_protocol_capabilities.sql` 把 OpenAI 提供商原生 V2 和旧版 Compact 的自动模式，固定为升级前实际生效的开关，保留手动的 force_on 和 force_off，并清除 Responses 和压缩的历史探测字段。没有明确"不支持"结论的自动提供商保持开启。文本路由使用已有的管理员三态，双协议模式不会因为探测结果降成 Chat；国产供应商改用自己手动配置的协议生成路由。

迁移 `271_clean_openai_import_capability_state.sql` 补充清理 `openai_oauth_import_defaults` 模板里的历史探测字段，把手动写的 `auto` 转成开启；模板里缺少的开关、手动关闭的值、提供商的默认值和其他参数保持原样。这个迁移可以重复执行，不创建缺失的模板，也不覆盖非法 JSON、非对象的模板或非对象的 Extra。JSONB 无法表示的 Unicode（例如 `\u0000`）和超出范围的数值，同样保留原始文本，辅助配置不会阻断启动；容错只覆盖 JSONB 转换这一块，数据库的读写异常照常报错。读写时继续丢弃旧客户端的输入，废弃的状态不会被重新写进来；已有的迁移文件保留，以满足校验和的约束。

全部后端升级完成后，再开放新的管理控件；旧实例可能仍然执行探测和旧的路由逻辑，不支持在新旧混跑时依赖新行为。迁移更新提供商 extra 后，按调度数据的失效机制传播。回退二进制不会恢复被清理的探测状态；需要精确回退时，使用升级前的数据库备份。验证时覆盖两种协议的单选和双选、两类压缩开关，以及手动测试不改变配置。

## 分组 Fast 与 Ultra Fast 策略

迁移 `269_group_openai_fast_policy.sql` 新增默认跟随请求的四值策略列，第一次新增时，把旧的 OpenAI `force_openai_fast=true` 回填为强制 Fast（原 SQL 里也有对未启用的平台值 `composite` 的历史兼容判断），重复执行不会覆盖新值。旧列保留为兼容镜像。先完成全部后端升级和认证快照 v38 的重建，再开放 Ultra Fast 和关闭策略；旧实例无法执行新规则，不应同时运行。

## 数据共享功能下线

迁移 `265_remove_data_sharing.sql` 是不支持新旧实例同时运行的破坏性迁移。它幂等地删除 `data_share_export_artifacts`、`data_share_sessions`，删除分组、API Key 和复合 Key 映射里与数据共享有关的列和索引，并删除九个数据共享的运行设置。对于有效的 `backup_content_config` JSON 对象，迁移只删除 `include_data_share_sessions`；历史上无效的 JSON，或者 JSONB 无法表示的值，保留并发出数据库 notice，不阻断迁移。历史迁移 141 到 168 和 226 保持不变，空库可以按完整序列初始化，旧版本也可以前向升级。

升级按以下顺序执行：

1. 停止接流量，停止全部旧实例，防止旧代码在列删除后继续读写。
2. 创建 PostgreSQL 备份，并实际验证可以恢复。另外记录所有本地导出目录，以及远端存储的 endpoint、bucket、prefix 和对象 key 清单；同时找出可能包含共享会话的旧备份。清单里不记录访问密钥的明文。
3. 只启动一个新实例执行迁移。确认两张表、相关的列、索引和九个设置都已删除，`backup_content_config` 的其他选项保持不变，并验证原来的用户端和管理端 `/api/v1/data-sharing/*` 路径返回普通的 404。
4. 确认认证快照版本升到了 v37，旧的 Redis 快照因为版本不匹配自然重建；完成核心网关、计费、备份和管理页面的抽样后，再扩容其他新实例。

新版本忽略旧配置里的 `gateway.data_sharing_capture`、`gateway.data_sharing_export` 和对应的环境变量；部署者需要从 YAML、Compose 覆盖和密钥管理里手动删除这些键。迁移只清理 PostgreSQL 的元数据，本地目录、S3 或 R2 对象和旧备份都不会被删除；`.gitignore` 继续保留旧导出目录的规则，以免还没人工处理的敏感文件被提交。外部对象和包含共享会话的旧备份，按部署方的保留和合规策略盘点、保留或清理。

## 分组 OpenAI Fast 按 Standard 计费

迁移 `264_group_free_openai_fast.sql` 给 `groups` 增加默认关闭的 `free_openai_fast` 布尔列。迁移 281 之后，这个设置已经移到价格配置。当时，管理 API、分组复制和认证快照只为 OpenAI 分组保留这个策略；分组切换到其他平台时，由服务层清零。上游请求仍然使用 Fast 或 priority，只有用户侧的结算，在同一个模型、渠道和计费时刻，改用 Standard 价格。

认证缓存版本从 v35 升到 v36，快照增加了免费 Fast 字段。Usage Log 的 Fast `total_cost` 继续作为提供商统计和提供商额度的成本基数，Standard 的 `actual_cost` 和统一结算的基础金额用于余额、订阅和 API Key 配额。迁移是幂等的新增列，但旧后端不会读取这个策略；发布时先执行迁移、升级全部后端实例，确认旧的 v35 快照已失效、管理 API 的字段往返正确，再开放开关。回退旧二进制不会删除这一列；新旧混跑期间，不能依赖免费 Fast 的计价。

## 分组推理强度超限动作

迁移 `263_group_reasoning_effort_over_limit.sql` 给 `groups` 增加非空的 `max_reasoning_effort_over_limit`，默认 `downgrade`，并记录 `deny` 的拒绝含义。上游同名迁移的编号没有直接复用，本 fork 按当时的最大迁移号递增为 263。当时管理服务只允许 `downgrade` 或 `deny`，并且 `deny` 只对 OpenAI 分组开放；迁移 277 之后，分组没有平台，这条平台限制也就不存在了。

认证缓存版本从 v34 升到 v35，快照增加了这个动作。HTTP Responses 和 Chat、Messages 兼容桥和 Responses WebSocket，都在出站前执行"先做模型范围映射、再比较上限"的规则；拒绝属于本地的业务限制，不进入提供商故障转移，也不计入 SLA 失败。Messages 只对明确的 `output_config.effort` 应用策略，缺省请求的桥接默认值不受影响。部署时先执行迁移、升级全部后端实例，确认旧快照已失效、管理 API 的字段往返正确，再开放 `deny`。旧二进制会忽略新列，混跑期间不能依赖拒绝；回退时不需要删除列，但要停止写入新的动作，并重建缓存。

## 分组 OpenAI Fast 强制策略

迁移 `262_group_force_openai_fast.sql` 给 `groups` 增加默认关闭的 `force_openai_fast` 布尔列；迁移 269 之后，它只是新策略列的兼容镜像。当时管理端只允许 OpenAI 分组写入；认证快照升到 v34 后带上这个字段，网关把它转成 HTTP、Responses 和 WebSocket 请求的 `service_tier=priority`。分组级的强制不是绕过策略的旁路：全局的 Fast 和 Flex 过滤或阻断，以及 API Key 的 `force_off`，仍然作用于最终的请求体。

迁移只新增列，可以重复执行，但旧后端不读取这个策略。发布时先完成数据库迁移和全部后端实例的升级，确认旧的 v33 快照被拒绝并重建，再开放管理端的开关；回退旧二进制不会删除这一列，但会忽略新配置，混跑期间不能依赖分组级的 Fast。

## 缓存写入 1 小时分档

迁移 `261_channel_cache_write_1h_pricing.sql` 给渠道（274 之后叫价格配置）的模型价、渠道的 token 区间、提供商统计的模型价和提供商统计的区间，增加可空的 `cache_write_1h_price`。NULL 表示沿用旧的 `cache_write_price`，两档同价；填 0 表示 1 小时缓存写入免费。迁移只新增列，可以重复执行。用量里有 5 分钟和 1 小时的明细时，应用层分别计算，否则按聚合的缓存创建 token 回退，历史记录的金额不变。

升级后抽样验证：旧的配置仍然返回相同的总价，新配置 5 分钟和 1 小时字段的 API 往返、提供商统计的成本、模型广场的展示；确认所有实例都已运行包含这个迁移的版本，再开放 1 小时字段的写入。

<a id="api_key_billing_cache_invalidation"></a>
## API Key 结算字段的缓存失效

迁移 `258_extend_api_key_auth_cache_invalidation.sql` 通过 `CREATE OR REPLACE FUNCTION`，扩展已有的 API Key 鉴权缓存 outbox 触发器，把 `billing_mode` 和 `preferred_subscription_id` 的变化纳入失效条件。它依赖迁移 237 已经创建的列，没有修改历史迁移文件。旧实例可以继续运行；升级到新版本后，确认自动改绑产生的 Key 快照，在多实例之间及时失效，并抽样检查 outbox 只保存哈希、不保存明文的 Key。

## 创作台持久状态与 outbox

迁移 `257_creative_run_durable_settlement.sql` 给 `creative_runs` 增加 provisioning、provider 成功记录、settlement 和 release 重试，以及 reconciler 用的字段，创建 `creative_run_outbox`，并为每个用户和分组 active 的 `creative_studio` 托管 Key 增加部分唯一索引。它把已有的 queued 和 running 任务回填为可以继续入队的阶段，Redis 图片的 TTL 不变。

发布时先执行迁移，再部署兼容旧状态的应用版本，并启动 outbox 和临时数据的 reconciler；观察 `settlement_pending`、`release_pending`、lease lost、result lost 和 outbox 的延迟之后，再调高恢复告警的阈值。

## 国产供应商用户平台额度

迁移 277 已经归档并删除了用户平台额度和它的注册默认设置，当前版本不再授予、预检或结算这项额度。本节只记录旧版本的升级要求。

迁移 `248_allow_cn_user_platform_quotas.sql` 由上游迁移 224，按本 fork 当时的最大编号 247 递增而来，仓库没有保留上游的原文件名。它只替换 `user_platform_quotas.platform` 的 CHECK 约束，把 `kimi`、`zhipu`、`deepseek` 加进原来的六个平台，和应用层的九平台 allowlist 对齐。

这个迁移不回填已有用户的 CN 额度行。已有用户缺少这些行时，按当时的规则视为无限额；管理员手动保存九平台配置后，才创建对应的记录；新注册的用户，在一次批量写入里创建全部九个平台的默认快照。

## 分组逐模型定价

迁移 281 已经删除了本节新增的列，分组价格改由价格配置管理。本节只记录旧版本的升级要求。

迁移 `246_group_model_pricing.sql` 由上游迁移 221 按 fork 当时的最大编号递增而来，给 `groups` 增加默认 `TRUE` 的 `long_context_pricing_enabled` 和可空 JSONB `model_pricing`，并把全部存量分组回填为开启。前者只控制模型内置的长上下文阶梯，不会压平渠道手动设置的 token 区间；后者保存分组的逐模型价卡，当时结算和模型市场都按"分组 > 渠道 > 内置"的顺序解析。新建的管理请求省略开关时，也按开启处理，应用层写入布尔零值不会绕过数据库的默认值。

这个迁移只新增列，可以随新版本正常执行；但旧实例不认识分组价卡，混跑期间不能开放或修改 `model_pricing`，否则同一个分组命中不同版本的实例时，展示价和实扣可能不一致。先完成全部后端的升级、确认认证缓存已重建，再开放新的管理端。

分组和渠道价卡的功能对齐，使用原来的 `model_pricing` JSONB，没有新增迁移：分组支持上下文区间、Fast/Flex、Max 推理和分时倍率，只有倍率的条目继承渠道的基础价，复制分组时保留价卡和长上下文开关。发布前要核对历史上通过 API 写入的 token 区间：旧版本忽略它们，新版本会把有效的区间用于实际结算。

## Grok 媒体、搜索和 Voice 定价

迁移 272 和 281 已经删除了分组上的媒体价格；当前的 Grok 媒体、搜索和 Voice 价格在价格配置里。本节只记录旧版本的升级要求。

迁移 `242_group_video_model_prices.sql` 给分组增加可空的 JSONB `video_model_prices`，按 Grok 视频模型族和分辨率保存每秒价格；`243_group_audio_voice_pricing.sql` 增加 Realtime 每分钟、TTS 每百万字符和 STT 每小时的价格；`244_group_search_price_per_1k.sql` 增加搜索每千次的价格。三类价格都用 `NULL` 表示使用代码默认值，`0` 表示免费。

迁移 `245_clear_non_grok_video_generation_config.sql` 清除非 Grok 分组的旧视频价格（原 SQL 保留了未启用的平台值 `composite` 这一历史例外），其他平台不会被误认为有视频能力。清理前一次性创建 `groups_video_price_backup_245`，保存受影响分组的旧列和 JSONB；`CREATE TABLE IF NOT EXISTS` 保证重放不会覆盖第一次的快照。这个历史例外不代表本分支支持该平台；原迁移保持不变，以满足校验和的约束。确认不需要恢复后，可以手动删除备份表。

这四个文件由上游迁移 217 到 220，按 fork 当时的最大编号重新编号为 242 到 245。

## OpenAI 提供商级长上下文开关下线

迁移 `241_remove_openai_long_context_billing_toggle.sql` 幂等地删除迁移 203 创建的两个提供商同步触发器和两个函数，并从所有提供商的 `extra` 里删除 `openai_long_context_billing_enabled`，其他 JSONB 数据保留。新服务把这个键当作废弃的输入：提供商的创建、更新、批量更新、导入和 CRS 同步，即使收到非法的类型，也直接丢弃，不保存，也不返回旧的校验错误。

单个提供商的整体替换更新，如果只带了这个废弃键，等同于没有提供 `extra`，其他配置不会被清空；明确的 `extra:{}` 仍然表示清空允许清空的字段；废弃键和有效字段同时出现时，只处理有效字段。提供商数据导入在计算幂等指纹之前丢弃这个键，所以旧键缺失、任意旧值和非法类型都算同一个请求。

升级后，长上下文的用户价格由价格规则和分组倍率决定（当时的规则见[路由与结算](../domains/routing_and_billing.md)）。提供商统计和提供商的 `quota_used` 统一使用 `COALESCE(provider_stats_cost, total_cost) × provider_rate_multiplier`，提供商成本明确为零时不累计额度；用户余额、订阅和 API Key 配额继续使用 `ActualCost`。

升级方式：停机。确认触发器、函数和旧键都已清理，抽样核对模型广场的区间价和实扣一致后，再扩容其他新实例。旧实例连接已迁移的数据库后，可能重新写入废弃键，或者按旧的提供商开关算出不同的用户价格。

发布说明需要写明：之前关闭了提供商开关的 OpenAI 请求，超过模型阈值后，会开始按模型广场的长上下文价格扣费。

## 通用高级调度器

迁移 `238_generalize_advanced_scheduler.sql` 给 `groups` 增加有约束的 `scheduler_type`，默认 `basic`，并把旧的 OpenAI 实验调度器转成按分组选择的通用高级调度器。旧设置 `openai_advanced_scheduler_enabled=true` 时，只有已有的 OpenAI 和 Grok 分组回填为 `advanced`；开关为 false 或不存在时，所有存量分组保持基础调度。其他平台不会被自动升级，新建的分组总是基础调度。

迁移把旧的粘性、订阅优先、Top-K 和评分权重设置复制到 `advanced_scheduler_*`，然后删除全部 `openai_advanced_scheduler_*` 键，没有数据库别名，也没有读取回退。部署前，把配置里的 `gateway.openai_ws.lb_top_k`、`gateway.openai_ws.scheduler_score_weights.*`、`gateway.openai_scheduler.sticky_escape_*` 换成 `gateway.advanced_scheduler`；当时的版本会拒绝旧配置（后来改为加载时自动映射，见[配置](../interfaces/configuration.md#高级调度参数)），管理设置 API 也会拒绝旧字段。

迁移 `239_add_group_advanced_scheduler_overrides.sql` 给 `groups` 增加非空的 JSONB `advanced_scheduler_overrides`，默认 `{}`，并约束顶层是对象。它不修改已有分组的模式和全局权重；空对象让所有分组继续继承网关的通用参数。升级后，管理端可以只为高级分组保存需要偏离全局的字段，认证快照的版本会再次提升，避免旧缓存缺少覆盖值。

迁移 `240_remove_account_group_priority.sql` 幂等地删除 `provider_groups.priority`，以及依赖这一列的三个索引。这个字段没有完整的产品配置入口，实际调度和模型市场都使用 `providers.priority`；迁移之后，ProviderGroup 只记录提供商和分组的成员关系。迁移先按名称删除历史索引，再删除列，完整历史 schema 和缺少部分索引的兼容数据库都能处理。

升级方式：停机，前端也要同时升级。同时备份配置。确认认证快照因版本变化已经重建、分组模式和通用设置符合预期，并抽样核对提供商仍按全局优先级排序后，再扩容其他新实例。

## API Key 结算模式与批量图片快照

迁移 `237_add_api_key_billing_modes.sql` 给 `api_keys` 增加非空的 `billing_mode`（默认 `auto`）和可空的 `preferred_subscription_id`，并在 `batch_image_jobs` 增加同名的提交时结算快照列。它只新增列，不需要回填存量的 Key：旧记录自动保持"订阅优先、余额补足"的行为；旧的批量任务也按 `auto` 兼容结算。

迁移完成后，认证缓存的版本让旧快照失效并重建，缓存里不会缺少结算字段。SQL 的列和旧二进制兼容，但同一个部署里，不能让旧实例继续处理用户新配置的指定订阅或只用余额的 Key；先完成全部后端实例的升级，再在面板上开放这项配置。升级后至少抽样验证：个人和团队 Key 的订阅选择、套餐限定分组的拒绝、指定订阅额度用完时不扣余额、只用余额时不使用订阅，以及批量图片提交后修改 Key 配置，仍按提交时的快照冻结、结算和释放。

## 上游声明倍率探测下线

迁移 `236_remove_upstream_billing_probe.sql` 幂等地删除提供商 JSONB 里的 `upstream_billing_probe`、`upstream_billing_probe_enabled`，并删除设置 `upstream_billing_probe_settings`、`openai_low_upstream_rate_priority_enabled`、`openai_oauth_scheduling_rate_multiplier`、`openai_advanced_scheduler_weight_upstream_cost`。

迁移不改动其他的提供商 extra 和设置。旧配置项 `gateway.openai_ws.scheduler_score_weights.upstream_cost` 已经没有作用，升级前从配置文件、Secret 和环境模板里删除。

升级方式：停机。这次升级没有兼容路由，也没有弃用期，旧进程可能把已删除的数据重新写回去。`GET /v1/sub2api/billing` 和全部 `/api/v1/admin/providers/*upstream-billing-probe*` 路由，在新版本上返回普通的 `404`。

升级不扫描、也不清理 Redis。遗留的探测 leader lock 按原来 2 分钟的 TTL 自然过期，这不会恢复任何探测任务。升级后确认：迁移可以重复执行，无关的提供商 extra 和设置保持不变，Ollama Cloud、额度和 endpoint capability 的探测正常，声明倍率不再影响提供商的排序和评分。

## 分组客户端协议

迁移 273 已经把本节的字段改名为 `allowed_protocols`。本节只记录旧版本的升级要求。

迁移 `235_add_group_allowed_client_protocols.sql` 给分组增加非空的 JSONB `allowed_client_protocols`，按当时六个平台升级前的实际路由行为回填。OpenAI 是否加入 Messages，取自旧的 `allow_messages_dispatch`；其他已有的平台，按各自的迁移矩阵回填。后来新增的 Kimi、Zhipu、DeepSeek 不需要历史回填，新建的分组默认启用 Messages、Responses 和 Chat。旧列作为弃用管理 API 字段的数据库镜像保留，不支持新旧二进制共存。

数据库的默认值是空数组，作为绕过管理服务直接写分组时的 fail-closed 默认值。空数组对所有平台都是明确有效的策略，新代码不会按旧矩阵恢复或自动补上协议；管理 API 创建时省略这个字段，仍然使用各平台的新建默认值。

这次变更按一次性升级发布，不支持新旧后端或前后端混合运行。认证快照的版本随字段增加而升级，部署前遗留的 Redis 快照因为版本不匹配失效，并从已回填的数据库重建。升级后至少抽样验证：OpenAI 旧开关为 true 和 false 的情况、任意平台的空集合，以及 Gemini Responses 的非流式和 SSE 请求。

## 自研异步图片任务下线

包含迁移 `234_remove_async_image_storage_setting.sql` 的版本，会立即删除自研的 OpenAI 和 Grok 异步图片路由、后台的对象存储设置和 `image_storage_config` 数据库记录。这是破坏性的升级，没有任务排空、兼容查询或旧任务恢复。发布的 tag notes 需要写明这项变化。

升级前，先记下旧异步图片设置使用的 bucket 和 prefix，并从配置文件、Secret 管理和部署环境里删除 `image_storage` 和 `IMAGE_STORAGE_*`。旧进程会缓存已经解析的对象存储客户端，所以不能和新版本滚动重叠：先把全部旧实例移出流量并停止，再启动新版本。

历史的 Redis `image_task:*` 键，按原来最长 24 小时的 TTL 自然过期，不做全库扫描或清空。历史的 S3 和 R2 图片不会自动删除；升级后，先列举或 dry-run 旧的前缀，确认它和 `backups/` 或其他业务前缀不重叠，再由运维用对象存储工具定向删除。迁移完成后，数据库里不再有旧的存储位置，所以记录 bucket 和 prefix 要在升级前完成。

旧的任务 ID 在新版本上直接返回普通的 `404`，进程内的在途任务随旧实例停止而终止。回退旧二进制不会恢复已经删除的后台设置；只有手动恢复旧配置，才能重新启用旧版本的这个功能。

相关文档：[部署与数据库迁移](deployment_and_migrations.md)、[开发、验证与上游同步](development_workflow.md)、[运维目录](index.md)。
