# S15：收尾 HTTP 注册、DTO 与设置聚合，冻结历史问题清单

## 1. 基线与实施边界

以当前 `main`、HEAD `34eebbe3ef2c01fbf6b9473ec746fe495171a2b2` 为起点，完成 S15.0—S15.4。

实施前将本计划原样保存为 `refactor/S15-http-settings.md`，随后登记 roadmap 链接和“实施中”状态。执行记录追加到阶段文件，证据保存到 `refactor/baseline/S15/`，不另存 `.agents/plans/`。

已核实：

- Go 1.27.0、golangci-lint 2.13.2、Docker 29.5.2 可用。
- 定向普通测试 **409**、设置相关 unit race **428**、embed race **101**、真实 PostgreSQL/Redis integration race **15** 条通过事件，无失败或跳过。集合存在重叠，不相加、不代替全量验收。
- 规划期间 **14,564 个已跟踪文件摘要未变**，索引和工作区保持原状，原有 **50 个其他任务未跟踪文件**未动。
- 原实现复现、临时 Go overlay、日志和摘要位于 `/tmp/tokenrouter-s15-planning/`。三项问题在 race 模式下各重复三轮，均触发预期失败；不计为通过。
- **全程串行，不使用 subagent。** 不自动提交、推送或切换分支，不提交 `SYNC.md`。

保持 URL、认证、JSON、配置键、SQL schema、缓存命名空间、金额及 standard/simple 契约；只生成 Wire，不生成 Ent、不修改已发布 SQL。

历史问题主动排查到此结束。执行阶段只开展迁移、以下固定修复和约定验证。本次引入的回归必须修复；清单外历史问题只保存当次证据并登记，即使阻塞验收也先请求调整计划，不自行追加复现或修复。

## 2. 固定修复与设置保存决策

用户已确认三项全部纳入，并选择综合设置统一原子保存。

| 编号 | 原实现复现 | 确定修复 |
| --- | --- | --- |
| B01 | 综合设置的 Fast 策略校验返回 400，但同请求的站点名称已经保存 | 所有设置参与模块先校验并生成变更；系统设置、认证默认值、Fast 策略及支付配置合并为一次 settings 原子写入，成功后才应用运行状态与通知 |
| B02 | 旧 backend mode 回源晚于管理更新结束，覆盖刚发布的新值 | 唯一运行实例使用发布代次；回源发布前核对代次，旧加载不得覆盖管理更新，等待者不得重新发布旧结果 |
| B03 | 旧公开设置回源跨过 HTML 缓存失效点后，重新发布旧页面 | HTML 渲染绑定失效代次，过时代次不能填充缓存；响应的 HTML 与 ETag 来自同一次渲染，后续请求重新读取有效配置 |

B02 保留原 TTL、缺键/故障默认和读取预算，不把修复扩展为未经复现的全量缓存改写。B03 允许已开始的请求完成自身快照响应，但不得污染后续请求缓存；保留 nonce 替换、304 和静态资源缓存规则。

综合设置的保证分为两层：

- **提交前**：字段绑定、权限校验、业务校验或数据库写入失败，整批设置不变，不发布运行状态、不发成功通知。
- **提交后**：数据库已经成功，运行应用失败不能伪装成数据库回滚。返回 `SETTINGS_APPLY_FAILED`，标明已持久化及失败模块，不暴露敏感值；保留可重新加载的数据库事实，不增加自动写回或持久重试队列。
- 原本明确为尽力执行的辅助行为继续保持其保证，例如钉钉属性补齐；不将所有通知或辅助动作升级为事务条件。
- 专用设置入口复用所属模块的校验与应用实现，保留其原有写入范围和广播行为。

## 3. 实施步骤与接口

### S15.0：冻结路由、字段与消费者清单

归档规划资料，记录实际 HEAD、索引、工作区、工具版本、SQL、Ent/Wire 和源码摘要，以及 S14 完整验证结果。

建立两份主要账本：

- **路由契约**：method、完整路径、别名、所有者、认证/限流/审计/step-up 顺序、body 读取时点、响应类型、构建条件和测试。
- **设置契约**：请求字段、存储键、业务所有者、缺省及省略/null/清空语义、敏感值处理、校验顺序、提交后应用、缓存与通知。

