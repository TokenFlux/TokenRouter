# 账号调度与缓存一致性

本文描述网关如何构建可调度账号快照、筛选候选、维持粘性和等待队列，并说明 Redis/outbox 的失效传播与恢复边界。它不定义单个平台的凭据格式、分组定价或最终错误响应；这些分别由平台、领域和接口专题拥有。

## 章节导航

- [调度输入](#调度输入)：判断一个请求如何形成候选桶。
- [调度器模式](#调度器模式)：修改基础/高级调度选择或评分时读取。
- [快照一致性](#快照一致性)：修改缓存、outbox、重建或数据库回退时读取。
- [候选筛选与评分](#候选筛选与评分)：修改账号资格或选择顺序时读取。
- [账号自动停调阈值](#账号自动停调阈值)：修改平台用量阈值或恢复窗口时读取。
- [评分诊断](#评分诊断)：修改管理员评分解释或模拟接口时读取。
- [粘性与等待](#粘性与等待)：修改会话复用、并发等待或超时边界时读取。
- [失效与恢复](#失效与恢复)：修改账号更新后的跨实例可见性时读取。
- [诊断不变量](#诊断不变量)：排查无可用账号或陈旧选择时读取。

## 调度输入

调度不是只按 `platform` 随机选择账号。请求在进入调度前已经确定或携带分组、强制平台、客户端模型、映射后的模型、endpoint/媒体意图、协议 transport、OAuth/privacy 要求和可选 session 标识。账号优先级统一来自 `accounts.priority`；`account_groups` 只表达成员关系，不保存分组内优先级。

快照 bucket 由分组、平台和模式共同区分：

- `single`：目标平台的普通调度。
- `mixed`：Anthropic 或 Gemini 分组允许纳入显式开启 mixed scheduling 的 Antigravity 账号。
- `forced`：专用 Antigravity 路由等强制平台场景，不混入其它平台。

同一账号可能属于多个分组；每个 bucket 的资格和模型范围独立计算，但账号全局优先级在所有分组中一致。分组查询按 `accounts.priority`、`account_id` 稳定排序。

<a id="advanced_scheduler_selection"></a>
## 调度器模式

`groups.scheduler_type` 是分组级调度策略，取值只能是 `basic` 或 `advanced`，新建和历史未配置分组均为 `basic`。它不是平台能力开关：任何平台的分组都能选择高级调度器；未绑定分组的请求保持基础调度。Claude Code-only、不可用组等回退链完成后，必须以实际落到的最终分组重新读取该字段，不能沿用原分组的模式。强制平台只改变候选平台和混合模式，不清除最终分组，也不能绕过该分组的高级模式与参数覆盖。

高级分组的有效参数按字段合并：高级调度覆盖值、深复制、校验和配置合并由 `internal/scheduler/policy` 的纯叶子实现。选号和诊断直接使用其 RuntimeSettings、EffectiveSettings、FeedbackConfig 与 StickyEscapeConfig，运行反馈直接使用唯一 `scheduler.RuntimeStats`；评分与 Top-K 抽样也由 scheduler 拥有。平台直接使用原生无凭据分数类型，执行目标只在当次适配作用域保留对应关系，不再往返转换旧分数实体。`scheduler.SettingsRuntime` 负责动态设置读取、短 TTL 缓存与 singleflight，设置保存后发布到同一实例。`scheduler.Parameters` 固定参数来源并合并进程投影、运行设置和最终分组覆盖；app 将同一参数实例直接绑定各平台选择与诊断，不再临时构造 OpenAI 服务或借健康适配取得设置仓储。`GenericSelector` 与 `PlatformSelector` 拥有基础/高级选择、粘性、订阅池及 fresh/DB 复核的执行顺序；旧 service 仍保留账号和平台资格投影。app 显式绑定唯一反馈、设置和粘性统计实例，生产选择与诊断共用；运行设置及粘性观测已改为实例依赖，不再通过进程全局指针替换。

`gateway.advanced_scheduler` 提供进程默认值，`advanced_scheduler_*` 运行时设置可覆盖该默认值，最终由 Group 的 `advanced_scheduler_overrides` 覆盖。覆盖 JSON 缺失字段继续继承；显式 `false` 和 `0` 都是有效的分组值，空对象代表全部继承。两个 EWMA alpha 分别控制错误率和 TTFT 的最新样本权重，范围为 `0 < alpha <= 1`；sticky escape 的开关、TTFT 阈值和错误率阈值也按同一优先级合并。运行时反馈仍按账号共享，但请求完成时使用该请求最终分组解析出的 alpha 回写，选择和反馈不会因保存后的配置变化而错配。七项基础权重最终全为零也是有效策略，此时粘性加成仍可参与，完全并列的候选只按账号全局优先级、账号 ID 稳定决胜，再进入 Top-K 抽样；负载和等待已经属于评分信号，不能作为第二套同分比较规则。合并后的基础权重和完整权重总和必须有限，管理写入拒绝会使任一总和溢出的覆盖；请求期遇到历史异常对象时仅把权重回退到已验证的全局值，避免生成 `NaN`/`Inf`。基础分组和无分组路径忽略这份对象，避免配置残留改变基础调度语义。

基础调度器保留原有的优先级、最近使用、负载、粘性和等待路径，不因高级调度器的存在改变排序或失败语义。高级调度器只在各平台先完成现有硬过滤后接管候选排序：

1. 适配层先执行分组、平台/混合模式、模型、能力、账号状态、限流、代理、privacy、配额、窗口费用和 RPM 等硬过滤。
2. 通用核心对剩余候选组合优先级、负载、队列、错误率 EWMA、首 token 延迟 EWMA、窗口重置和可选会话粘性分数，并在 Top-K 内做加权无放回选择。
3. 暂时满槽或负载率为 100% 的硬资格合格账号仍进入高级核心；选择前逐账号复核真实并发槽，全部满槽时可产生等待计划，无槽探测则忽略占用。没有结果反馈时错误率按 0% 计算；负载快照、TTFT、窗口或平台专属额度缺失时仍使用中性信号，不能据此排除账号。
4. 实际由高级调度器选出的转发结果、失败、TTFT 和切换会回写运行时统计；流已开始后的不可切换边界不变。

OpenAI/Grok 是通用核心的能力适配者：在高级分组中，OpenAI 额外处理 previous response、订阅优先、Responses transport、旧版 Compact 和额度余量，Grok 继续执行自身配额及媒体能力约束。Anthropic/Gemini 的 mixed bucket 仍只纳入显式开启 mixed scheduling 的 Antigravity 账号。关闭粘性加权时，各平台保留硬会话粘性；OpenAI previous response 不可跨账号移动时无论开关状态都保持硬绑定，可移动时才作为加权信号。共享错误率或 TTFT 超过通用逃逸阈值时只对当前请求逃逸，并保留原绑定。开启粘性加权时，上一响应和会话账号只获得评分加成，并与其它 Top-K 候选一起按权重抽样，不能被强制置首，也不能在 Top-K 尝试失败后获得额外硬兜底；window-cost/RPM 的 sticky-only 区间仍允许当前绑定账号进入评分。非 OpenAI 平台没有 previous-response 绑定语义，诊断输入中的该信号标记为 `ignored`。管理列表直接通过 `account/provider.SchedulerScoreOptions` 投影无凭据评分输入，app 注入生产选择共享的 `schedulerSharedState` 与原生设置 Store，不再往返转换旧账号实体。Codex 额度余量的八小时有效期与次窗口折扣由 account 唯一计算。管理端“高级调度评分”只对高级分组显示，并复用同一评分函数和候选池；其展示不把账号变成可用候选，也不取代请求级硬过滤。

账号与模型的短暂失败状态由 `account.ModelTransientState` 持有，网关调用点沿用同一实例；模型先完成平台规范化，再记录和查询。首次失败只计数，第二次冷却 10 秒，连续三次及以上冷却 45 秒，成功清零；30 分钟状态保留窗口与容量上限保持，进程重启不恢复这些内存状态。

## 评分诊断

管理员可通过 `scheduler/httpapi` 的账号高级调度评分诊断查看当前候选池的实时解释。`scheduler.DiagnosticService` 拥有候选、参数和解释计算，旧资格端口只在单次调用中关联执行投影，不把凭据带入核心。基准诊断不指定模型、会话粘性或上一响应粘性；模拟诊断只接受模型和两个账号 ID，不能接收 session hash、previous response 内容、凭据或代理认证信息。诊断使用无分页分组全集统计排除原因，并复用生产服务可安全执行的模型运行时封禁、额度、窗口费用、RPM、代理流隔离、OpenAI/Grok 配额自动暂停、影子母账号健康和渠道限制；它不会获取并发槽、注册会话、写入粘性或修改运行时统计。endpoint、transport、Compact、媒体等缺少请求输入的门禁以 `not_evaluated` 策略信号返回，真实请求仍会在完整上下文中追加检查。

Spark 影子的母账号资格由 `account.ParentHealthyForShadow` 统一判断，调度、诊断和 WS 复核投影到同一规则：母账号须存在、仍为 OpenAI OAuth，且凭据未因状态、到期或临时停调失效；母账号自身的全局限流、过载和手动调度开关不连带禁用影子。`account/provider.DefaultSparkShadowModels` 在调用时从 OpenAI 的唯一 Codex 别名表构造独立的恒等映射，app 直接将它注入账号管理，不保存第二份模型表。

评分核心在单次候选池中固定输出 `base_score = Σ(weight_i × normalized_i)`、`final_score = base_score + sticky_bonus`、`selection_weight = final_score - top_k_min_score + 1`、`selection_probability = selection_weight / top_k_weight_sum`。开启粘性加权时，诊断概率就是包含粘性加成后的 Top-K 抽样概率，不再附加置首规则。开启订阅优先且存在可用 ChatGPT 订阅账号时，排名、Top-K 和概率只基于订阅池，普通账号标记为 deferred；订阅池不可用时才使用普通池。关闭粘性加权且硬粘性账号可用时，诊断保留其原始排名和 `in_top_k` 状态，但把实际选择模式标记为 `sticky_forced_first`，被强制账号概率为 1，其它候选概率为 0。发生粘性逃逸时，原绑定账号按普通候选执行 window-cost/RPM 门禁；逃逸和缺少上下文的能力门禁继续作为独立策略信号展示。

错误率和 TTFT 使用共享的运行时 EWMA；错误率以 0% 为初始基线，没有反馈样本时按 0% 计算，归一化健康度为 1，首次失败会从该零基线更新 EWMA 并立即低于完全未观测账号。每个聚合值还保存样本数和最近观测时间。诊断对未观测错误率明确显示“0%（未观测）”，负载、TTFT、窗口重置或平台额度快照缺失时则标注“未观测，使用中性值”，而不是把账号表示为失败或不可调度。负载分母使用账号的 `EffectiveLoadFactor()`。分组覆盖、全局运行时设置与进程默认值均逐字段标注来源，保证诊断公式和实际高级调度路径共用相同有效参数。

<a id="scheduler_snapshot_consistency"></a>
## 快照一致性

协议统一后调度 Redis 命名空间升级为 `sched:v2:`，完整与轻量账号投影均携带 `upstream_protocols` 和认证方式，分组认证快照 v40 携带准入集合、转换映射和 Responses 图片策略。协议候选过滤在评分前执行，每次切号和 fresh/DB 复核重新检查；转发目标只保存在当次账号副本，不污染共享缓存。

调度事件契约及去重编码、SQL 读写位于 `scheduler` 与 `scheduler/postgres`；同事务写入和提交后尽力发布的界限保持。`scheduler.SnapshotService` 拥有重建、事件消费与受限回退，网关读取和生命周期直接绑定这一实例，旧快照服务与 Redis 缓存包装已删除。`scheduler/rediscache` 拥有原 `sched:v2` 发布、epoch/tombstone 和锁协议。其 `codec.AccountCodec` 唯一负责完整/轻量账号的存储形状及字段过滤，保持历史 JSON 字段与 nil/空集合。app 直接把 account/routing 存储和凭据刷新后的原生记录绑定到同一缓存；旧 repository 的备用存储构造、事件绑定和快照发布器已删除，剩余执行形状转接只引用 app 注入的存储。编码器内部持有受控完整记录，核心只读取无凭据的候选元数据。尚未清理的旧执行实体只在读取边界转换，分组读取单独绑定原 routing 来源，保持查询次数。

`scheduler.SnapshotService` 管理 bucket 快照和账号投影。启动时异步执行初始重建，outbox 立即执行首轮；运行中消费调度 outbox，并周期性做全量重建以修复漏通知或外部写入。账号状态热更新可以先发布快照，再通过 outbox/失效广播传播到其它实例。

一致性保护包括：

- bucket 写 token/epoch 阻止较慢的旧重建覆盖新状态。
- 已退役 bucket 使用 tombstone，防止延迟事件把已删除分组重新打开。
- 分组生命周期 lease 协调 reopen/retire，分布式锁约束同一 bucket 的重建。锁句柄持有随机 owner token，释放时比较令牌；旧持有者超时后不能删除继任者的锁，key、字符串存储类型和 TTL 保持。
- 账号变化、分组变化、批量变化、last-used 和全量重建使用不同事件，避免每次热字段更新都重算全部数据。

快照未就绪或读取失败时可以受限回退数据库。回退有独立超时和 QPS 门槛；超过门槛返回 cache-not-ready/fallback-limited 类错误，而不是让故障期间所有请求同时击穿数据库。数据库是账号配置权威源，快照是转发热路径投影；两者短暂不一致时必须用版本栅栏和后续重建收敛。

快照运行时重复 Start 不会再次启动或重建；Stop 取消周期、排队和支持 context 的在途操作，并按应用剩余预算等待。Stop 后不能重新启动，超时不等于任务完成，也不等于持久 outbox 已经排空。

消费水位按事件 ID 推进，低 ID 事务在水位越过后才提交时，不保证逐事件补消费；该既有限制由周期全量重建恢复。清理时的十秒宽限只延迟删除，不能使旧 ID 重新进入水位之后的轮询。

## 候选筛选与评分

候选账号依次受以下约束收窄：

1. 分组关联、平台/混合模式、active、schedulable 和账号有效期。
2. 账号级、模型级和 endpoint 级临时不可调度、限流恢复时间及配额状态。
3. 客户端模型经过映射后的最终模型、白名单和账号 capability。
4. OAuth-only、privacy、客户端类型、站点/区域、媒体资格和所需 transport。
5. 当前并发槽和等待策略。

Bedrock 账号的模型筛选包含型号、来源区域及全局推理开关的统一解析。没有已核实有效路由的账号不会进入候选；模型目录与持久可用性诊断复用同一边界，具体优先级和未知规则处理见 [Bedrock 模型与来源区域](../interfaces/anthropic_upstream.md#bedrock_region_routing)。

通过硬过滤后才比较优先级、近期使用、账号负载、排队、错误率、延迟、重置窗口或配额余量。高级核心提供跨平台的固定评分与 Top-K 选择机制，OpenAI/Grok 适配器只补充其请求确实具备的 Responses transport、WebSocket、旧版 `/responses/compact`、原生 `remote_compaction_v2`、previous response、订阅和额度信号。原生 V2 继续使用普通 Responses 模型路由，独立读取管理员配置的 `openai_native_compaction_v2_mode`；它不读取任何历史探测状态或旧版 Compact 开关。管理员关闭时才排除该压缩能力，开启仍不能绕过普通 Responses 端点能力。调度元数据投影必须保留两类压缩开关与文本路由配置。压缩资格只区分启用和关闭，不再维护未知能力等级。快照显示关闭的账号仍可在末尾进行数据库复核，管理员重新启用后不会因旧快照被永久漏选；数据库确认关闭的账号不得获取最终调度资格。上游声明倍率、OAuth 参考倍率和 `upstream_cost` 权重不再参与候选排序或评分；账户本地 `rate_multiplier` 与渠道上游计费模型来源只属于结算。新增评分项不能绕过硬资格，也不能因缺少观测把账号永久降为不可用。

## 账号自动停调阈值

数据库运行时设置 `account_scheduling_thresholds` 只接受 `openai`、`anthropic`、`grok` 三个平台和 1-100 的整数。缺失平台补为 100；100 表示禁用。账号凭据中的 `account_scheduling_threshold` 可覆盖平台默认值，同样只接受 1-100，浮点值按最近整数归一。Gemini、Kiro 和 Antigravity 即使携带观测字段也不参与这项门禁。

每次候选调度前使用当前额度快照评估阈值：OpenAI 读取身份匹配、尚未重置且未超过自动暂停陈旧期限的 Codex 5h/7d 窗口，`codex_*_used_percent` 始终按 0-100 百分数原值解释，`1.0` 表示 1% 而不是 100%；Anthropic 的 utilization 字段继续按 0-1 比例换算百分比。Grok 只读取响应头投影的滚动 quota 窗口，不使用官方 7d/30d 账单周期。多个窗口同时达到阈值时，选择重置时间最晚的窗口作为暂停截止；已过期、明确陈旧、缺少截止时间、身份不匹配或缺少观测的快照不会暂停账号。历史快照缺少或无法解析 `codex_usage_updated_at` 时保持原有 fail-closed 语义，不会仅凭缺失时间逃逸阈值门禁。

命中后服务写入带结构化来源的临时不可调度原因和窗口截止时间，并触发账号状态失效传播；相同阈值状态不重复写入。管理员可从暂停原因区分阈值门禁与普通限流。设置读取使用进程原子缓存和 singleflight 回源；配置项不存在时，默认阈值按正常 TTL 缓存，只有真实数据库故障使用较短错误 TTL。显式保存后立即替换缓存，未提供该字段的部分更新不能用默认值污染现有缓存。

OpenAI/Grok 的进程内停调、同账号 429 恢复窗口和刷新失败发布代次由 `account.RuntimeBlockState` 统一持有，app 将同一实例绑定给执行与恢复消费者。Grok 暂定停调回滚只撤销本次安装的代次；后来延长或重新声明的停调不能被旧回滚删除。凭据版本阻断仍使用 `RefreshFailureBlocks`，无匹配版本时不计算身份散列。旧执行入口只做平台资格和记录投影。

<a id="session_lifecycle"></a>
## 粘性与等待

显式 session、previous response、WebSocket 或平台内部上下文可以建立粘性。命中账号仍需重新通过当前快照的状态、分组、模型和策略校验；账号被禁用、移组、限流、混合调度关闭或能力不再满足时，旧绑定必须失效。

app 分别构造唯一的 `scheduler.SessionLimitCache` 与 `billing.WindowCostCache`，网关按两个原生端口使用它们。会话注销只操作调度会话，窗口费用沿用 `window_cost:account:` 与 30 秒缓存 TTL；两个数据面共享 Redis 客户端，不再通过旧组合接口互相暴露操作。

Anthropic OAuth/Setup Token 账号的 `max_sessions` 限制空闲窗口内的活跃会话数。Messages 正常成功（包括流式和切号后成功）或返回可结算的部分结果后，成功账号的会话注册必须保留，由最后活动时间和空闲超时决定过期；请求结束只释放请求并发槽。未成功服务的失败请求以及已放弃的账号 attempt 立即注销对应会话，避免失败请求占满空闲窗口。

当粘性账号的并发槽和有界等待队列都已满时，负载感知选择可以为单个请求临时使用其它合格账号；这类容量溢出不会改写持久粘性绑定，后续请求仍优先回到原账号。

并发已满但候选可能很快释放时，请求进入有界等待队列。等待继续使用原轮询退避、总截止时间和上下文取消；不能持有已失效账号的选择结果。没有任何永久合格候选与暂时无并发槽是不同错误，前者不应靠长等待掩盖。

请求资源以 `Lease` 和 `AttemptLease` 表达所有权：用户槽、API Key 统计和确认取得的等待计数归请求租约，账号槽、串行锁和会话登记归当前尝试。获取后立即登记，完整账号补全或协议复核失败时回滚，重复释放只执行一次。等待增加失败仍按原策略放行，但未确认取得的计数不递减，结果不明时沿用 TTL 自愈。同步 `WaitObserver` 只通知 HTTP 输出心跳，SSE 格式、Flush 与写失败仍由 HTTP 拥有。

会话批量查询使用可冷启动的 Lua 调用，Redis 脚本缓存被清理后仍执行原查询，不把 NOSCRIPT 当作会话缺失。`AttemptLease.Finish` 使用实际成功、可结算部分结果或失败决定保留/注销；物理槽提前释放不等于逻辑服务失败。WS 入站和 Live 租约保持独立，图片本地限流不合并进 Redis 账号池。

释放模式由原调用方明确选择。Qoder 已进入上游的流式请求使用完成释放，客户端断开只停止下游写入，仍在原预算内收集尾部 usage 后归还资源；等待和非流取消保持原行为。真实业务输出后的重试边界由网关执行层判断，scheduler 不建立第二套 failover 循环。

## 失效与恢复

以下变化必须使相关账号投影或 bucket 失效：账号启停/删除、凭据刷新、分组关系、优先级、模型映射/白名单、代理可用性、限流与临时不可调度、mixed scheduling、privacy 和可调度资格。影响多个平台 bucket 的 Antigravity 混合账号要同时更新原生目标平台与 Antigravity bucket。

写数据库成功但失效广播失败时，应记录可操作告警并依赖周期重建收敛。仅清本实例缓存不能保证多实例一致；仅发通知而不保存权威状态也会在重建后回退。

账号 extra 的原子合并只保留仍有效的系统状态语义，例如 Ollama Cloud 管理会话和用量快照。凭据或代理变化仍按各自规则保存或失效 Ollama 状态；历史 `upstream_billing_probe` 与 `upstream_billing_probe_enabled` 在所有写入边界直接丢弃，不再触发专用 CAS、outbox 或调度快照失效。

## API Key 用量展示缓存

API Key 上游用量是控制面查询，不属于调度快照。`UpstreamUsageService` 在请求前后重新读取账号凭据、代理、Base URL、TLS 设置和 `extra.upstream_usage_query`；身份指纹变化时丢弃结果并返回冲突。singleflight 只合并相同账号和配置指纹，等待方取消不会取消共享查询；并发槽限制上游查询，但不会占用网关账号调度槽。

前端账号列表把成功结果放入按管理员隔离的 `sessionStorage`，TTL 为五分钟。缓存键包含账号 ID、`updated_at`、代理/Base URL 和规范化适配器配置；账号保存、列表增量发现配置变化或凭据/代理变化时清除内存和浏览器条目。列表加载、虚拟滚动和自动刷新仅恢复已有缓存，不主动访问上游。结果永远不写调度缓存、数据库或 `Extra`，因此查询失败不会改变转发行为。

## 诊断不变量

- “无可用账号”诊断要区分无分组关联、硬资格过滤、模型/endpoint 不匹配、临时限流、并发等待超时和快照不可用。
- 日志和 Ops 记录使用账号 ID、bucket、平台、模型和过滤原因，不记录 token 或完整凭据。
- 修改筛选或评分时同时覆盖缓存命中、数据库回退、粘性命中后失效、多实例乱序事件和流开始后的失败。
- 管理端账号测试成功不等于所有请求协议都具备 capability；调度仍按实际 endpoint 判定。

相关文档：[网关请求生命周期](gateway_request_lifecycle.md)、[网关策略控制](../domains/gateway_policy_controls.md)、[账号维护](../operations/account_maintenance.md)。

### 分组定价快照

当前认证缓存版本为 40，沿用已有 key、TTL/jitter、负缓存、发布订阅和 outbox 延迟二次失效协议；不根据旧媒体字段是否为空推断快照完整性。认证快照中的 `ModelPricing` 保存完整分组价卡，包括上下文区间、Fast/Flex、Max 推理和分时规则。普通 Key 与复合 Key 的分组投影使用独立副本；金额指针、模型列表、区间和分时段均不能被单次请求修改后污染缓存。分组更新事务提交后使用现有按分组失效机制，价格继承与展示共享相同解析规则，见[分组模型价卡与倍率继承](../domains/routing_and_billing.md#group_model_pricing)。

### Key 认证快照的所有权

`apikey` 拥有认证缓存与回源并发控制，Redis 技术实现位于 `apikey/rediscache`，失效 outbox 存储位于 `apikey/postgres`。每次返回独立请求副本，复合选组、模型映射和分组回退不能共享可变 map、slice、指针或嵌套策略。重连沿用现有重新订阅及安全清理机制；生命周期在在途操作结束后再关闭订阅和 L1。该兼容性不扩大平台额度的单进程协调范围。
