# 网关策略控制

本文描述分组、渠道、账号和全局运行设置共同形成的网关策略。它关注策略所有权、优先级和失败边界，不重复平台协议转换、账号调度算法或具体 HTTP 字段。

## 章节导航

- [策略层级](#策略层级)：判断配置应归属分组、渠道、账号还是全局。
- [协议与能力准入](#协议与能力准入)：修改 Messages、Live、媒体或客户端限制时读取。
- [模型路由](#模型路由)：修改模型重定向、映射、白名单或 fallback 时读取。
- [认证与隐私](#认证与隐私)：修改 OAuth-only、privacy 或会话隔离时读取。
- [推理与 Header](#推理与-header)：修改 reasoning、beta 或上游 Header 时读取。
- [重试与降级](#重试与降级)：修改状态码、分组回退或失败分类时读取。

<a id="gateway_policy_layers"></a>
## 策略层级

| 层级 | 拥有的策略 | 不应承担的责任 |
| --- | --- | --- |
| API Key | 分组选择、复合前缀、Key 级模型重定向、Key 限额 | 不能改变上游账号凭据或平台 |
| Group | 上游平台、基础/高级调度器选择、高级调度器稀疏覆盖、客户端协议/媒体准入、fallback、OAuth/privacy、推理上限、模型可见性、Fast/Standard 计费和 RPM | 不保存真实上游 token |
| Channel | 一个分组的模型映射、定价和功能配置 | 不能把不存在的账号能力变成可调度能力 |
| Account | 凭据、代理、模型映射/白名单、重试状态码、临时不可调度、Header override 和 capability | 不能绕过分组对用户公开的能力 |
| Setting/config | 高级调度评分参数、跨分组运行策略、兼容开关、默认 Header/UA、缓存和安全策略 | 不能替代分组的调度器选择或每个账号的权威运行状态 |

每个 Group 最多关联一个 Channel，请求在该渠道配置下选择账号。字段属于哪一层决定缓存失效范围、管理权限和审计来源，不能为了前端表单方便复制为多份互相覆盖的配置。

## 协议与能力准入

Group 的 `platform` 表示上游平台；`allowed_protocols` 控制客户端入口，`protocol_fallbacks` 控制账号无法原生承接时的唯一转换目标。账号 `credentials.upstream_protocols` 只勾选原生能力。完整 24 项目录、平台/认证边界和新建默认值由后端提供给前端，详见[统一协议能力](../interfaces/protocol_capabilities.md)。

协议配置是硬资格：候选原生优先，没有原生或指定转换路线则不参与评分；所有旧的模型、配额、媒体资格和会话约束继续生效。切号重新解析，不根据请求失败临时挑选另一协议。分组转换目标不必作为客户端入口开放。

Images 生成/编辑独立控制，Responses 图片工具使用 `responses_image_policy` 的继承、开启桥接、关闭桥接、屏蔽四态。分组显式配置优先，继承保留既有账号/渠道/全局链；既有视频和批量作业资源操作继续按任务归属授权，不依赖新建入口开关。

## 模型路由

共同模型链为：复合 Key 前缀解析、Key 级精确重定向、Group/Channel 路由、Account 映射与白名单。每层只执行自己的单步规则；不要依赖隐式多跳别名链。请求模型、上游模型和计费模型分别记录，响应中的模型恢复以客户端契约为准。

Group 可以启用模型路由、默认映射和 OpenAI Messages 专用模型配置。OpenAI Messages 专用配置中的精确规则优先于系列规则，只有非空目标值才生效；空配置或空系列字段不使用内置默认模型，当前渠道模型保持不变并继续进入账号层。Channel 决定分组内的映射、价格和功能；Account 则处理供应商或站点差异。可见模型只包含当前可请求结果，未知或歧义定价以未定价表达，不使用猜测价格。

客户端限制的逐组读取与回退由 routing 的 `ResolveClientGroup` 唯一执行。通用入口保留原读取错误，OpenAI 快照入口仍可保留未解析分组；客户端识别在读取当前分组后按原短路顺序执行。强制平台旁路和最终分组的资金、权限复查仍由各入口拥有。

Group 的 fallback 包括普通 fallback、invalid-request fallback 和 unavailable fallback。它们是显式的跨分组策略：目标分组仍要重新执行平台、Key、模型、权限、计费和 `scheduler_type` 约束，不能只把原账号列表替换掉。循环、目标失效或策略不匹配必须终止。

`scheduler_type` 仅属于 Group，`basic` 为默认值，`advanced` 表示该分组在硬过滤后使用通用高级评分。高级调度的 Top-K、评分权重、粘性加权和订阅优先是网关通用设置，不存在全局启用开关；设置不能把基础分组隐式切换为高级，也不能让 OpenAI/Grok 特有能力作用于不具备该能力的账号。

高级分组可在 `advanced_scheduler_overrides` 保存稀疏参数覆盖。每个未出现的字段依次继承数据库通用设置和 `gateway.advanced_scheduler` 配置默认值；覆盖范围包括 Top-K、评分权重、粘性/订阅开关、错误率/TTFT 两项 EWMA alpha，以及 sticky escape 开关和两个阈值。出现的字段（包括 `false` 和 `0`）以分组值为准，alpha 必须大于 0 且不超过 1，TTFT 阈值必须为正数，错误率阈值必须在 `0..1`。空对象表示全部恢复继承。基础分组即使历史上留有该对象也不读取它，切换为高级后才重新生效。

## 认证与隐私

`require_oauth_only` 排除 API Key 等非 OAuth 账号；`require_privacy_set` 要求上游隐私状态已经确认。OpenAI/Antigravity 的 privacy 检查和设置可在创建、刷新或维护流程触发，但请求热路径只能使用当前已验证状态，不能假定刷新成功。

OpenAI 客户端访问裁决直接使用 account 的原生检测端口，按需读取 Header 字符串，TLS 路由只传入匹配结果；HTTP、Live 和自动探针复用同一规则。WS 传输选择直接调用 gateway/provider 对账号与配置的投影和 egress 的唯一决策，配置与动态客户端放行设置仍在原时点读取。

会话隔离与粘性约束防止不同账号、团队或用户上下文互相复用。OAuth passthrough、Claude Code-only 和允许客户端策略必须与账号类型共同校验；客户端伪造 User-Agent 不能自动获得额外权限。

## 推理与 Header

Group 可限制最大 reasoning effort 并配置 effort 映射，平台适配器再把统一值转换为 Anthropic thinking、OpenAI reasoning 或 Qoder/Gemini 原生字段。显式关闭、平台不支持和管理员上限的优先级必须可预测；无效字符串通常保持默认或被拒绝，不能静默提升推理强度。

HTTP 在原策略改写位置捕获客户端档位，`gateway/requeststate` 保存请求策略快照并执行报文字段改写；映射、上限和错误类型仍由 routing 拥有。WS 使用相同报文规则和原生用量解码器，逐轮区分客户端原档位与最终上游档位。缺省桥接档位不自动成为客户端请求，显式字段被最终转换删除后也不再从模型后缀补回；异步完成只读取已经固化的值。

Fast/Ultra Fast 的系统、分组与 Key 裁决统一由 `gateway/tierpolicy.Resolve` 执行，HTTP 报文与 WS 帧都使用该结果。系统短路和分组强制关闭不会触发后续 Key 开启查价；Key 强制开启产生的新档位仍须再次接受系统策略。执行适配投影资格及惰性读取端口，协议拒绝、HTTP/SSE 错误与 WS 错误帧分别由原生 Adapter 输出。

Header 策略来自平台默认、全局设置和允许的账号 override。认证、hop-by-hop、Host/长度等受保护头不能被任意覆盖。Anthropic beta/cache、OpenAI UA/客户端元数据、Claude Code mimicry 和 dateline/metadata 兼容均应在平台边界内处理，并接受出站安全校验。

## 重试与降级

账号可配置额外重试状态码和临时不可调度规则，但最终是否换账号还取决于平台错误分类、响应是否开始、attempt 上限和上下文截止时间。401/403、429、5xx、内容策略和本地拒绝不能只按 HTTP 数字归为一类。

重试发生在同一分组的账号 attempt 内；fallback 发生在明确的跨分组策略上；错误响应规则只改变最终客户端展示。修改这些阶段时要验证不会重复扣费、重复写流或把本地策略拒绝计作账号故障。

相关文档：[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[网关错误响应策略](../interfaces/gateway_error_policy.md)、[路由与结算](routing_and_billing.md)。