同时登记 DTO、旧错误转接、Gin Context 业务值、Wire 和文档消费者。现有旧 handler 主目录、admin、dto 和 routes 分开清点；已经不存在的旧 middleware/quotaview 路径不重新创建。

### S15.1：模块路由注册与 HTTP 装配

- 各模块 `httpapi` 拥有自己的注册函数，接收具体处理器及所需 middleware。不接收全局 Handlers，也不通过字符串查找处理器。
- app 构造处理器和认证、限流、审计依赖，按当前顺序装配注册函数；server 只持有 HTTP Options、全局中间件、静态资源入口和注册函数列表。
- server 不再接收具体 APIKeyService、SubscriptionService、OpsService、SettingService，也不承担业务实例构造。
- 保留共享路由组的中间件顺序，避免重复安装认证、审计或限流。OAuth 中属于支付的续接入口由 payment 注册，保留原 URL 和所处认证链。
- common 健康检查、正常模式 setup 状态和遥测兼容入口归 server；业务路径及别名由所属模块声明。网关继续复用既有端点目录，不建立第二份协议表。
- 删除 Handlers、AdminHandlers 和旧 routes 汇总。其测试迁到所属模块或 app 的装配契约测试，保留原断言。

重点核对裸路径、强制平台入口、Responses 子资源、支付 webhook 原始签名输入、WebSocket 子协议，以及 `/models` 的 API/HTML 内容协商。

### S15.2：DTO、身份上下文和通用适配清理

- DTO 与映射由实际输出模块拥有；用户与管理员字段继续分离，保持浅层关联、递归截断、nil/空集合、时间、省略字段及历史 Ent 输出形状。
- 现有泛型 DTO 在 app 的具体处理器装配处实例化；跨模块查询通过明确投影接口接入，不再建立集中别名或 mapper 包。
- 删除旧 handler/admin/dto 聚合生产代码。相关行为测试随实现迁移；旧 service/domain 测试若必须保留兼容入口，仅使用测试局部转接，并登记 S16 删除项。
- 认证与业务状态使用既有 Principal、AccessSnapshot 和显式请求状态；Gin 只保留 HTTP 适配所需访问。业务核心不通过 Gin 或旧 Context 读取完整实体。
- JWT、管理员、Key、step-up、审计使用各模块原生 middleware；server/middleware 保留通用 HTTP 功能及面板限流适配。
- HTTP 消费者改用 httpx；核心错误比较使用 apperror 类别与 reason。清零旧 `Code/ToHTTP`、response 转接的消费者后删除对应 HTTP 兼容入口，保留需在 S16 清理的非 HTTP 类型别名清单。
- 不改变普通 Key 门禁的 body 读取时点，也不重新实施网关算法或平台取消策略。

### S15.3：设置参与接口与唯一运行实现

settings 保留通用存取、版本、通知及综合更新协调；业务解释回到所属模块。

| 所有者 | 设置职责 |
| --- | --- |
| identity、team、promotion | 注册/认证/provider/captcha/安全设置、默认接纳参数、团队与推广开关 |
| notification、site | SMTP 与通知呈现；品牌、菜单、登录协议和公开展示 |
| gateway、account、egress | 入站与转发策略、Fast/客户端规则；账号健康与导入默认；出站策略 |
| scheduler、billing | 调度运行参数；资金显示、权益和额度相关规则 |
| usage、ops、creative | 查询展示、预聚合、监控设置、创作模型和 worker 参数 |
| payment、search、moderation、backup | 复用各自已迁的配置实现，不复制缓存或注册表 |
| server | 面板限流与客户端地址策略的 HTTP 应用 |

具体按字段职责登记，不能仅按名称前缀分派。供应商交换、发现等网络行为仍通过对应 provider 端口执行。

**综合设置接口与流程：**

1. app 静态注册设置参与者及明确执行顺序；重复字段或键所有权在装配时拒绝，不做运行时插件系统。
2. HTTP 保留原扁平请求/响应形状，解析字段存在性并执行原权限门禁，再投影为模块输入。
3. 协调器在可取消的更新保护内读取当前值。各参与者的 `Prepare` 只校验、规范化和生成 `PreparedChange`，不得写数据库或发布缓存。
4. 合并变更，调用唯一 Store 的一次原子批量写入。共享 JSON 设置由其唯一所有者合并，不能让多个模块互相覆盖。
5. 成功后按登记顺序调用模块应用能力，再执行原有公开设置/CSP、缓存失效及 worker 通知。所有应用处理都使用已提交值，不使用含省略零值的原请求。
6. 必要应用失败按第二节返回明确结果；成功响应继续按原形状读取和投影，不用默认零值掩盖必需数据读取失败。

