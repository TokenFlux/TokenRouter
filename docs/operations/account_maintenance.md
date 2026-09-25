# 账号维护

本文描述上游账号凭据刷新、健康测试、配额与能力探测、临时不可调度和自动恢复的后台流程。它不定义创建表单字段或请求内调度评分；这些由平台专题与调度架构拥有。

## 章节导航

- [凭据刷新](#凭据刷新)：修改 refresh 候选、并发、重试或状态同步时读取。
- [状态与临时不可调度](#状态与临时不可调度)：修改错误、限流或恢复时间时读取。
- [账号测试与自动恢复](#账号测试与自动恢复)：修改定时测试或恢复条件时读取。
- [额度与能力探测](#额度与能力探测)：修改上游 usage、quota 或 endpoint capability 时读取。
- [运维诊断](#运维诊断)：排查刷新堆积、误封禁或账号抖动时读取。

<a id="account_credential_refresh"></a>
## 凭据刷新

`account.BackgroundRefreshService` 分页读取需要维护的账号，按平台 refresher 判断资格，并对每个 provider 应用独立并发/QPS 门槛、单次 attempt 超时、周期总超时和有界退避。OAuth、Setup Token 和 Qoder COSY 的候选规则不同；API Key、Bedrock 和 Service Account 通常由各自请求路径或签名 provider 管理，不应统一假设有 refresh token。

刷新成功后要原子更新凭据/过期时间，清理可恢复错误，并同步账号缓存与调度快照。OpenAI/Antigravity 还可在刷新后确保 privacy 状态。刷新失败按失败阈值记录，不立即把一次瞬时网络错误等同于永久禁用；凭据明确撤销或账号归属失效时才进入需要重新授权的状态。

非 Grok 的统一刷新成功路径已通过 `account.CredentialRefreshWriter` 接入 `account/postgres` 的条件写入：在同一 Ent 事务里按账号 ID、平台、类型、状态、完整凭据和代理比较并锁定行，再执行原凭据清理及 outbox 写入。管理员已更新或禁用账号时，本轮结果不再覆盖当前状态；调用方重新读取，且不为这次冲突再次交换 token。Grok 继续使用原有 CAS、持久化后回读和 provider containment 错误分类。

统一锁、重读、交换前快照、版本写入、条件持久化和竞争恢复已进入 `account.OAuthRefreshAPI`，由 app 持有唯一实例。请求与后台直接使用原生协调器及平台刷新器。后台刷新及 Grok 管理对账直接绑定原生 `BackgroundRefreshService`，六个平台执行器与后置端口由 app 显式装配；供应商错误分类在 account/provider，实际交换在 upstream。分页周期、恢复游标、平台并发/QPS、每轮失败隔离、尝试预算、重试与成功后置顺序由同一实例的账号组件执行，相关测试直接验证账号组件。

app 直接构造、绑定并登记唯一 `BackgroundRefreshService`，其 `RefreshLoop` 保留立即首轮和周期下限。分页元数据、严格 ID 顺序、未完成页不推进游标和末页归零由 `RefreshCandidateScan` 管理；按平台分组和 worker 统计由 `RefreshPageProcessor` 执行。周期与 Grok 管理对账共用同一组平台准入实例，每轮连续失败状态彼此独立。停止同时取消周期、扫描与按需对账，等待实际工作并固定首次结果；超时不等于排空，随后由 app 决定是否可以关闭共享依赖。

非 Grok 后台失败通过 `RefreshFailureWriter` 比较交换身份后写入健康；旧失败不根据调用参数覆盖管理员的新凭据/代理/状态。相同身份已有更长 cooldown 时不缩短；outbox 失败仍保留原尽力语义。内存快速阻断针对刷新失败区分凭据身份，账号级配额/容量阻断保持原范围。发布前复核显式清理代次，迟到通知不能重装已清理的阻断；多份在途凭据的期限彼此独立。Antigravity 强制刷新标记在原 Extra/outbox 事务中复核身份后清理；永久失败只有健康条件写入成功后才清理该身份的标记。

账号 CRUD/筛选、凭据与健康状态写入、CN/Ollama 持久化快照由 `account/postgres.AccountStore` 唯一实现。调度查询与 outbox 由 scheduler 及其存储适配提供，app 注入调度事件发布端口。账号消费累计与额度重置 SQL 已进入 `billing/postgres.AccountUsageParticipant`，事务内操作不提交或发布事件；兼容入口保留原提交后顺序。

刷新成功清除临时停调时，存储还比较本次凭据/代理/状态与原 cooldown 的期限及原因；身份或窗口已变化则不清 Redis 状态，不发布旧账号快照，也不继续维护旧身份。成功清理会先更新本次健康投影再发布缓存，避免数据库已恢复而调度缓存仍停调。AG 缺失 project_id 的恢复仍只在原触发条件下执行。手动刷新保留清错、全账号限流、AG scope、模型限流、临时停调的五次独立提交，每步比较交换身份和该步骤原状态；冲突即停止，失败前已提交的步骤保留。运行时阻断清理还校验原代次，不清除后来安装的阻断。

管理员单账号/批量刷新也使用同一协调器，保持原平台锁 key，锁内重读后再执行显式刷新。凭据通过原管理校验与配置事务保存，并在行锁内比较交换身份；冲突只返回最新账号，不再次交换或继续旧成功结果的后置动作。该比较字段只用于内部 Go 调用，不接受 HTTP JSON 输入；单条/批量管理刷新和重新授权已由 `ManagedRefreshService` 统一编排，HTTP 只处理绑定、状态码和展示。`account.ManualCredentialExchange` 组合原生授权及 Qoder 刷新端口，app 直接投影共享传输。

统一刷新协调器关闭时拒绝新认领，取消锁等待与在途交换，再在给定预算内等待。已接纳的等待者取得账号锁后还要复查停止屏障，防止持锁者先响应取消并释放锁、等待者尚未收到取消时又启动交换。若交换器忽略取消，停止会报告未完成，迟到响应也不写入凭据；该行为不改变正常请求的取消来源。交换前快照深复制嵌套凭据，保留原 nil 凭据转为空对象的比较语义。

账号创建、复制/恢复、影子关系以及编辑和批量校验由 `account.Admin` 拥有；Qoder 的站点/PAT 验证由 `account/provider` 组合唯一上游实现。普通编辑、管理员编辑、影子代理传播和 CRS 配置写入显式声明本次拥有的字段，`AccountStore.UpdateConfiguration` 在原事务中锁定并读取最新记录，再合并这些字段。名称等普通修改不会写回旧凭据、error/schedulable 或消费快照；管理员未提供的敏感凭据子键从锁内最新值继承。显式状态恢复仍更新状态与错误消息，CRS 仍可写入来源状态和调度开关。

配置替换保留 billing 消费累计及专用维护入口的最新快照，固定窗口基于锁内累计按原取时点重新计算。Ollama 身份变化仍清理受管会话，CN 身份变化仍使观测失效；原 outbox 与配置一起提交。`last_used_at`、全账号限流、过载和会话窗口只由专用运行入口维护。新增的字段意图不改变数据库、缓存或 HTTP 格式。

请求路径 token provider 仍会在使用前检查过期偏移，并用账号级锁避免并发刷新。后台刷新降低热路径延迟，但不是唯一正确性来源；两条路径必须使用相同的凭据版本/CAS 保护，避免旧请求覆盖新 token。

`account.DeferredService` 维护唯一 last-used 队列，入队与批次提取互斥；旧批次失败只补回尚无新值的账号，避免覆盖随后到达的活动时间。停止时取消新周期和等待者，等待正在写入的批次后进行最终 flush；最终写入同时受原十秒预算及应用剩余退出预算约束。账号到期扫描由 `account.ExpiryService` 执行，保留一分钟周期与立即首轮，取消和等待受生命周期 context 约束，停止后不能重新扫描。

账号文件导入导出由 `account.Archive` 拥有，HTTP 在 `account/httpapi`。备份保留管理员显式原凭据导出和影子排除，跨调用边界使用独立副本；导入保留先处理代理、后读取一次动态模板、逐项创建及原隐私副作用顺序。`account/transfer` 只拥有文件格式与省略/null 标记，模板默认内容仍由设置用例提供。ID Token 仅复用原 OpenAI 非认证解码补齐缺失提示，不作为身份验证。

Codex session 文件导入由 `account.CodexImporter` 拥有，HTTP 直接调用该用例，纯解析与身份索引在 account 内维护。access-token-only 仅按 access 摘要匹配，完整 OAuth 保留原用户/账号兼容匹配，Agent Identity 保持团队隔离及 runtime 合并。索引与导入 map 在跨调用边界复制。时钟和私钥验证显式注入，导入 JWT 解码仍仅补齐提示，不承担认证；OAuth 批量创建共用相同的合并、保护字段和摘要规则，供应商交换由 upstream 执行。

CRS 同步与预览由 `account.CRSSync` 拥有六类来源规则，`account/provider` 负责原登录/导出 HTTP，代理身份匹配由 egress 负责。普通部分成功、nil/显式空选择、影子限制和来源字段清理保持原语义。`account.CRSAuthorization` 直接组合 Claude/OpenAI/Gemini 原生授权，导入后的尽力刷新共享生产协调器和条件写入：锁内确认来源身份，管理员修改优先，取消后不写回；导入成功计数不依赖刷新成功。该显式导入路径保留原状态资格和令牌版本字段，不套用后台 active/过期筛选。

Grok 导入后的主动探测由 app 注入唯一 `account.GrokImportProbeScheduler`。它按需启动，保留三 worker、64 个排队项和按账号去重；每项真正执行时才开始 25 秒预算。输入为无凭据的 AccountSnapshot，供应商返回最小日志观测。停机取消未领取的尽力项，取消并等待在途；超时报告未完成，不把取消当作已探测。账户创建和已完成导入不会因异步探测失败回滚。

批量创建、删除、刷新和清错由 `ManagementBatch` 执行。删除保持母/影子依赖排序与五并发，其余批量入口保留原部分成功、重复 ID、十并发和错误顺序。批量凭据字段更新先验证全部对象，再逐账号提交字段补丁；配置事务在行锁内合并最新凭据，不能把校验时的旧 token 或未选配置写回。补丁参数不接受 HTTP JSON 输入，不新增数据库字段。

隐私设置的实际请求由唯一 `PrivacyService` 登记生命周期，停止取消并等待在途，忽略取消的迟到响应也不再写入。批量后台入口仍保留原任务完成屏障和任务名称；隐私服务停止后不会继续发起下一个请求。隐私写回在原 Extra/outbox 事务中比较查询时的凭据、代理、状态和影子归属；身份变化返回无可应用结果，不覆盖新身份，也不更新旧输入的成功模式。缺少条件写入能力的兼容构造不会退回无条件覆盖。普通存储失败仍保留原尽力日志与返回语义。

隐私传输参数由 `account/provider.PrivacyOptions` 统一组合，app 直接构造原生用例；OpenAI 账号与订阅查询复用同一平台客户端，Antigravity 使用原授权隐私请求。Grok、OpenAI、Claude、Gemini 和 Antigravity 请求侧 token source 由 app 直接绑定共享缓存与刷新协调器，OpenAI 指标和 Antigravity 统计各自只有一份；AlphaSearch 的 PAT 元数据补齐独立绑定同一授权实例。

账号列表及运行状态读取由 `ManagementList`、`RuntimeStatusReader` 执行，HTTP `RuntimePresenter` 只转换管理 DTO 和母账号字段。分页和服务端排序先于观察，评分仅在显式请求时查询筛选池及分组池，并对候选并集批查负载。实际评分、并发、会话和 RPM 由 scheduler 提供，详细用量由 usage 提供，各自维护缓存与运行状态。OpenAI 自动暂停纯规则属于 account，每个原读取点投影动态默认阈值，保留窗口豁免、两小时陈旧边界和缺失时间戳语义。

Google One 单条/批量 tier 刷新由 `TierManagement` 拥有资格、查询集合、十并发和条件配置写入；Drive 网络调用与供应商 tier 推断由 account/provider 与 upstream/gemini/codeassist 协作提供。空/损坏批量输入仍回落原最多一万条 Google One 查询，逐项失败不取消其它项。查询开始时冻结身份，保存时在原配置行锁内复核，并只合并 tier_id 与本轮 Drive 字段；管理员的新 token 和未选 Extra 不被旧快照覆盖，outbox 失败仍回滚本次配置。身份复核使用已有字段，不提供跨实例协调。

管理端可用模型由 `routing.AdminCatalog` 组合账号显式模型和平台默认目录；`routing/provider` 在每次调用时投影 upstream 的同一目录，保留 Google One、Qoder 站点及各平台 JSON 形状，不建立另一份缓存。

实时模型同步请求由 `ModelSyncService` 跟踪在途并在停止时取消、等待；构造不请求供应商。临时凭据预览保持原无持久化流程，错误契约由 account 唯一拥有；`account/provider.ModelCatalogue` 负责实际 endpoint、Header、响应报文解析及读取上限，app 直接绑定原生账号存储和凭据来源。模型预览与账号测试共享一个 ProbeTasks 作用域，持久账号仍由同一 task 协调器串行处理。

账号管理 HTTP 路由绑定 account/httpapi 的具体处理器，测试直接验证所属模块。高级调度诊断通过只读安全投影调用 scheduler 的同一评分算法，app 保留同一反馈实例及 gateway 绑定顺序。

执行入口、管理接口和调度快照共用原生账号规则，但不共用公开数据形状。执行目标由原生 Record 和请求路线组成，原有复制边界保留自引用关联与 map 隔离。账号与 billing 的 SQL 实例仍只在 app 构造一次，配置更新、CAS 与消费累计各由原存储负责。

## 状态与临时不可调度

`account.HealthService` 拥有通用错误规则匹配、显式错误/池模式优先级、认证失败/过载状态转换、403 累计冷却、CN 可恢复冷却、429 默认回避、流超时计数阈值及账号/模型额度阈值判断；供应商报文分类与模型规范化由执行 Adapter 提供。原持久化、缓存和调度反馈顺序保持，长期状态与临时窗口不互相替代。临时停调、403 和超时计数由 app 构造的 `account/rediscache` 实现提供，Lua 与 key/TTL 由该适配维护。可选仓储缺省时保留原跳过行为，适配包装不把缺失依赖当成已配置后端。

Anthropic 的限流响应头由 upstream 解析为窗口观测，account 负责维持 5h 会话、账号耗尽窗口和 Fable 模型级窗口；被动采样仍按原独立写入顺序执行。OpenAI 图片错误分类由 upstream 提供，account 负责池模式、错误码策略和图片能力冷却；图片能力窗口不会因此升级为账号级限流。Grok 管理额度、账单与模型维护通过原生 provider 组合唯一探测运行时。

平台错误通过 `account/provider.UpstreamHealth` 接收显式 `HealthObservation`，模型、thinking 与图片端点意图按当前 attempt 投影。401 凭据母账号处理、图片/模型冷却、API Key 滚动熔断和 Team 联动分别由账号用例维护；Team 去重仅有一份进程内状态。app 先独立构造同一健康核心、恢复用例、窗口观测与 Team 实例，再向原生消费者发布；Antigravity 重试直接接收这些实例。

网关执行端直接持有同一 UpstreamHealth；gateway/provider 仅把当次模型、端点和独立执行记录交给原生健康能力，并按原范围回写凭据、Extra 或阈值状态。调度参数直接绑定 scheduler.Parameters，不借健康对象读取配置。

账号长期状态、`schedulable`、全账号限流、模型限流和临时不可调度规则是不同层次：

- 凭据/配置错误可以记录 recoverable error 并要求人工重新授权。
- 429、明确 reset time 或短期网络/供应商故障使用恢复时间，在到期前过滤账号或模型。
- 管理员策略和代理过期可能临时移出调度，但不删除账号。
- 账号到期可由维护任务自动暂停；重新启用前仍需验证凭据和关联资源。

状态写入要携带凭据快照或版本条件。较早请求不能在新凭据生效后再次设置旧错误；恢复同样不能清除另一个请求刚确认的永久错误。

## 账号测试与自动恢复

管理端即时测试和 `scheduled-test-plans` 使用平台测试服务调用真实凭据/模型，并保存测试结果。计划/结果值、CRUD、结果保留规则及执行器已进入 `account`，SQL 位于 `account/postgres`，管理 HTTP 位于 `account/httpapi`。app 注入唯一实例、时钟和 cron 技术适配；构造不启动，保留分钟周期、十秒偏移、每轮最多十个测试与原五分钟预算。停止会取消偏移和槽位等待，等待在途并报告超时；停止后不能再次 Start。

即时测试、计划测试及分组后台探测调用同一 `account.TestService.Test` 事件用例。`account/httpapi` 负责原 SSE Header、提交时机与逐事件 Flush，后台直接聚合类型化事件；平台执行通过受控句柄调用 account/provider 的测试目标，不接收 Gin。写出失败会返回原写入错误并取消执行，事件顺序及部分内容保留。健康恢复由唯一 `account.RecoveryService` 执行，管理员、即时测试、计划测试和窗口恢复入口共同使用。

原独立写入顺序与尽力缓存删除保留，不增加整段事务；状态恢复不改变人工调度开关。每个计划可配置自动恢复。成功测试可以清除符合条件的 error、rate limit、temporary unschedulable 和模型限流，但不能绕过管理员禁用、账号过期或类型不匹配。

Qoder、Gemini、Grok、Anthropic、Bedrock、OpenAI 与国产平台的测试目标由 `account/provider` 接收原生账号记录，供应商请求和事件解析由对应 upstream 实现。Qoder 继续共享授权会话，Gemini 保留 API Key、AI Studio OAuth、Code Assist 与 Vertex 四条请求路径；Anthropic 与 Bedrock 保留各自认证和签名区域；Grok 继续区分文字与图片端点并复用原健康写入规则。

OpenAI 保留 Responses、Chat、两种 Compact 与图片分支，国产平台保留固定协议和自适应探测顺序。HTTP 使用相同 EventSink，后台消费同一组事件。单次 `TestRun` 显式持有输出错误、取消、TLS 自动路由、task 恢复和终态抑制；自动探针 UA 只属于当前执行，不修改共享适配器。Antigravity 指定账号探测由原生 AntigravityProbe 取得凭据、构造请求并复用 AntigravityRetry；转发入口另行投影 Ops、粘性及请求状态。模型同步由原生 ModelCatalogue 执行。

OpenAI 客户端许可由 account 按账号策略裁决，字符串识别复用平台实现；自动探针与网关共用 `account/provider.OpenAIProbePolicy` 的 UA 优先级和浏览器回退，动态设置仍按原时机读取。429 报文解析由 upstream/openai 执行，窗口恢复时间和观测套餐写入由 account 拥有；影子不持有套餐凭据，窗口缺少重置信号时不新增状态写入。

app 直接构造唯一 TestService 和 TestTargets。计划测试及分组探测共用同一个入口；管理测试的 Qoder 会话保留原独立作用域，在周期测试停止后由 AccountTestQoderSessions hook 取消并等待会话构建。Antigravity 探针与转发共享平台重试和 credits 状态，探针参数独立传入。

测试本身应使用受控超时、代理/TLS 路由和脱敏日志。一个模型测试成功只证明该路径当时可用，不证明所有 endpoint capability 或媒体资格。失败结果需区分认证、模型、配额、代理、TLS 和上游容量，以免自动恢复形成启停抖动。

Kimi、Zhipu、DeepSeek 的连接测试仅测试账号 `upstream_protocols` 中启用的原生端点；空集合直接报告未启用协议，不发起上游请求。测试复用账号自定义 Base URL、代理、TLS 指纹和受保护 Header Override；Anthropic 协议的自定义中继在模型同步等 OpenAI 格式请求中只移除末尾 `/anthropic`，不能改回官方 host 或丢弃此前的路径前缀。

管理端连接测试请求必须显式选择 `test_type=text|image` 并传入同一字段的自定义 `prompt`。文字测试不再因为模型名称包含图片标记而切换端点；图片测试也不再依赖模型名称命中规则，而是由 OpenAI、Gemini 或 Grok 账号的平台图片端点执行。OpenAI 的 `compact` 与 `legacy_compact` 仅执行固定载荷的连接测试，不显示或使用自定义提示词。未携带 `test_type` 的历史调用才允许回退到旧模型名判断。图片和文字的结果分别通过 SSE 图片事件和内容事件返回；不支持图片端点的平台应直接返回可诊断的错误，不得静默改成文字测试。

## 额度与能力探测

Gemini tier 的静态默认和动态设置由唯一 `account.GeminiQuotaService` 合并并缓存，返回请求私有副本。`GeminiPrecheck` 通过只读统计端口执行原本地预检，独立保留一分钟的日统计缓存和逐次分钟查询；app 注入洛杉矶日界，展示用固定 24 小时窗口保持原差异。

平台可维护独立的上游额度快照：OpenAI/Codex 窗口、Gemini tier/model quota、Antigravity credits、Grok 计费/媒体资格、Qoder Credits，以及 Kimi/Zhipu/DeepSeek 的统一用量监控快照等。快照用于调度、容量展示和诊断，不是 TokenRouter 用户余额或订阅账本。

OpenAI 额度与重置由 `account.OpenAIQuotaService` 统一编排，app 直接绑定原生账号读取、存储和共享 task 协调器。`account/provider.OpenAIQuotaFactory` 组合代理、TLS Router、凭据与 Agent Identity Header，供应商请求和解析由 upstream/protocol 执行。重置次数查询把带到期时间的完整结果保存为账号展示快照；上游只返回正数次数却缺少到期明细时，实时结果仍返回给调用方，但旧快照必须保留。

直接调用重置 API 成功消费次数后，服务先在脱离客户端取消信号的有界上下文中恢复账号 error、限流和临时不可调度状态，再回读额度快照与最新账号投影；恢复不修改人工 `schedulable` 开关。后续步骤部分失败时响应使用 `cache_refreshed`、`account_state_recovered` 和 `warning_code` 明确区分，调用方不得把已消费的次数当作可重试失败。

Codex 邀请资格、规则与 credit 查询汇总由 `account.CodexInviteResetService` 执行；资格接口失败只关闭邀请入口，仍按原顺序查询已有次数。账号 provider 在原准备时点读取共享 token、代理和专用 TLS/UA，`upstream/openai.CodexInviteClient` 拥有 HTTP 报文及响应体关闭。管理员路由直接绑定原生用例。

OpenAI API Key 不再自动探测 Responses 能力；创建、编辑、批量更新和复制均以管理员选择的上游协议为准，历史探测字段被清理且不参与调度。两类压缩也由独立管理员开关决定，手动连接测试不更新能力配置，但仍保留额度观测、401 认证错误记录与 429 限流处理。账号与 OAuth 导入模板共用历史输入清理边界，模板读取也不返回旧探测状态或自动模式。API Key 文字测试可显式选择 Responses 或 Chat Completions，OAuth 仍使用 Codex Responses。HTTP continuation 为独立开关，缺失时关闭。

调度投影必须保留原生集合、认证方式、两类压缩开关与 continuation 设置，配置变化沿用账号投影失效机制。国产供应商不再异步写回旧 OpenAI 文本路由镜像。Grok 计费/媒体资格、Ollama Cloud 与各平台额度探测保持各自独立流程。

通用的上游声明倍率探测已移除，不再有定时任务、手动操作、快照或公开账单自省接口。账号创建、编辑、批量更新、复制、CRS 同步和仓储写入都会丢弃历史 `upstream_billing_probe` 与 `upstream_billing_probe_enabled` 键；这项清理不得影响 Ollama Cloud 会话/用量、endpoint capability 或其它额度状态。

实时探测失败时保留最近成功快照并同时暴露当前错误，不把旧数据标为实时。任何配额耗尽或 capability 变化都要触发相关调度投影失效。

## API Key 上游用量查询

API Key 上游用量由独立的 `UpstreamUsageService` 提供，和 OAuth/Setup Token 的 `OAuthUsageService` 语义分离。它只服务管理员展示，不参与调度、自动暂停、倍率、本地配额或结算；列表加载、滚动和自动刷新都不会产生上游流量。管理员手动查询时，服务按账号和规范化配置指纹合并并发请求，单次约 60 秒超时、512 KiB 响应体上限、禁止重定向，并复用代理、TLS 指纹、Header Override 和既有 `HTTPUpstream`。

配置缺失时，普通 API Key 默认启用 Sub2API；New API 和 Zivv 必须显式选择对应适配器。CN API Key 则按平台/mode 自动选择 Kimi 余额、Kimi Coding、Zhipu Coding 或 DeepSeek 多币种余额适配器，Zhipu payg 明确不支持。Sub2API 的钱包负余额可以展示，`remaining=-1` 只在适配器内部转换为 `unlimited=true`。New API 用 `/api/usage/token/` 读取 Key 配额，再通过固定的钱包端点或受保护的用户访问令牌查询用户钱包；只有钱包结果进入 `balance`，Token 配额进入 `limits`/`subscription`。

Zivv 用 `/v1/user/balance` 同时读取钱包、累计用量、Key 限额和套餐，`key_limit=0` 显示为不限量。`unlimited_quota=true` 时忽略上游可能溢出的额度字段，状态接口失败时使用默认单位比例；官方实例未配置用户访问令牌且不提供 API Key 钱包端点时返回 `UPSTREAM_USAGE_WALLET_UNAVAILABLE`，不把 Token quota 当钱包余额。手动查询失败不修改账号状态或运行快照，也不自动回退到另一个协议。

浏览器只缓存成功的归一化结果五分钟，缓存键隔离管理员身份、账号 `updated_at`、代理/Base URL 和配置；失败不缓存，账号凭据、代理或配置变化立即失效。审计仅记录管理员动作和脱敏元数据，不记录 API Key 或上游原始响应。该功能与已移除的 `upstream_billing_probe` 完全不同，不恢复旧的自动倍率探测。

CN 周期监控默认关闭；启用后只把统一快照写入 `extra.cn_usage_monitor_snapshot`，用身份 hash 和账号 `updated_at` CAS 防止旧探测覆盖新凭据。失败保留最近成功结果，余额低于阈值只写带同一身份 hash 的临时不可调度原因，恢复也只清理由该身份创建的状态。多实例同轮由 leader lock 串行化，自定义中继必须命中启用的 URL allowlist。详细字段、适配器和超时语义见[API Key 上游用量查询](../interfaces/upstream_usage.md)。

OAuth 用量入口、Anthropic 主/被动窗口、六并发批量查询和生命周期由 `account.OAuthUsageService` 拥有，app 直接绑定原生存储、平台查询及唯一缓存。`OAuthUsageCache` 保存原 Anthropic、Antigravity、Qoder、窗口统计及 OpenAI/Grok 探测命名空间；原 key、TTL、负缓存和各自 singleflight 保持。缓存与 flight 返回请求私有展示副本，不能通过改写嵌套值或倒计时污染后续请求。

Antigravity/Qoder 的共享抓取、降级缓存和倒计时、Gemini 本地模型统计及固定 24 小时展示窗口、Grok 计费快照的新鲜度与统计组合、OpenAI 主/影子查询选择和节流均由核心编排。Codex/Anthropic 查询技术参数与错误投影进入 account/provider，报文和 Header 解析由 upstream 唯一实现；Grok 管理探测直接绑定 `account.GrokQuotaService` 与同一个 `ProbeRuntime`，账单、额度及模型目录请求由 `account/provider.GrokQuotaTransport` 执行；原停止 hook 直接等待原生拥有者。Gemini、Antigravity、Grok 额度展示直接使用 account 的策略实例。

本地展示统计由 `LocalUsageStatistics` 通过 app/account_usage_statistics.go 的五字段只读投影取得，保留缓存未命中才查询、批量优先及八并发回退。实际用量 SQL 和详细报告由 usage 提供。成功查询的错误恢复只清理观察到的同一凭据/代理/状态及原错误，PostgreSQL 单条条件更新后才发布原尽力 outbox；不会借迟到恢复撤销管理员的新禁用或错误。批量缺失账号和查询失败共享同一结果写入锁。

用量核心的停止登记覆盖外层请求、Antigravity/Qoder 独立查询和 OpenAI 的异步快照写回。共享抓取保留独立于调用方的取消策略；应用停止取消并等待其完成，超时报告未完成，重复 Stop 固定首次结果。Grok 管理探测及六小时模型目录同步共用 `account.ProbeRuntime`，app 在关闭数据库前等待；同 key 合并、25/15 秒预算及按需执行保持。模型任务复制账号记录，探测返回复制嵌套额度与 Header。调度免费额度统计由账号准入能力和 usage 统计端口协作，异步执行纳入后台完成屏障，保持独立查询 context。

Qoder 与 OpenAI 查询写回比较本轮平台、账号类型、状态、凭据、代理及影子归属；管理员换身份后旧结果不覆盖新行。Qoder 清除限流还比较原限流及 overload 窗口，保留快照与健康写入各自的提交/通知顺序。Qoder、Antigravity 与 Anthropic 内存缓存及共享返回带进程内来源标识，换身份不复用旧结果，负缓存也不能跨身份传播；key、TTL 与持久化格式不变。主动查询回写 Anthropic 被动 Extra 时比较查询身份，窗口列另外比较旧结束时间；两步保持原独立提交和尽力失败行为。

观测 Extra 只同步单账号快照，窗口列继续发布尽力 outbox。普通网关的供应商 Header 与错误由 upstream 解析，account/provider 接收观测并调用账号存储；scheduler 负责评分与选择，gateway 负责请求时序。刷新与管理查询按各自的身份快照条件写入，不能推断所有平台请求都使用相同的竞争协议。

Ollama Cloud 的共享浏览器会话、按 API Key 身份分组、手动刷新、周期资格、singleflight、成功/失败快照和重试调度由 `account.OllamaCloudUsageService` 唯一拥有，app 直接绑定 AccountStore、原加密器及动态设置端口。设置 JSON 校验和到期规则也在 account；原 settings 表 key 与赋值行为不变。Cookie 名值检查和允许集合归 egress，固定 URL 请求、重定向阻断及 HTML 供应商解析由 `upstream/ollama.FetchUsage` 返回技术观测，account/provider 投影共享 HTTP 池、Cookie 和取消上下文。

用量原生包不决定健康或调度，见 [原生查询与账号编排](../interfaces/upstream_usage.md#native_usage_adapters)。

其运行拥有者同时跟踪立即首轮、每分钟扫描和管理员手动查询；停止取消排队、周期锁等待及在途操作，并使用 app 剩余预算等待。未完成时报告超时，重复 Stop 不覆盖首次结果；调用方或停机取消后，迟到响应不再写快照。Redis 租约竞争跳过、故障回退数据库和无后端执行的既有策略通过同一端口实现复用，锁名、owner、TTL 及释放预算保持原值。

## 运维诊断

- 观察每 provider 的候选数、刷新成功/失败、节流、超时和最长积压，而不只看总成功率。
- 关联账号测试、刷新、quota probe、代理健康和调度过滤原因，区分凭据故障与出站网络故障。
- 检查账号数据库状态、当前进程投影和跨实例失效是否一致；手工改数据库后等待周期重建不等于即时生效。
- 自动恢复或批量导入后抽查实际协议，避免仅凭 token endpoint 成功误判推理可用。
- OpenAI Chat 排障应同时核对入站协议、`upstream_protocols` 与分组 `protocol_fallbacks` 和 Usage Log 的 `upstream_endpoint`；默认模式应记录 `/v1/chat/completions`，不能因历史探测状态改变上游协议。

相关文档：[上游账号能力矩阵](../interfaces/upstream_account_matrix.md)、[账号调度与缓存一致性](../architecture/account_scheduling_and_cache.md)、[上游传输安全](upstream_transport_security.md)。
