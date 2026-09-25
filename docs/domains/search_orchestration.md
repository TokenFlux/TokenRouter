# 搜索编排与供应商额度

本文说明 Brave/Tavily 搜索的配置发布、供应商选择、额度和退出行为，以及它与网关工具模拟的分工。Grok 原生搜索、独立 Web/X 搜索和 OpenAI AlphaSearch 的平台执行仍由各自网关与上游实现负责。

## 章节导航

- [职责与入口](#search_ownership)：定位配置、工具协议和供应商调用。
- [配置发布](#search_publication)：修改保存、缓存与热更新时读取。
- [选择与代理](#search_selection)：修改候选及故障切换时读取。
- [额度与取消](#search_quota)：修改计数、退额和窗口时读取。
- [生命周期](#search_lifecycle)：修改配置替换或停机时读取。
- [验证入口](#验证入口)：核实失败和并发边界。

<a id="search_ownership"></a>
## 职责与入口

`search` 拥有配置快照、当前 Manager 注册表、供应商选择和额度规则。`search/provider` 执行 Brave/Tavily HTTP 请求，`search/rediscache` 存储计数与代理故障标记。管理接口位于 `search/httpapi`，提供配置、测试及额度查询和重置。

`gateway/searchtools` 识别工具、判断账号与渠道的启用策略，并生成客户端协议事件和合成用量。Anthropic API Key 账号支持 `default`、`enabled`、`disabled` 三态；历史布尔值 `true` 解释为启用，`false` 回到默认。最终工具执行还受渠道和全局配置约束，账号选项不直接改变供应商配置。

app 将同一个 ConfigService、Registry 和渠道能力绑定到网关。搜索额度衡量外部供应商调用，用户资金由 billing 结算；搜索计数不能证明资金已提交。网关调用顺序见[审核与搜索协作](../architecture/gateway_request_lifecycle.md#moderation_search_boundaries)。

<a id="search_publication"></a>
## 配置发布

配置保存在 `web_search_emulation_config`。正常读取缓存 60 秒，数据库错误缓存 5 秒，回源预算 5 秒；缺键返回空配置。读取、管理展示及供应商配置使用独立副本，调用方不能修改运行快照。

保存先校验并合并需保留的 API Key，持久化成功后推进本进程的发布代次、替换缓存并构造 Manager。较早的回源或 Manager 构建即使晚返回，也不能覆盖后一次保存。代次只存在于进程内，不构成跨实例通知协议。

配置中的空 API Key 可保留已有密钥；开启功能时，合并后仍缺密钥的供应商会被拒绝。管理响应使用专用脱敏投影。供应商代理无法解析时跳过该供应商，不能改为直连。配置接口契约见[搜索配置发布](../interfaces/configuration.md#search_configuration)。

<a id="search_selection"></a>
## 选择与代理

Manager 先过滤缺少密钥、已过期及代理不可用的供应商，再按剩余额度计算权重。有正剩余额度的候选优先，每次选择为候选固定一次随机因子后排序；未设置额度或当前剩余额度不为正的候选排在这组之后；是否超额由尝试前的原子预占最终判断。每次尝试前还会检查请求是否取消，并执行额度预占。

账号代理优先于供应商代理。供应商自己的代理故障会标记五分钟不可用，并允许尝试下一供应商；账号代理由各供应商共用，故障时返回 `ErrProxyUnavailable` 给调用方处理，不继续通过同一代理重试所有供应商。请求取消不会标记代理故障或尝试下一供应商。

管理测试按配置顺序尝试有密钥且未到期的供应商，成功即返回；它不预占额度，不使用生产搜索的额度加权或代理故障标记筛选。生产搜索与管理测试都不因代理故障自动直连。

<a id="search_quota"></a>
## 额度与取消

Redis 计数键为 `websearch:quota:<provider>`，按供应商类型区分。预占使用 Lua 原子递增，并在首次创建或缺少 TTL 时补上过期时间。计数超过限额时归还本次递增并跳过该供应商；正常成功保留计数，失败只释放本次已确认取得的预占，重复释放执行一次。

请求取消后的退额使用脱离原取消信号、最多三秒的清理预算。Redis 不可用或递增结果不明确时，搜索按现有策略放行，但不猜测是否取得计数，也不执行猜测性补偿。退额失败记录日志，不能承诺一定恢复额度。

设置订阅起点时，下一月度日期按 UTC 计算；月末溢出收敛到目标月份最后一天。计数 TTL 为到该日期的时长再加 24 小时缓冲，未设置起点时为 32 天。实际计数通过键过期后重新创建而重置，不能把名义订阅日期描述为精确的计数清零时点。管理员重置直接删除计数键。

<a id="search_lifecycle"></a>
## 生命周期

搜索在 HTTP 开放前初始化。配置替换发布新的 Manager，已有请求继续使用原配置快照；旧 Manager 退役时关闭空闲连接，在途调用结束后再清理其连接。

所有配置代次共用 WorkGroup。停止后拒绝新搜索，等待请求及退额清理；超时报告未完成操作，不将预算耗尽视为排空成功。完整关闭顺序见[系统架构](../architecture/system_architecture.md#startup_and_shutdown)。

## 验证入口

核心依据是 `backend/internal/search/config.go`、`manager.go`、`lifecycle.go` 和 `rediscache/state.go`。配置测试覆盖慢回源与发布竞争、快照副本；`lease_test.go` 覆盖重复退额、不明确的预占、取消预算与停机；provider 测试验证代理、HTTP 和 Redis 协作。工具启用规则由 `backend/internal/gateway/searchtools/` 的实现和测试核实。