普通读取保留原批量查询、额外查询时点、TTL 和 singleflight 作用域；不拆成逐字段查询。完整 config 的缺省转换放在 app，业务模块接收独立 Options。

旧 SettingService 改为窄委托；规则与缓存只有一份，不整体搬成新的巨型 SettingsService。公开 API/embed/CSP 继续由 site 提供安全投影，web 不接收敏感设置。落实 B02、B03，并保留原 HTML/CSP 更新次序。

### S15.4：生命周期、门禁与文档收尾

- app 持有唯一设置协调器、模块运行实例和通知订阅；构造不启动后台工作，启停复用现有 lifecycle。
- 新增的协调等待受 context 约束。关闭封闭更新入口，等待在途更新和应用，再注销订阅、关闭共享依赖；保留 HTTP 五秒与后台三十秒预算。
- 删除迁出文件的 depguard 许可；按核心、HTTP、存储、provider、server、web、app 角色约束。仅在消费者清零后删除旧路径规则，不整体放开 Adapter。
- 同步架构、HTTP、配置、入口安全、网关生命周期和开发文档，保留稳定锚点，准确说明综合设置的数据库原子性与提交后应用边界。
- S16 接收旧 service/repository/domain/model、legacybridge、Ent 源引用及跨模块测试的最终清理；逐项列出实际消费者，不用全局 alias 文件延期。

## 4. 验证与验收

每批运行迁移包、旧转接及直接消费者测试。范围固定如下：

| 验证面 | 必须取得的证据 |
| --- | --- |
| B01 | Fast/支付后段校验失败无写入；真实 PostgreSQL 批量失败整体回滚；提交前无缓存或通知变化 |
| 设置应用 | 省略/null/清空、敏感字段保留、共享 JSON 合并、连续更新、应用失败明确返回已持久化、原尽力副作用 |
| B02 | 旧回源与管理更新两种顺序、连续保存、等待者结果、缺键/故障默认和 TTL |
| B03 | 失效与回源交错、旧渲染不能填缓存、HTML/ETag 一致、nonce、304、后续请求取得新配置 |
| 路由 | 全部 method/path 对照、重复注册、别名、认证/限流/审计/step-up 顺序、公开与管理员拒绝边界 |
| DTO | 用户/管理员字段、递归关联、敏感凭据、空集合、时间、分页、CSV 和错误链兼容 |
| HTTP 模式 | embed/noembed、SPA fallback、`/models` 协商、普通 Key 不提前读 body、webhook/WS/SSE 回归 |
| 生命周期 | 唯一实例、更新等待取消、订阅注销、在途应用完成及 Redis/SQL 最后关闭 |

固定问题回归保留行为断言，屏障不得依赖修复前必然发生的错误交错。共享状态执行定向 race；设置事务与认证限流使用隔离 PostgreSQL/Redis。外部发现使用本地 HTTP 夹具，不调用生产服务，不扩展全仓 race 或 benchmark。

depguard 夹具覆盖合法方向、精确旧许可、新文件拒绝、旧文件新增禁止 import、迁出例外失效及正常 Adapter，保存诊断后删除。核对普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 构建选择。

收尾在 backend 使用 `GOTOOLCHAIN=go1.27.0`，串行执行：

```bash
go generate ./cmd/server

go test -count=1 -json ./...
go test -count=1 -json -tags=unit ./...
go test -count=1 -json -tags=integration -p=4 ./...

golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=unit ./...
golangci-lint run --timeout=30m --max-same-issues=0 --max-issues-per-linter=0 --build-tags=integration ./...

make build
make -C .. test-frontend
make -C .. build-frontend
go build -tags=embed -o bin/server-embed ./cmd/server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server
```

另验证两个维护命令、Wire 再生成无差异、真实前端产物的 embed 测试，以及 standard/simple、setup 和 SIGTERM 装配回归。

S14 lint 基线 **1 / 283 / 17** 按路径映射、规则和完整消息比较。迁移引入的问题必须解决，不扩大忽略规则。跳过、仅编译及既有外部环境限制单列。

## 5. 完成、交接与回退

完成须同时满足：

