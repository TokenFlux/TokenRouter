# 搜索编排与供应商额度

本文说明 Brave/Tavily 搜索的配置发布、供应商选择、额度和退出行为，以及它与网关工具模拟的分工。Grok 原生搜索、独立的 Web/X 搜索和 OpenAI AlphaSearch 由各自的网关和上游代码执行。

## 章节导航

- [职责与入口](#search_ownership)：定位配置、工具协议和供应商调用。
- [配置发布](#search_publication)：修改保存、缓存与热更新时读取。
- [选择与代理](#search_selection)：修改候选及故障切换时读取。
- [额度与取消](#search_quota)：修改计数、退额和窗口时读取。
- [生命周期](#search_lifecycle)：修改配置替换或停机时读取。
- [验证入口](#验证入口)：核实失败处理和并发行为。

<a id="search_ownership"></a>
## 职责与入口

`search` 拥有配置快照、当前 Manager 注册表、供应商选择和额度规则。`search/provider` 执行 Brave/Tavily HTTP 请求，`search/rediscache` 存储计数与代理故障标记。管理接口位于 `search/httpapi`，提供配置、测试及额度查询和重置。

`gateway/searchtools` 识别工具、判断提供商与渠道的启用策略，并生成客户端协议事件和合成用量。Anthropic API Key 提供商支持 `default`、`enabled`、`disabled` 三态；历史布尔值 `true` 解释为启用，`false` 回到默认。工具最终是否执行，还受渠道和全局配置约束；提供商上的选项只影响启用判断，供应商配置保持原样。

app 将同一个 ConfigService、Registry 和渠道能力绑定到网关。搜索额度统计的是对外部供应商的调用次数，用户资金由 billing 结算；资金是否提交以 billing 为准，搜索计数说明不了这件事。网关调用顺序见[审核与搜索协作](../architecture/gateway_request_lifecycle.md#moderation_search_boundaries)。

<a id="search_publication"></a>
## 配置发布

配置保存在 `web_search_emulation_config`。正常读取缓存 60 秒，数据库错误缓存 5 秒，回源预算 5 秒；缺键返回空配置。读取、管理展示和供应商配置各用一份独立副本，调用方修改副本不会影响运行中的快照。

保存先校验并合并需保留的 API Key，持久化成功后推进本进程的发布代次、替换缓存并构造 Manager。较早开始的回源或 Manager 构建即使更晚返回，也覆盖不了后一次保存的结果。发布代次只存在于进程内；缓存到期后按上述预算回源。

配置中的空 API Key 可保留已有密钥；开启功能时，合并后仍缺密钥的供应商会被拒绝。搜索配置的管理 GET/PUT 接口调用 `PopulateWebSearchUsage`，补充用量并保留 `api_key`。需要隐藏密钥的调用方使用 `SanitizeWebSearchConfig`，它清空密钥并保留 `api_key_configured` 标记。供应商的代理无法解析时，跳过这个供应商，请求不会改走直连。配置接口见[搜索配置发布](../interfaces/configuration.md#search_configuration)。

<a id="search_selection"></a>
## 选择与代理

Manager 先过滤缺少密钥、已过期及代理不可用的供应商，再按剩余额度计算权重。有正剩余额度的候选优先，每次选择为候选固定一次随机因子后排序；未设置额度或当前剩余额度不为正的候选排在这组之后；是否超额由尝试前的原子预占最终判断。每次尝试前还会检查请求是否取消，并执行额度预占。

提供商代理优先于供应商代理。供应商自己的代理故障会标记五分钟不可用，并允许尝试下一供应商；提供商代理由各供应商共用，故障时把 `ErrProxyUnavailable` 返回给调用方处理，其他供应商也走同一个代理，所以不再逐个重试。请求取消时，既不标记代理故障，也不尝试下一个供应商。

管理测试按配置顺序尝试有密钥且未到期的供应商，成功即返回。管理测试不预占额度，也不按生产搜索的额度权重和代理故障标记筛选供应商。生产搜索和管理测试在代理故障时都直接失败。

<a id="search_quota"></a>
## 额度与取消

Redis 计数键为 `websearch:quota:<provider>`，按供应商类型区分。预占使用 Lua 原子递增，并在首次创建或缺少 TTL 时补上过期时间。计数超过限额时归还本次递增并跳过该供应商；正常成功保留计数，失败只释放本次已确认取得的预占，重复释放执行一次。

请求取消后的退额使用脱离原取消信号、最多三秒的清理预算。Redis 不可用或递增结果不确定时，搜索按现有策略放行，计数是否取得保持未知，系统也不做推测性的补偿。退额失败时记录日志，这部分额度可能无法恢复。

设置订阅起点时，下一月度日期按 UTC 计算；目标月份没有这一天时，取那个月的最后一天。计数 TTL 为到该日期的时长再加 24 小时缓冲，未设置起点时为 32 天。计数在键过期、重新创建时才归零，所以名义上的订阅日期并不是精确的清零时刻。管理员重置直接删除计数键。

<a id="search_lifecycle"></a>
## 生命周期

搜索在 HTTP 开放前初始化。配置替换发布新的 Manager，已有请求继续使用原配置快照；旧 Manager 退役时关闭空闲连接，在途调用结束后再清理其连接。

所有配置代次共用 WorkGroup。停止后拒绝新搜索，等待请求及退额清理；超时时报告未完成的操作，预算用完不算排空成功。完整关闭顺序见[系统架构](../architecture/system_architecture.md#startup_and_shutdown)。

## 验证入口

`backend/internal/search/config.go`、`manager.go`、`lifecycle.go` 和 `rediscache/state.go` 实现配置、请求编排、退出等待和额度存储。配置测试覆盖慢回源与发布竞争、快照副本。`manager_test.go`、`lifecycle_test.go` 和 `quota_cleanup_scenario_test.go` 覆盖重复退额、预占结果不确定、取消预算与停机等待。provider 测试验证代理、HTTP 和 Redis 协作。工具启用规则由 `backend/internal/gateway/searchtools/` 的实现和测试核实。
