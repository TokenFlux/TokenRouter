# 提供商维护

本文描述上游提供商的后台维护流程：凭据刷新、管理操作、健康测试、额度和能力探测、临时不可调度和自动恢复。创建表单的字段见平台文档，请求时的调度评分见调度架构文档。

## 章节导航

- [凭据刷新](#account_credential_refresh)：修改刷新候选、并发、重试或状态同步时读取。
- [管理操作](#管理操作)：修改提供商的编辑、导入导出、同步、批量操作、隐私或列表时读取。
- [状态与临时不可调度](#状态与临时不可调度)：修改错误、限流或恢复时间时读取。
- [提供商测试与自动恢复](#提供商测试与自动恢复)：修改定时测试或恢复条件时读取。
- [额度与能力探测](#额度与能力探测)：修改上游 usage、quota 或 endpoint capability 时读取。
- [OAuth 用量查询](#oauth-用量查询)：修改 OAuth 和 Setup Token 的用量展示、缓存或写回时读取。
- [运维诊断](#运维诊断)：排查刷新堆积、误封禁或提供商抖动时读取。

<a id="account_credential_refresh"></a>
## 凭据刷新

### 后台刷新

`provider.BackgroundRefreshService` 分页读取需要维护的提供商，按平台的 refresher 判断资格，并对每个 provider 应用独立的并发和 QPS 上限、单次 attempt 超时、每轮总超时和有上限的退避。OAuth、Setup Token 和 Qoder COSY 的候选规则各不相同；API Key、Bedrock 和 Service Account 通常由各自的请求路径或签名 provider 管理，它们不一定有 refresh token。

app 直接构造、绑定并登记一个 `BackgroundRefreshService`，它的 `RefreshLoop` 启动后立即执行第一轮，周期有下限。分页元数据、严格的 ID 顺序、没处理完的页不推进游标、最后一页后归零，由 `RefreshCandidateScan` 管理；按平台分组和 worker 统计由 `RefreshPageProcessor` 执行。周期刷新和 Grok 的管理对账，共用同一组平台准入实例，每轮的连续失败状态互相独立。停止时，同时取消周期、扫描和按需的对账，等待实际的工作结束，并固定第一次的结果；超时不算排空，之后由 app 决定能否关闭共享的依赖。

请求路径上的 token provider 仍在使用前检查是否快过期，并用提供商级的锁避免并发刷新。后台刷新能降低热路径的延迟，但正确性不依赖它；两条路径使用相同的凭据版本和 CAS 保护，旧请求无法覆盖新的 token。

### 刷新协调与条件写入

统一的锁、重读、交换前的快照、版本写入、条件持久化和竞争恢复，都在 `provider.OAuthRefreshAPI`，由 app 持有一个实例。请求路径和后台都直接使用这个协调器和各平台的刷新器。后台刷新和 Grok 管理对账直接绑定 `BackgroundRefreshService`，六个平台的执行器和后置接口由 app 装配；供应商错误的分类在 `provider/provider`，实际的交换在 upstream。

刷新成功后，原子地更新凭据和过期时间，清理可以恢复的错误，并同步提供商缓存和调度快照。OpenAI 和 Antigravity 还可以在刷新后检查并设置 privacy 状态。刷新失败按失败阈值记录，一次瞬时的网络错误不等于永久禁用；凭据被明确撤销，或者提供商的归属失效时，才进入需要重新授权的状态。

非 Grok 的刷新成功路径，通过 `provider.CredentialRefreshWriter` 使用 `provider/postgres` 的条件写入：在同一个 Ent 事务里，按提供商的 ID、平台、类型、状态、完整的凭据和代理比较并锁住这一行，然后执行凭据清理和 outbox 写入。管理员已经更新或禁用了这个提供商时，这一轮的结果不会覆盖当前状态；调用方重新读取，也不会为这次冲突再交换一次 token。Grok 使用自己的 CAS、持久化后回读和 provider containment 错误分类。

刷新成功、要清除临时停调时，存储还会比较这一次的凭据、代理和状态，以及原来 cooldown 的期限和原因；身份或窗口已经变了，就不清 Redis 里的状态，不发布旧的提供商快照，也不再维护旧身份。清理成功时，先更新本次的健康数据，再发布缓存，避免数据库已经恢复、调度缓存却仍然停调。Antigravity 缺少 project_id 时的恢复，有自己的触发条件，清除停调本身不会触发它。

非 Grok 的后台刷新失败，通过 `RefreshFailureWriter` 比较交换时的身份后，再写入健康状态；旧的失败不会根据调用参数，覆盖管理员新设置的凭据、代理或状态。同一身份已经有更长的 cooldown 时，不会被缩短；outbox 失败按尽力而为处理。内存里的快速阻断，对刷新失败按凭据身份区分；提供商级的配额和容量阻断，范围不变。发布前复核明确的清理版本，迟到的通知无法重新装上已经清除的阻断；多份在途凭据的期限互相独立。Antigravity 的强制刷新标记，在 Extra 和 outbox 的事务里复核身份后清除；永久失败时，只有健康状态的条件写入成功后，才清除这个身份的标记。

### 管理员刷新

管理员的单个或批量刷新，使用同一个协调器，平台锁的 key 不变，在锁内重读后执行刷新。凭据通过管理校验和配置事务保存，并在行锁内比较交换时的身份；冲突时只返回最新的提供商，不再交换，也不继续执行旧结果的后续动作。这个比较字段只用于内部的 Go 调用，不接受 HTTP JSON 输入。单个和批量的管理刷新、重新授权，由 `ManagedRefreshService` 统一编排，HTTP 只负责绑定、状态码和展示。`provider.ManualCredentialExchange` 组合授权和 Qoder 的刷新接口，app 直接提供共享的传输。

手动刷新分五次独立提交：清除错误、清除全提供商限流、Antigravity 的 scope、模型限流、临时停调；每一步都比较交换时的身份和这一步原来的状态，冲突时立即停止，失败之前已经提交的步骤保留。清理运行时阻断时还校验原来的版本，之后才安装的阻断不会被清掉。

### 停止

统一的刷新协调器关闭时，拒绝新的认领，取消锁等待和在途的交换，然后在给定的预算内等待。已经接纳的等待者，拿到提供商锁之后还要复查停止屏障：持锁者先响应取消并释放了锁、等待者还没收到取消时，它也不会再启动交换。如果交换器忽略了取消，停止会报告未完成，迟到的响应也不会写入凭据；这不影响正常请求的取消来源。交换前的快照会深复制嵌套的凭据，nil 凭据转成空对象再比较。

### 活动时间和到期扫描

`provider.DeferredService` 维护 last-used 队列（只有一个），入队和取出批次互斥；旧批次写入失败时，只补回还没有新值的提供商，之后到达的活动时间不会被覆盖。停止时，取消新的周期和等待者，等正在写入的批次完成后，做最后一次 flush；这次写入同时受十秒预算和应用剩余的退出预算限制。提供商的到期扫描由 `provider.ExpiryService` 执行，每分钟一次，启动后立即执行第一轮，取消和等待受生命周期 context 控制，停止后不再扫描。

## 管理操作

### 存储和编辑

提供商的增删改查和筛选、凭据和健康状态的写入、CN 和 Ollama 的持久化快照，只由 `provider/postgres.ProviderStore` 实现。调度的查询和 outbox 由 scheduler 和它的存储适配提供，app 注入调度事件的发布接口。提供商的消费累计和额度重置 SQL 在 `billing/postgres.ProviderUsageParticipant`，在外层事务里执行时，不提交，也不发布事件；兼容入口按"先提交、再发布"的顺序执行。

提供商的创建、复制和恢复、影子关系，以及编辑和批量校验，由 `provider.Admin` 负责；Qoder 的站点和 PAT 验证，由 `provider/provider` 调用上游实现（只有一处）。普通编辑、管理员编辑、影子的代理传播和 CRS 配置写入，都会明确声明这一次要写哪些字段；`ProviderStore.UpdateConfiguration` 在事务里锁住并读取最新的记录，再合并这些字段。改名字之类的普通修改，不会写回旧的凭据、error 和 schedulable 状态或消费快照；管理员没有提供的敏感凭据子键，从锁内的最新值继承。明确的状态恢复仍然更新状态和错误消息，CRS 仍然可以写入来源状态和调度开关。

配置替换保留 billing 的消费累计，以及专用维护入口写入的最新快照，固定窗口根据锁内的累计值、在原来的时点重新计算。Ollama 的身份变化时，清理受管的会话；CN 的身份变化时，让观测失效；outbox 和配置一起提交。`last_used_at`、全提供商限流、过载和会话窗口，只由专用的运行入口维护。字段意图的声明不改变数据库、缓存和 HTTP 的格式。

### 导入、导出和同步

提供商的文件导入导出由 `provider.Archive` 负责，HTTP 在 `provider/httpapi`。管理员可以选择导出原始凭据，导出会排除影子提供商，跨调用传递时使用独立的副本。导入先处理代理，再按文件中的配置逐项创建提供商，最后执行隐私设置。`provider/transfer` 定义文件格式，省略和 null 字段按提供商创建规则处理。ID Token 通过 OpenAI 的非认证解码补齐缺失的身份提示。

Codex session 文件的导入由 `provider.CodexImporter` 负责，HTTP 直接调用这个用例，纯解析和身份索引在 provider 内部维护。只有 access token 的，按 access 的摘要匹配；完整的 OAuth 按用户和提供商兼容匹配；Agent Identity 按团队隔离，并合并 runtime。索引和导入用的 map 在跨调用传递时复制。时钟和私钥验证都由外部注入，导入时的 JWT 解码只用来补齐提示，不承担认证；OAuth 批量创建使用相同的合并、保护字段和摘要规则，供应商交换由 upstream 执行。

CRS 的同步和预览由 `provider.CRSSync` 负责，包含六类来源的规则；`provider/provider` 负责登录和导出的 HTTP，代理身份的匹配由 egress 负责。普通的部分成功、nil 和空选择、影子限制和来源字段的清理，各有既定的规则。`provider.CRSAuthorization` 直接组合 Claude、OpenAI、Gemini 的授权，导入后尽力刷新，使用生产环境的协调器和条件写入：在锁内确认来源身份，管理员的修改优先，取消后不写回；导入的成功计数和刷新是否成功无关。这条导入路径保留原来的状态资格和令牌版本字段，不使用后台刷新的 active 和过期筛选。

Grok 导入后的主动探测，由 app 注入的 `provider.GrokImportProbeScheduler`（只有一个）执行。它按需启动，有三个 worker、64 个排队位，按提供商去重；每一项真正开始执行时，才开始计算 25 秒的预算。输入是不含凭据的 ProviderSnapshot，供应商返回最小的日志观测。停机时，取消还没被领取的尽力项，取消并等待在途的项；超时报告未完成，取消不算已经探测过。异步探测失败时，提供商的创建和已经完成的导入不会回滚。

### 批量操作

批量创建、删除、刷新和清除错误由 `ManagementBatch` 执行。删除按母提供商和影子的依赖排序，并发 5；其他批量入口支持部分成功、重复 ID，并发 10，错误按固定顺序返回。批量更新凭据字段时，先验证全部对象，再逐个提供商提交字段补丁；配置事务在行锁内合并最新的凭据，校验时的旧 token 和没选中的配置不会被写回。补丁参数不接受 HTTP JSON 输入。

### 隐私设置

隐私设置的实际请求，由 `PrivacyService`（只有一个）登记生命周期，停止时取消并等待在途的请求，忽略了取消的迟到响应也不会写入。批量的后台入口保留任务完成屏障和任务名称；隐私服务停止后，不会再发起下一个请求。隐私写回在 Extra 和 outbox 的事务里，比较查询时的凭据、代理、状态和影子归属；身份变了就返回"没有可以应用的结果"，新身份不会被覆盖，旧输入的成功模式也不会被更新。没有条件写入能力的兼容构造，不会退回到无条件覆盖。普通的存储失败，按尽力而为记录日志并返回。

隐私的传输参数由 `provider/provider.PrivacyOptions` 统一组合，app 直接构造用例；OpenAI 的提供商和订阅查询使用同一个平台客户端，Antigravity 使用授权的隐私请求。Grok、OpenAI、Claude、Gemini 和 Antigravity 请求侧的 token source，由 app 直接绑定共享的缓存和刷新协调器，OpenAI 的指标和 Antigravity 的统计各只有一份；AlphaSearch 的 PAT 元数据补齐，单独绑定同一个授权实例。

### 列表、tier 和模型

提供商列表和运行状态的读取，由 `ManagementList` 和 `RuntimeStatusReader` 执行，HTTP 的 `RuntimePresenter` 只负责转换管理 DTO 和母提供商的字段。先分页和服务端排序，再观察；只有明确请求时，才查询筛选池和分组池的评分，并对候选的并集批量查询负载。实际的评分、并发、会话和 RPM 由 scheduler 提供，详细用量由 usage 提供，各自维护自己的缓存和运行状态。OpenAI 自动暂停的纯规则属于 provider，每个读取点都使用动态的默认阈值，同时包含窗口豁免、两小时的陈旧界限和缺少时间戳时的处理。

Google One 单个和批量的 tier 刷新，由 `TierManagement` 负责资格、查询集合、并发 10 和条件配置写入；Drive 的网络调用和供应商的 tier 推断，由 `provider/provider` 和 `upstream/gemini/codeassist` 协作提供。批量输入为空或损坏时，回落为最多一万条的 Google One 查询，单项失败不影响其他项。查询开始时固定身份，保存时在配置的行锁内复核，只合并 tier_id 和这一轮的 Drive 字段；管理员的新 token 和没选中的 Extra 不会被旧快照覆盖，outbox 失败时回滚这一次的配置。身份复核使用已有的字段，没有跨实例的协调。

管理端的可用模型由 `routing.AdminCatalog` 合并统一目录与提供商配置，再检查提供商的白名单和执行资格。空白名单允许目录未知的型号，Spark、OAuth、站点及协议规则照常检查。测试和定时测试选择器显示实际模型 ID。Jev 手动测试默认填入 `jev-latest`，其他平台等待管理员选择型号。批量测试使用勾选的候选型号，默认提示词独立初始化。

实时模型同步请求由 `ModelSyncService` 跟踪在途的调用，停止时取消并等待；构造时不请求供应商。临时凭据的预览不持久化，错误格式只由 provider 定义；`provider/provider.ModelCatalogue` 负责实际的 endpoint、Header、响应报文解析和读取上限，app 直接绑定提供商存储和凭据来源。模型预览和提供商测试共用一个 ProbeTasks 范围，持久化的提供商仍由同一个 task 协调器串行处理。

提供商管理的 HTTP 路由绑定 `provider/httpapi` 的具体处理器，测试直接验证所属模块。高级调度诊断通过只读的安全数据，调用 scheduler 的同一个评分算法；app 保留同一个反馈实例和 gateway 的绑定顺序。

执行入口、管理接口和调度快照使用同一套提供商规则，但各自的公开数据格式不同。执行目标由提供商记录和请求路线组成，复制时保留自引用的关联和 map 的隔离。提供商和 billing 的 SQL 实例只在 app 里构造一次，配置更新、CAS 和消费累计由各自的存储负责。

## 状态与临时不可调度

`provider.HealthService` 负责：通用错误规则的匹配、明确的错误和池模式的优先级、认证失败和过载的状态转换、403 累计冷却、CN 可恢复的冷却、429 默认回避、流超时计数阈值，以及提供商和模型的额度阈值判断。供应商报文的分类和模型的规范化，由执行适配提供。持久化、缓存和调度反馈的顺序不变，长期状态和临时窗口互不代替。临时停调、403 和超时计数，由 app 构造的 `provider/rediscache` 实现提供，Lua 脚本、key 和 TTL 由这个适配层维护。可选的仓储没有配置时，按原样跳过，适配层的包装不会把缺失的依赖当成已经配置。

Anthropic 的限流响应头，由 upstream 解析成窗口观测，provider 负责维护 5 小时的会话窗口、提供商耗尽窗口和 Fable 的模型级窗口；被动采样按独立的写入顺序执行。OpenAI 图片错误的分类由 upstream 提供，provider 负责池模式、错误码策略和图片能力的冷却；图片能力窗口不会因此升级成提供商级的限流。Grok 的管理额度、账单和模型维护，通过 provider 组合的探测运行时（只有一个）执行。

平台错误通过 `provider/provider.UpstreamHealth` 接收明确的 `HealthObservation`，模型、thinking 和图片端点的意图按当前 attempt 传入。401 时凭据母提供商的处理、图片和模型的冷却、API Key 的滚动熔断和 Team 联动，分别由提供商用例维护；Team 的去重只有一份进程内的状态。app 先单独构造健康核心、恢复用例、窗口观测和 Team 实例，再交给使用方；Antigravity 的重试直接接收这些实例。

网关执行端直接持有同一个 UpstreamHealth；gateway/provider 只把当次的模型、端点和独立的执行记录交给健康模块，并按原来的范围回写凭据、Extra 或阈值状态。调度参数直接绑定 `scheduler.Parameters`，不会借健康对象读取配置。

提供商的长期状态、`schedulable`、全提供商限流、模型限流和临时不可调度规则，是不同的层次：

- 凭据或配置错误，可以记录为 recoverable error，并要求人工重新授权。
- 429、明确的 reset 时间或短期的网络和供应商故障，使用恢复时间，到期之前过滤掉提供商或模型。
- 管理员策略和代理过期，可能把提供商临时移出调度，但不会删除它。
- 提供商到期时，维护任务可以自动暂停它；重新启用之前，仍要验证凭据和关联的资源。

状态写入要带上凭据快照或版本条件。较早的请求，无法在新凭据生效后再设置旧的错误；恢复也不能清掉另一个请求刚刚确认的永久错误。

## 提供商测试与自动恢复

管理端的即时测试和 `scheduled-test-plans`，使用平台的测试服务调用实际的凭据和模型，并保存测试结果。计划和结果的值、增删改查、结果保留规则和执行器在 `provider`，SQL 在 `provider/postgres`，管理 HTTP 在 `provider/httpapi`。app 注入一个实例、时钟和 cron 技术适配；构造时不启动，每分钟一个周期，偏移十秒，每轮最多十个测试，预算五分钟。停止时，取消偏移和槽位的等待，等待在途的测试，超时时报告；停止后无法再次 Start。

即时测试、计划测试和分组的后台探测，都调用同一个 `provider.TestService.Test` 事件用例。`provider/httpapi` 负责 SSE 的 Header、提交时机和逐个事件的 Flush，后台直接汇总类型化的事件；平台执行通过受控的句柄，调用 `provider/provider` 的测试目标，不接触 Gin。写出失败时返回写入错误并取消执行，事件顺序和已经产生的部分内容保留。健康恢复由 `provider.RecoveryService`（只有一个）执行，管理员、即时测试、计划测试和窗口恢复入口都使用它。

恢复按独立的写入顺序执行，缓存删除尽力而为，没有整段的事务；状态恢复不改变人工的调度开关。每个计划可以配置自动恢复。测试成功时，可以清除符合条件的 error、rate limit、temporary unschedulable 和模型限流，但管理员禁用、提供商过期或类型不匹配的情况，仍然保持原状态。

Qoder、Gemini、Grok、Anthropic、Bedrock、OpenAI 和国产平台的测试目标，由 `provider/provider` 接收提供商记录，供应商请求和事件解析由对应的 upstream 实现：

- Qoder 共用授权会话。
- Gemini 有 API Key、AI Studio OAuth、Code Assist 和 Vertex 四条请求路径。
- Anthropic 和 Bedrock 各自保留认证和签名区域。
- Grok 区分文字和图片端点，并使用同一套健康写入规则。
- OpenAI 有 Responses、Chat、两种 Compact 和图片分支。
- 国产平台按固定协议和自适应的探测顺序测试。

HTTP 使用 EventSink 输出事件，后台消费同一组事件。`TestRun` 处理输出错误、取消、TLS 自动路由、task 恢复和终态抑制。每次自动探测请求携带自己的 UA。Antigravity 指定提供商的探测，由 AntigravityProbe 取得 OAuth 凭据、构造请求，并复用 AntigravityRetry。转发入口另外处理 Ops、粘性和请求状态。模型同步由 ModelCatalogue 使用 OAuth 查询可用模型，API Key 和历史 `upstream` 的测试及模型查询实现已移除。

OpenAI 客户端许可，由 provider 按提供商策略判断，字符串识别复用平台的实现；自动探针和网关共用 `provider/provider.OpenAIProbePolicy` 的 UA 优先级和浏览器回退，动态设置在每次判断时读取。429 报文的解析由 `upstream/openai` 执行，窗口恢复时间和观测到的套餐由 provider 写入；影子不持有套餐凭据，窗口缺少重置信号时，不写入状态。

app 直接构造一个 TestService 和 TestTargets。计划测试和分组探测使用同一个入口；管理测试的 Qoder 会话有自己独立的范围，周期测试停止后，由 ProviderTestQoderSessions hook 取消并等待会话的构建。Antigravity 的探针和转发共用平台重试和 credits 状态，探针的参数单独传入。

测试本身使用受控的超时、代理和 TLS 路由、脱敏的日志。一个模型测试成功，只说明这条路径当时可用，不代表所有的 endpoint capability 和媒体资格都可用。失败的结果要区分认证、模型、配额、代理、TLS 和上游容量，否则自动恢复可能来回启停。

### 测试参数

Kimi、Zhipu、DeepSeek 的连接测试，只测试提供商 `upstream_protocols` 里启用的原生端点；集合为空时，直接报告没有启用的协议，不发起上游请求。管理端可以用 `protocol=chat_completions|anthropic|responses` 只测其中一个已经启用的协议，选了没启用的协议，在请求上游之前就会失败；省略时，按 Chat、Messages、Responses 的顺序，依次验证所有已启用的协议。测试复用提供商的自定义 Base URL、代理、TLS 指纹和受保护的 Header Override。Anthropic 协议的自定义中继，在模型同步等 OpenAI 格式的请求里，只去掉末尾的 `/anthropic`，host 和之前的路径前缀都保持不变。

管理端的连接测试请求，要明确选择 `test_type=text|image`，并在同一个请求里传自定义的 `prompt`。文字测试不会因为模型名里有图片标记就换端点；图片测试由 OpenAI、Gemini 或 Grok 提供商的平台图片端点执行，和模型名无关。OpenAI 的 `compact` 和 `legacy_compact` 只执行固定载荷的连接测试，不显示也不使用自定义提示词。只有没带 `test_type` 的旧调用，才按模型名判断。图片和文字的结果，分别通过 SSE 的图片事件和内容事件返回；不支持图片端点的平台，直接返回可以诊断的错误，不会悄悄改成文字测试。OpenAI API Key 和 Grok 的图片测试，同时接受 `b64_json` 和图片链接（OpenAI 只接受 `https` 或 `data:image/` 开头的链接）；上游返回了结果、但没有可以展示的图片时，测试判为失败，并附上截断的响应正文。

## 额度与能力探测

Gemini tier 的静态默认值和动态设置，由 `provider.GeminiQuotaService`（只有一个）合并并缓存，每个请求拿到一份私有的副本。`GeminiPrecheck` 通过只读的统计接口执行本地预检，单独保留一分钟的日统计缓存，分钟级的数据每次查询；app 注入洛杉矶时间的日界，展示用的固定 24 小时窗口和它不同。

各平台可以维护自己的上游额度快照：OpenAI 和 Codex 的窗口、Gemini 的 tier 和模型额度、Antigravity 的 credits、Grok 的计费和媒体资格、Qoder 的 Credits，以及 Kimi、Zhipu、DeepSeek 的统一用量监控快照等。这些快照用于调度、容量展示和诊断，和 TokenRouter 的用户余额、订阅账本无关。

OpenAI 的额度和重置，由 `provider.OpenAIQuotaService` 统一编排，app 直接绑定提供商的读取、存储和共享的 task 协调器。`provider/provider.OpenAIQuotaFactory` 组合代理、TLS Router、凭据和 Agent Identity 的 Header，供应商请求和解析由 upstream 和 protocol 执行。重置次数的查询，把带到期时间的完整结果保存为提供商的展示快照；上游只返回了正数的次数、却没有到期明细时，实时结果照常返回给调用方，旧快照保持不变。

直接调用重置 API、成功消费了次数之后，服务先在一个和客户端取消脱钩的有限上下文里，恢复提供商的 error、限流和临时不可调度状态，再回读额度快照和最新的提供商数据；恢复不修改人工的 `schedulable` 开关。后续步骤部分失败时，响应用 `cache_refreshed`、`provider_state_recovered` 和 `warning_code` 明确区分；已经消费的次数，调用方不能当作可以重试的失败。

Codex 的邀请资格、规则和 credit 查询的汇总，由 `provider.CodexInviteResetService` 执行；资格接口失败时，只关闭邀请入口，已有的次数照常按顺序查询。提供商 provider 在准备时读取共享的 token、代理和专用的 TLS 和 UA，`upstream/openai.CodexInviteClient` 负责 HTTP 报文和关闭响应体。管理员路由直接绑定这个用例。

OpenAI API Key 不会自动探测 Responses 能力；创建、编辑、批量更新和复制，都以管理员选择的上游协议为准，历史的探测字段已经清除，不参与调度。两种压缩也由各自的管理员开关决定，手动连接测试不更新能力配置，但额度观测、401 认证错误的记录和 429 限流的处理照常进行。提供商写入时统一清理历史探测状态，并将兼容的压缩开关值规范为开启或关闭。API Key 的文字测试可以明确选择 Responses 或 Chat Completions，OAuth 使用 Codex Responses。HTTP continuation 是独立的开关，没有配置时关闭。

调度数据要保留原生协议集合、认证方式、两种压缩开关和 continuation 设置，配置变化时按提供商数据的失效机制传播。国产供应商不会异步写回旧的 OpenAI 文本路由镜像。Grok 的计费和媒体资格、Ollama Cloud 和各平台的额度探测，各自是独立的流程。

通用的上游声明倍率探测已经移除，没有定时任务、手动操作、快照或公开的账单自省接口。提供商的创建、编辑、批量更新、复制、CRS 同步和仓储写入，都会丢弃历史上的 `upstream_billing_probe` 和 `upstream_billing_probe_enabled` 键；这项清理不影响 Ollama Cloud 的会话和用量、endpoint capability 和其他的额度状态。

实时探测失败时，保留最近一次成功的快照，并同时显示当前的错误，旧数据不会被标成实时的。配额用完或 capability 变化时，都要让相关的调度数据失效。

API Key 提供商的上游用量查询，只用于管理员展示，和调度、自动暂停、倍率、本地配额和结算都无关；国产供应商可选的周期监控会写快照并临时停调。适配器、缓存和监控的规则见 [API Key 上游用量查询](../interfaces/upstream_usage.md)。

## OAuth 用量查询

管理端提供商统计弹窗将日期、模型、入站端点和上游端点放在同一次 `GROUPING SETS` 查询中，平均耗时按非空耗时记录数计算。查询在专用只读事务中设置 `SET LOCAL jit = off`，事务结束后设置自动恢复，减少行数估算偏高时的即时编译开销。提供商成本、用户扣费和标准费用分别返回。该报表的提供商维度使用原始用量记录。

OAuth 用量入口、Anthropic 的主动和被动窗口、并发 6 的批量查询和生命周期，由 `provider.OAuthUsageService` 负责，app 直接绑定存储、平台查询和缓存（只有一个）。`OAuthUsageCache` 保存 Anthropic、Antigravity、Qoder、窗口统计，以及 OpenAI 和 Grok 探测的命名空间，每个命名空间有自己的 key、TTL、负缓存和 singleflight。缓存和 flight 返回请求私有的展示副本，修改嵌套的值或倒计时，不会影响之后的请求。

Antigravity 和 Qoder 的共享抓取、降级缓存和倒计时，Gemini 的本地模型统计和固定 24 小时的展示窗口，Grok 计费快照的新鲜度和统计组合，OpenAI 主提供商和影子的查询选择和节流，都由核心编排。Codex 和 Anthropic 查询的技术参数和错误转换在 `provider/provider`，报文和 Header 的解析只在 upstream 实现。Grok 的管理探测直接绑定 `provider.GrokQuotaService` 和同一个 `ProbeRuntime`，账单、额度和模型目录的请求由 `provider/provider.GrokQuotaTransport` 执行，停止 hook 直接等待这些持有者。Gemini、Antigravity、Grok 的额度展示，直接使用 provider 的策略实例。

本地展示统计通过 `app/provider_usage_statistics.go` 交给 `LocalUsageStatistics`。今日统计优先批量读取，失败时以并发 8 逐个回退。窗口统计按各平台的规则使用缓存或读取原始记录，用量 SQL 和详细报告由 usage 提供。查询成功后的错误恢复，只清理观察到的同一凭据、代理、状态和原错误，PostgreSQL 单条条件更新之后，才尽力发布 outbox；迟到的恢复不会撤销管理员新设置的禁用或错误。批量查询里缺失的提供商和查询失败，共用同一把结果写入锁。

OpenAI OAuth 的本地 5 小时和 7 天统计由一条 SQL 同时计算，扫描范围从两个窗口起点中的较早者开始，再分别过滤和求和。起点使用供应商的有效重置时间，缺失或过期时使用当前时间减去窗口长度；未来时间记录继续参与统计。结果读取当前原始用量，两个窗口各自持有结果副本。读取器不支持合并或合并查询失败时，分别读取窗口并保留成功的结果；请求取消后停止回退。

用量核心的停止登记，覆盖外层的请求、Antigravity 和 Qoder 的独立查询，以及 OpenAI 异步的快照写回。共享的抓取有自己独立于调用方的取消策略；应用停止时取消并等待它完成，超时报告未完成，重复调用 Stop 返回第一次的结果。Grok 的管理探测和每六小时一次的模型目录同步，共用 `provider.ProbeRuntime`，app 在关闭数据库之前等待它；同 key 的请求会合并，预算分别是 25 秒和 15 秒，按需执行。模型任务复制提供商记录，探测返回复制过的嵌套额度和 Header。调度的免费额度统计，由提供商准入能力和 usage 的统计接口协作完成，异步执行纳入后台的完成屏障，使用独立的查询 context。

Qoder 和 OpenAI 的查询写回，比较这一轮的平台、提供商类型、状态、凭据、代理和影子归属；管理员换了身份后，旧结果不会覆盖新的那一行。Qoder 清除限流时，还比较原来的限流和 overload 窗口，快照和健康写入各自按自己的顺序提交和通知。Qoder、Antigravity 和 Anthropic 的内存缓存和共享返回，带有进程内的来源标识：换了身份就不复用旧结果，负缓存也不会跨身份传播；key、TTL 和持久化格式不变。主动查询回写 Anthropic 被动 Extra 时，比较查询时的身份，窗口列另外比较旧的结束时间；两步各自提交，失败时尽力而为。

观测 Extra 只同步单个提供商的快照，窗口列尽力发布 outbox。普通网关请求里，供应商的 Header 和错误由 upstream 解析，`provider/provider` 接收观测并调用提供商存储；scheduler 负责评分和选择，gateway 负责请求的时序。刷新和管理查询按各自的身份快照做条件写入，各平台的请求使用的竞争处理方式不一定相同。

Ollama Cloud 的共享浏览器会话、按 API Key 身份分组、手动刷新、周期资格、singleflight、成功和失败的快照、重试调度，只由 `provider.OllamaCloudUsageService` 负责，app 直接绑定 ProviderStore、加密器和动态设置接口。设置 JSON 的校验和到期规则也在 provider。Cookie 名和值的检查、允许的集合由 egress 负责，固定 URL 的请求、重定向阻断和 HTML 的供应商解析，由 `upstream/ollama.FetchUsage` 返回技术观测，`provider/provider` 提供共享的 HTTP 池、Cookie 和取消上下文。它的运行持有者同时跟踪：启动后立即执行的第一轮、每分钟一次的扫描和管理员的手动查询；停止时，取消排队、周期锁的等待和在途的操作，并在 app 剩余的预算内等待。未完成时报告超时，重复调用 Stop 不会覆盖第一次的结果；调用方或停机取消之后，迟到的响应不再写快照。Redis 租约竞争时跳过、故障时回退到数据库、没有后端时直接执行，这些策略通过同一个接口复用。

用量原生包不决定健康和调度，见[原生查询与提供商编排](../interfaces/upstream_usage.md#native_usage_adapters)。

## 运维诊断

- 按每个 provider 观察候选数、刷新的成功和失败、节流、超时和最长的积压，只看总成功率不够。
- 把提供商测试、刷新、quota probe、代理健康和调度过滤原因关联起来，区分凭据故障和出站网络故障。
- 检查提供商的数据库状态、当前进程的数据和跨实例的失效是否一致；手工改了数据库之后，要等周期重建，才会生效。
- 自动恢复或批量导入之后，抽查实际的协议；token endpoint 成功，不代表推理一定可用。
- 排查 OpenAI Chat 时，同时核对入站协议、`upstream_protocols`、分组的 `protocol_fallbacks` 和 Usage Log 的 `upstream_endpoint`；默认模式应该记录 `/v1/chat/completions`，历史的探测状态不会改变上游协议。

相关文档：[上游提供商能力矩阵](../interfaces/upstream_provider_matrix.md)、[提供商调度与缓存一致性](../architecture/provider_scheduling_and_cache.md)、[上游传输安全](upstream_transport_security.md)。