- 路由由目标模块注册，server 无旧业务服务依赖；全局 Handlers/AdminHandlers、旧 routes 和 DTO 聚合已删除。
- 设置规则由所属模块唯一拥有，综合更新完成一次原子保存；提交后失败语义有行为证据。
- B01—B03 修复通过，原失败资料保留；必要验证未完成时保持“待验”。
- 旧 HTTP 转接、上下文、测试兼容、Wire 和剩余 S16 消费者有精确清单。
- SQL、Ent、S00—S14 冻结资料和其他任务文件无意外变化；原计划正文保留，新增与已有文件的 diff 检查通过。

满足后 roadmap 更新为 **16 / 17**、S15 已完成，下一步编写 S16 子计划。交付可审查差异，不自动提交。

回退按子步骤恢复代码、装配、门禁和文档，不涉及数据库或缓存格式降级。先停止在途设置更新；已提交的配置事实不自动还原。撤销 B01—B03 将恢复部分保存、旧 backend mode 覆盖及旧 HTML 缓存重新发布风险。

---

## 执行记录（持续追加，S15 尚未完成）

### 2026-09-17：S15.0 冻结与第一批实现

- 实施 HEAD 为 `34eebbe3ef2c01fbf6b9473ec746fe495171a2b2`，分支保持 main。原计划正文为 14,107 字节，SHA-256 为 `427d65fd332739dfaa03431752f5fb4151469728e822b460bb11c0436b490e04`，后续仅追加记录。
- 规划 overlay、三轮预期失败和定向基线已归档至 [planning](baseline/S15/planning/)。初始源码摘要与工作区见 [initial-state.json](baseline/S15/initial-state.json)、[initial-checksums.json.gz](baseline/S15/initial-checksums.json.gz)。没有修改原有 50 个其他任务未跟踪文件，没有暂存或提交。
- 已建立 [路由源码声明基线](baseline/S15/routes-baseline.json)、[当前路由声明](baseline/S15/routes-current.json)、[对照](baseline/S15/route-table-comparison.json)。两边各 693 条 method/path 声明；这是源码证据，不能替代全图装配和权限次序验收。
- 已登记 [295 个综合设置输入字段](baseline/S15/settings-field-inputs.json) 与 [162 个旧 SettingService 方法](baseline/S15/remaining-setting-methods.json)。字段业务所有者与完整消费者仍待逐项拆分，未按前缀猜测并宣告归属完成。

### 2026-09-17：固定修复与设置提交边界

- B01：新增唯一 Store 所属的 Updates/UpdateSession，保护旧值读取、准备、一次批量提交及后置应用。Fast 与 payment 的校验/编码可先生成变更，不再分批写入综合设置。提交后的必要应用失败返回 `SETTINGS_APPLY_FAILED` 与已持久化标记。设置参与者的静态字段/键注册，以及旧规则完整迁出尚未完成。
- B02：backend mode 的唯一运行缓存迁入 gateway/admission，代次保护阻止旧回源覆盖更新；保留原 TTL、缺键/错误默认与独立回源预算。
- B03：HTML 缓存使用失效代次发布，过时回源只完成自身响应、不回填共享缓存；响应 ETag 与自身 HTML 配对。
- [cache-fixed-race.json](baseline/S15/cache-fixed-race.json)：83 条通过；[atomic-update-contracts.json](baseline/S15/atomic-update-contracts.json)：343 条通过；[native-update-race.json](baseline/S15/native-update-race.json)：57 条通过。集合重叠，不能相加。
- [settings-http-fixed-race-recheck.json](baseline/S15/settings-http-fixed-race-recheck.json)：18 条通过，包含一次写入、写失败无应用、应用失败明确持久化及 B02 交错。
- [settings-atomic-postgres-race.json](baseline/S15/settings-atomic-postgres-race.json)：真实隔离 PostgreSQL 的约束触发失败取得通过证据，整批设置保持旧值且没有运行应用。
- 本次迁移回归：Fast 深复制曾把显式空数组改成 null，被新增 HTTP 契约捕获；改用保留 nil/空数组区别的复制后复验通过。原失败 [settings-http-fixed-race.json](baseline/S15/settings-http-fixed-race.json) 保留，没有削弱断言。

### 2026-09-17：路由、HTTP 参数与依赖门禁

- server 只接收 Options、middleware 与注册函数；配置优先级由 app 投影。common 归 server，payment、身份、公开设置/退订/市场、用户路由与大部分管理员注册已由各模块拥有。
- 网关路径分派与协议门禁迁入 gateway/httpapi，批量图片注册归 batchimage；旧 gateway 注册入口仅构造兼容参数。Responses 路径提取与白名单继续共用唯一实现。
- 账号管理注册与 scheduler 诊断分开；Codex 邀请重置 HTTP 通过 account 的窄操作接口调用既有实现，不扩展其业务或供应商能力。
- 当前仍保留全局 Handlers/AdminHandlers、server/routes 转接及旧 DTO/设置聚合，不能宣布 S15.1/S15.2/S15.3 已完成。
- [http-routing-contracts-unrestricted.json](baseline/S15/http-routing-contracts-unrestricted.json)：1073 条通过；[native-gateway-routes-complete.json](baseline/S15/native-gateway-routes-complete.json)：244 条通过。早期沙箱禁止监听临时端口的失败单独保留，外部执行后取得实际结果。
- [route-gates-clean.json](baseline/S15/route-gates-clean.json)：当前普通 depguard 0 项；这不是完整 lint 或普通/unit/integration 夹具验收。
- Wire 仅由生成器更新，见 [wire-native-routes.json](baseline/S15/wire-native-routes.json)；[command-build-check.json](baseline/S15/command-build-check.json) 为命令可构建性证据。
- 文档已同步本批实际变化；完整所有权、链接与路由锚点收尾仍待最终迁移完成。
- 执行偏差：前段有只读验证命令与后续处理短暂重叠，未使用 subagent；后续验证按串行执行。测试搬迁期间的缺失测试私有转接、重复 import 等编译失败均保留原日志，修复后取得对应复验结果，不计入既有失败基线。

### 下一批必须完成的工作

1. 用各模块原生构造参数和注册函数替代 app 的全局 Handlers/AdminHandlers，移动相应路由契约测试后删除 server/routes。不得用改名后的全局聚合代替删除。
2. 将综合设置的准备、领域规则、动态读取与缓存按 295 个字段/162 个方法逐项迁回所有者；建立真实生产注册表，构造时拒绝重复字段/键。旧 SettingService 仅保留已登记的委托，不能整体复制成新服务。
3. 删除旧 DTO/mappers 聚合及 HTTPCode/response 转接，完成原生主体/请求上下文与 middleware 改绑；整理 S16 精确残留清单。
4. 完成构建选择、可丢弃 depguard 夹具、相关真实进程与集成/race、完整 normal/unit/integration/lint、前端及各构建验收。当前不能更新为 16/17。
5. 重新核对 Wire 幂等、SQL/Ent/历史冻结资料与其他任务文件、文档锚点及全部新增文件 diff；必要验证未完成保持待验。


### 2026-09-17：删除聚合与扩展原生设置（继续实施）

- 已删除生产全局 `Handlers/AdminHandlers`、`server/routes` 和 `handler/dto`；app 分别装配原生认证、用户、管理员、网关和支付注册函数。旧路由/DTO 断言迁入模块或 app，旧实体仅作为精确测试夹具。前述“仍保留聚合”的执行记录描述的是当时状态，本批已完成对应删除。
- DTO 映射直接使用 account/apikey/routing/identity/usage/billing 的唯一实现；综合设置响应仅保留原扁平 JSON 值投影，没有另建集中 mapper。
- 面板设置移入 `server/runtimeconfig`；身份注册、安全、captcha 与管理员密钥设置归 identity；账号冷却、流超时、健康熔断和导入模板归 account；推广读取归 promotion，用量展示归 usage，保留期归 audit，整流/Beta/Fast 和转发配置缓存归 gateway。相应专用设置 HTTP 直接绑定原生 handler。旧入口只委托已经迁出的能力，剩余 OAuth/综合准备的收尾仍在实施。
- JWT、管理员认证、step-up 与审计生产装配直接调用 identity/audit HTTP 能力，旧实体装配移为测试局部适配。SessionBinding 的完整配置投影归 app。
- 新 `settings.Registry` 在构造时核对模块、字段及键所有权，并按静态顺序隔离输入和准备变更。Fast 与支付准备器已接入综合更新；其余综合字段的静态参与者尚未补齐，不能据此宣布 S15.3 完成。
- [native-panel-and-dto-contracts.json](baseline/S15/native-panel-and-dto-contracts.json)：6,652 条通过、2 项跳过，无失败；[panel-promotion-native-unit.json](baseline/S15/panel-promotion-native-unit.json)：996 条通过；[native-account-settings-http.json](baseline/S15/native-account-settings-http.json)：321 条通过；[native-domain-settings-contracts-recheck.json](baseline/S15/native-domain-settings-contracts-recheck.json)：401 条通过。
- [native-gateway-settings-contracts.json](baseline/S15/native-gateway-settings-contracts.json)：382 条通过；[native-forwarding-settings-race-final.json](baseline/S15/native-forwarding-settings-race-final.json)：296 条通过；[native-auth-middleware-contracts.json](baseline/S15/native-auth-middleware-contracts.json)：981 条通过；[static-settings-participants-contracts.json](baseline/S15/static-settings-participants-contracts.json)：101 条通过。事件包含父子测试且集合重叠，不相加。
- [native-composition-dependencies.json](baseline/S15/native-composition-dependencies.json) 登记旧目录规则删除和迁移测试的准确许可；[native-composition-depguard-clean.json](baseline/S15/native-composition-depguard-clean.json) 是该时点普通 depguard 通过证据。后续扩展设置/认证的规则仍需同步，不能用此结果替代最终 lint。
- 本批的漏 import、测试类型引用、缓存常量和 Wire 集合位置错误均为迁移期间的自身问题，失败日志独立保留，只有对应复验才计通过。没有开展清单外历史问题审计或修复，没有提交。

### 当前剩余验收范围

继续完成综合设置所有者及静态准备/应用参与者、残余业务上下文与 Key 中间件、旧 HTTP 响应转接清理。随后核对全图路由、各构建标签、精确 depguard 夹具、全量测试/lint/构建、进程与 embed，以及文档锚点和冻结资料。S15 仍为“实施中”，roadmap 保持 15 / 17。

### 2026-09-18：设置所有权、原生 HTTP 与阶段应用

- 已将站点、账号阈值、支付展示、Ops 共享 JSON、网关转发设置全部接入静态参与者。`TestS15SettingsFieldOwnership` 对真实 app 注册表和扁平请求类型逐字段核对，295 个输入全部且唯一命中；见 [字段归属契约](baseline/S15/settings-field-ownership-contract-recheck.json)。这只证明字段登记，不代替各项行为验收。
- `settings/composite` 只组合所属模块的值投影、准备顺序和应用步骤；原设置解释分别进入 identity、site、billing、routing、scheduler、account、notification、promotion、usage、audit、creative、moderation、gateway、payment、ops、search。旧 SystemSettings 仅保留值别名；旧初始化方法没有生产调用方，已移入测试兼容文件。
- 综合 GET/PUT 已直接绑定 `settings/httpapi.Handler`，生产使用 `composite.Runtime`，不调用旧 SettingHandler 或旧 SettingService 执行。预聚合与创作设置端点直接绑定对应原生处理器。提交后回读一次数据库快照，按 gateway → scheduler → account → server → 客户端许可 → 公开通知 → creative → ops 顺序应用，再维持支付配置原刷新位置；必要失败按所属阶段报告已持久化。
- Key 认证、JWT/admin/step-up、Backend 模式门禁的生产装配直接使用原生接口。旧 Key/context 读取者和部分测试转接仍逐文件登记至 S16，未将其宣布全部删除。CORS/CSP 使用 `server/httpconfig` 纯参数，不读取完整启动配置。
- 公开 API/embed/CSP 保持原批量设置读取；身份、团队与用量分别生成安全投影，旧 SitePublicSource 桥接已清零删除。账号阈值、UA/客户端许可、Cyber 及配额自动暂停配置缓存迁入唯一所属实现，保留原 TTL、故障默认和调用时点。
- 定向结果：站点 417、原生 Key HTTP 355、账号阈值 race 384、支付/Ops 设置 350、网关管理设置 578、客户端/审核设置 race 368、公开来源 372、身份回显 426、其他领域回显 416、调度/客户端地址回显 646、配额缓存 race 319、预聚合/创作 HTTP 103、组合快照 417、原生综合 HTTP 84、组合运行时 race 434、最终 HTTP/设置定向 482 条通过事件。集合存在重叠，不能相加；相应独立 JSON 日志保存在本阶段目录。
- 本批迁移回归均保留原失败记录：生成投影时类型与字段同名导致语法错误；原生接口转换遗漏测试构造类型；Backend guard 的旧测试夹具将 typed nil 作为有效接口；遗漏 helper/import。均只修复本次迁移，并保留原业务断言。一次测试受到沙箱 Go 缓存权限阻断，授权后复验；一次直接命令误用系统 Go 1.27.1，已恢复统一 GOTOOLCHAIN=go1.27.0，不计为行为验证。
- 当前普通 depguard 为零项；unit 最近已核对仅原有六项 handler/service 规则违规，后续新文件仍须收尾重验，不以此替代完整 lint 基线比较。
- 已同步架构、HTTP、配置及开发流程文档；当前文件与兼容消费者见 [迁移清单](baseline/S15/migration-files-current.json.gz)、[兼容消费者](baseline/S15/compatibility-consumers-current.json)。[完整性检查](baseline/S15/integrity-pre-final.json) 的 8,768 个受保护文件均未变化，原计划正文摘要一致，索引为空。
- 全量普通测试正在执行；unit/integration、完整 lint、前端、构建、真实进程及最终依赖夹具仍待收尾。S15 保持“实施中”，roadmap 尚未调整为 16 / 17。

### 2026-09-18：第一轮收尾验证

- [普通全量](baseline/S15/s15-final-normal.json)：11,794 条通过事件，四项既有跳过，无失败。
- [unit 全量](baseline/S15/s15-final-unit.json)：19,844 条通过事件，八项跳过，无失败。跳过按实际测试事件单列，不计为行为通过。
- [实际路由清单](baseline/S15/final-route-table-comparison.json)：用当前生产注册函数构造 Gin engine，对比迁移前冻结的 693 条 method/path，全部一致且无重复。该检查与已有鉴权、协议、限流、body 时序测试一起提供装配证据，不把仅声明数量相等当作验收。
- integration 全量已按 `-p=4` 启动，随后进行完整 lint、构建、前端、构建选择和最终门禁夹具。当前仍未满足全部完成标准。

### 2026-09-18：全量集成与 lint 基线核对

- [integration 全量](baseline/S15/s15-final-integration.json)：12,775 条通过事件，五项跳过，无失败。真实进程的 standard/simple SIGTERM、CLI/Web/AUTO_SETUP、两个精简命令、版本注入、初始化失败和监听失败场景通过，见 [进程事件摘要](baseline/S15/process-modes-from-final-integration.json)。
- [完整 lint 对照](baseline/S15/lint-baseline-comparison.json)：normal/unit/integration 为 1 / 283 / 17，与 S14 的文件、规则和完整消息逐项一致，没有新增或扩大忽略。完整诊断保存在 `lint-final-*.json`。
- lint 首轮发现迁移后无生产调用的旧 helper；[清理清单](baseline/S15/unused-compatibility-cleanup.json) 记录删除和测试迁移。最终九项仍有 unit 测试调用，保留为带 unit 标签的测试转接，其余删除；没有修改原断言。另修复新路由清单测试的类型断言检查和格式，并用领域错误类型保留历史大写 HTTP 文案。
- 补齐余额显示、前端 URL 与 Grok URL 模式读取的所属模块委托，保留单键查询、回退时点和原 nil 接收者行为。清理后的定向验证分别取得 423 条通过（服务/设置）和 59 条通过（app），原编译调整失败记录保留。
- 实际 app 注册表的 19 个参与者及全部字段/键见 [参与者](baseline/S15/settings-participants-actual.json)；[字段账本](baseline/S15/settings-field-ownership.json) 已登记逐字段实际持久键，Ops 共享 JSON 的子路径和所有者单列。
- 构建、前端、真实产物 embed 和最终门禁夹具仍在执行。未据此提前调整 roadmap。

<a id="s15_completion"></a>
## S15 完成与 S16 交接（2026-09-18）

S15.0—S15.4 已完成，交付工作树差异，未提交或推送。

| 子步骤 | 完成结果与证据 |
| --- | --- |
| S15.0 | 原计划完整保留；HEAD/索引/原始工作区、源码摘要、字段及路由基线已冻结。 |
| S15.1 | 各模块拥有注册函数，app 注入原生处理器；全局 Handlers/AdminHandlers、旧 server/routes 已删除。实际 Gin 注册的 693 条 method/path 与原清单一致，见 [路由对照](baseline/S15/final-route-table-comparison.json) 与 [中间件链](baseline/S15/route-middleware-chains-final.json.gz)。 |
| S15.2 | DTO/映射归属对应 HTTP 模块，旧 DTO 聚合及 response、Code/ToHTTP 兼容入口已清零删除；JWT/admin/Key/step-up、Backend 与未分组 Key 门禁使用原生规则。旧实体 context 读取仅保留到 S16。 |
| S15.3 | 19 个静态参与者覆盖全部 295 个输入字段及实际持久键；一次原子批量写入，提交后按模块应用。业务解释/缓存由所属模块唯一持有，旧设置入口仅保留投影与委托；公开设置保持批量读取及安全投影。 |
| S15.4 | 更新入口封闭、取消、在途等待与原生命周期装配一致；门禁、文档、构建选择及差异检查通过。 |

最终验收：

- 普通、unit、integration 全量分别为 **11,794 / 19,844 / 12,775** 条通过事件，无失败；分别有 4 / 8 / 5 项跳过。父子事件与集合重叠，不求和。外部供应商、磁盘凭据与已登记分支跳过不计为行为通过。
- 清理及最后门禁迁移后的定向回归通过；[未分组门禁](baseline/S15/native-group-guard-final.json) 270 条通过，[最终真实 PostgreSQL 原子失败 race](baseline/S15/settings-atomic-postgres-race-final.json) 通过。B01—B03 的原失败与修复记录保留；没有扩展历史问题排查。
- [完整 lint 对照](baseline/S15/lint-baseline-comparison.json) 为 **1 / 283 / 17**，逐文件、规则和完整消息与 S14 一致，新增为零；原有诊断未被改成许可。
- 前端 lint、typecheck、14 个关键测试文件的 199 条测试及生产构建通过；普通服务、embed、Linux amd64 和两个维护命令构建通过，见 [构建摘要](baseline/S15/build-final-summary.json)。真实产物下 [embed 测试](baseline/S15/real-embed-final.json) 102 条通过。
- 最后 HTTP 门禁归属收尾后，普通/embed/Linux 构建再次通过；[最终进程测试](baseline/S15/process-after-guard.json) 的 11 个父子事件通过，覆盖 standard/simple SIGTERM、setup、精简命令、版本及失败清理。Wire 只经生成器更新，见 [重复生成稳定性](baseline/S15/wire-final-stability.json)。
- [depguard 行为矩阵](baseline/S15/depguard-behavior-matrix.json) 的 21 个普通/unit/integration 场景全部符合预期，夹具均已删除或恢复。[八组构建选择](baseline/S15/build-selection-final-summary.json) 无加载错误；e2e 的文件选择不代表真实供应商 E2E 执行。
- [完整性](baseline/S15/integrity-final.json)：8,768 个受保护文件不变，SQL/Ent/S00—S14/AGENTS 未发生意外修改，原计划正文摘要一致，50 个原有其他任务文件仍在，索引为空。新增文件和已有差异均通过空白检查，代码锚点有效。

S16 的明确退出项：

1. 按 [最终兼容消费者清单](baseline/S15/compatibility-consumers-final.json) 清理 `service/repository/domain/model`、legacybridge 和旧 provider set；当前唯一实现已在目标模块，不复制规则或缓存。
2. `server/middleware/api_key_auth.go` 的旧 Key/订阅/Ops context 读取仅返回旧形状，实际认证与准入已迁出；逐个改绑剩余读取者后删除，不能直接删除后改变旧消费者的请求状态。
3. 旧 handler/admin 的构造和方法仅为兼容或测试委托；没有生产路由继续绑定旧 SettingHandler。测试局部投影、带 unit 标签的旧 helper 及非 HTTP 错误构造器按实际消费者清零。
4. config 的纯值别名、Ent schema/生成代码的旧类型源引用，连同最后跨模块测试一起交 S16；保持 HTTP/数据库/缓存格式，不借清理改变运行模式或金融行为。

回退按阶段计划恢复代码、规则、装配和文档；先停止在途设置更新，已提交配置不会因回退自动恢复。撤销 B01/B02/B03 分别恢复部分保存、旧 backend mode 发布覆盖和旧 HTML 缓存回填风险。较大日志采用无损 gzip 归档，命令、退出码和实际测试事件摘要保留为独立 JSON。
