# 包与文件清点

详细逐文件声明、构建条件、目标、消费者、测试与 Wire/文档引用见 [inventory.json](inventory.json)。

direct_import_consumers 是 AST 确认的包导入消费者，不将词法同名命中冒充方法调用图。混合业务的实际资金调用另见资金写入清单。

| 源目录 | Go / 非测试 / 测试 / 生成 | 目标归属 | 阶段 |
| --- | --- | --- | --- |
| cmd/cleanup-ingress-reject-logs | 2 / 1 / 1 / 0 | 保留；清理用例归 internal/ops，由命令装配必要依赖 | S08、S14 |
| cmd/jwtgen | 1 / 1 / 0 / 0 | 保留；通过 identity 的明确能力或独立签发工具调用，不引入整套应用 | S05、S14 |
| cmd/server | 4 / 3 / 1 / 1 | 保留入口；Wire 应用图与资源持有移至 internal/app，版本变量留入口 | S02、S14 |
| ent | 233 / 233 / 0 / 230 | 保留 `ent`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| ent/account | 2 / 2 / 0 / 2 | 保留 `ent/account`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/accountgroup | 2 / 2 / 0 / 2 | 保留 `ent/accountgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/announcement | 2 / 2 / 0 / 2 | 保留 `ent/announcement`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/announcementread | 2 / 2 / 0 / 2 | 保留 `ent/announcementread`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/apikey | 2 / 2 / 0 / 2 | 保留 `ent/apikey`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/apikeycompositegroup | 2 / 2 / 0 / 2 | 保留 `ent/apikeycompositegroup`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/authidentity | 2 / 2 / 0 / 2 | 保留 `ent/authidentity`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/authidentitychannel | 2 / 2 / 0 / 2 | 保留 `ent/authidentitychannel`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/batchimageevent | 2 / 2 / 0 / 2 | 保留 `ent/batchimageevent`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/batchimageitem | 2 / 2 / 0 / 2 | 保留 `ent/batchimageitem`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/batchimagejob | 2 / 2 / 0 / 2 | 保留 `ent/batchimagejob`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/creativerun | 2 / 2 / 0 / 2 | 保留 `ent/creativerun`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/creativerunoutbox | 2 / 2 / 0 / 2 | 保留 `ent/creativerunoutbox`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/creativerunoutput | 2 / 2 / 0 / 2 | 保留 `ent/creativerunoutput`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/enttest | 1 / 1 / 0 / 1 | 保留 `ent/enttest`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/errorpassthroughrule | 2 / 2 / 0 / 2 | 保留 `ent/errorpassthroughrule`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/group | 2 / 2 / 0 / 2 | 保留 `ent/group`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/hook | 1 / 1 / 0 / 1 | 保留 `ent/hook`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/idempotencyrecord | 2 / 2 / 0 / 2 | 保留 `ent/idempotencyrecord`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/identityadoptiondecision | 2 / 2 / 0 / 2 | 保留 `ent/identityadoptiondecision`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/intercept | 1 / 1 / 0 / 1 | 保留 `ent/intercept`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/migrate | 3 / 2 / 1 / 2 | 保留 `ent/migrate`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/paymentauditlog | 2 / 2 / 0 / 2 | 保留 `ent/paymentauditlog`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/paymentorder | 2 / 2 / 0 / 2 | 保留 `ent/paymentorder`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/paymentproviderinstance | 2 / 2 / 0 / 2 | 保留 `ent/paymentproviderinstance`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/pendingauthsession | 2 / 2 / 0 / 2 | 保留 `ent/pendingauthsession`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/predicate | 1 / 1 / 0 / 1 | 保留 `ent/predicate`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/promocode | 2 / 2 / 0 / 2 | 保留 `ent/promocode`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/promocodeusage | 2 / 2 / 0 / 2 | 保留 `ent/promocodeusage`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/proxy | 2 / 2 / 0 / 2 | 保留 `ent/proxy`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/redeemcode | 2 / 2 / 0 / 2 | 保留 `ent/redeemcode`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/redeemcodeusage | 2 / 2 / 0 / 2 | 保留 `ent/redeemcodeusage`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/runtime | 1 / 1 / 0 / 1 | 保留 `ent/runtime`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/schema | 46 / 45 / 1 / 0 | 保留 `ent/schema`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| ent/schema/mixins | 2 / 2 / 0 / 0 | 保留 `ent/schema/mixins`；手写 schema/生成入口按现有流程维护 | S16 核验 |
| ent/securitysecret | 2 / 2 / 0 / 2 | 保留 `ent/securitysecret`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/setting | 2 / 2 / 0 / 2 | 保留 `ent/setting`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/subscriptionplan | 2 / 2 / 0 / 2 | 保留 `ent/subscriptionplan`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/team | 2 / 2 / 0 / 2 | 保留 `ent/team`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/teaminvitation | 2 / 2 / 0 / 2 | 保留 `ent/teaminvitation`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/teammembership | 2 / 2 / 0 / 2 | 保留 `ent/teammembership`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/teamownershiptransfer | 2 / 2 / 0 / 2 | 保留 `ent/teamownershiptransfer`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/tlsfingerprintprofile | 2 / 2 / 0 / 2 | 保留 `ent/tlsfingerprintprofile`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/tlsfingerprintrouter | 2 / 2 / 0 / 2 | 保留 `ent/tlsfingerprintrouter`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/usagecleanuptask | 2 / 2 / 0 / 2 | 保留 `ent/usagecleanuptask`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/usagelog | 2 / 2 / 0 / 2 | 保留 `ent/usagelog`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/user | 2 / 2 / 0 / 2 | 保留 `ent/user`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/userallowedgroup | 2 / 2 / 0 / 2 | 保留 `ent/userallowedgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/userattributedefinition | 2 / 2 / 0 / 2 | 保留 `ent/userattributedefinition`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/userattributevalue | 2 / 2 / 0 / 2 | 保留 `ent/userattributevalue`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/userdisabledpublicgroup | 2 / 2 / 0 / 2 | 保留 `ent/userdisabledpublicgroup`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/userplatformquota | 2 / 2 / 0 / 2 | 保留 `ent/userplatformquota`；生成/辅助包按现有流程维护 | S16 核验 |
| ent/usersubscription | 2 / 2 / 0 / 2 | 保留 `ent/usersubscription`；生成/辅助包按现有流程维护 | S16 核验 |
| internal/config | 8 / 3 / 5 / 0 | 保留；Wire 组合移 app，各模块接受所需 Options | S02、S15 |
| internal/domain | 13 / 10 / 3 / 0 | 拆至 protocol、routing/capability、routing、scheduler/policy、identity、billing、site；端点展示元数据由 HTTP Adapter 组织；逐文件见 5.4，最终删除 | S02.4、S03—S07、S11、S16 |
| internal/handler | 164 / 67 / 97 / 0 | 各模块/httpapi；网关编排移 gateway 核心，首条链在 S09.1 接入；通用输入输出移 server/httpx；最终删除 | S04—S15 |
| internal/handler/admin | 132 / 61 / 71 / 0 | 管理员方法归各模块/httpapi/admin_*.go；Dashboard → usage、诊断 → ops；最终删除 | S04—S15 |
| internal/handler/dto | 14 / 7 / 7 / 0 | 各模块/httpapi 的 DTO；协议 DTO → protocol；凭据投影由 account 生成；最终删除 | S03—S15 |
| internal/handler/quotaview | 2 / 1 / 1 / 0 | 限额展示规则 → billing 的只读投影；JSON 输出 → billing/httpapi；最终删除 | S04 |
| internal/integration | 3 / 0 / 3 / 0 | backend/tests/integration；保留 unit/integration/e2e 标签，同步 Makefile/脚本/CI 路径 | S00 登记、S16 迁移 |
| internal/middleware | 3 / 1 / 2 / 0 | 限流用例/故障策略 → server/middleware；通用 Redis 固定窗口实现 → infra/redis；最终删除 | S01、S15 |
| internal/model | 3 / 3 / 0 / 0 | 错误规则 → internal/gateway；TLS Profile/Router 管理模型 → internal/egress；最终删除 | S06、S11 |
| internal/payment | 14 / 8 / 6 / 0 | 保留路径并按支付模块重构；合入旧 service 的支付用例，金额/手续费保持支付口径；数据库负载选择拆 Adapter，Wire → app | S12 |
| internal/payment/provider | 14 / 6 / 8 / 0 | 保留路径、各外部提供商和合同测试，收紧与支付核心的接口 | S12 |
| internal/pkg/anthropicfp | 2 / 1 / 1 / 0 | 合入 upstream/anthropic 的请求规范化文件，必要时才保留专用子包 | S09.2 |
| internal/pkg/antigravity | 13 / 8 / 5 / 0 | upstream/antigravity；通用协议 DTO/纯转换抽至 protocol，专有封装留平台 | S03、S09.5 |
| internal/pkg/apicompat | 49 / 15 / 34 / 0 | protocol/anthropic、protocol/openai、protocol/bridge；不复制转换实现 | S03、S11 |
| internal/pkg/claude | 4 / 2 / 2 / 0 | upstream/anthropic；纯 wire 常量 → protocol/anthropic，入站版本识别 → gateway/clientmeta | S03、S09.2、S11 |
| internal/pkg/ctxkey | 1 / 1 / 0 / 0 | 按身份、网关执行参数和 infra/telemetry 拆分；显式投影替代业务键，最终删除 | S01、S05、S07、S09.1、S11 |
| internal/pkg/errors | 4 / 3 / 1 / 0 | pkg/apperror；HTTPCode/响应映射 → server/httpx 和各协议 Adapter，兼容期保留旧错误比较 | S01、S15、S16 |
| internal/pkg/gemini | 2 / 1 / 1 / 0 | 协议类型 → protocol/gemini；默认模型与能力资料 → upstream/gemini，展示由 routing 组装 | S03、S09.3 |
| internal/pkg/geminicli | 10 / 7 / 3 / 0 | upstream/gemini/codeassist；与 Gemini/Vertex 共用部分提取为无反向引用的低层能力 | S09.3 |
| internal/pkg/googleapi | 3 / 2 / 1 / 0 | Google 错误报文 → protocol/google；激活诊断 → upstream/gemini/codeassist | S03、S09.3 |
| internal/pkg/httpclient | 2 / 1 / 1 / 0 | infra/httpclient；统一普通/指纹客户端的策略入口，保留池隔离 | S01、S06 |
| internal/pkg/httputil | 3 / 1 / 2 / 0 | 读取/解压 → server/httpx；JSON 宽容修复 → protocol 的适用转换辅助文件 | S01、S03 |
| internal/pkg/ip | 2 / 1 / 1 / 0 | Gin 客户端地址 → server/clientip；纯 IP/CIDR → pkg/ipmatch；黑白名单裁决 → apikey | S01、S05 |
| internal/pkg/logger | 8 / 4 / 4 / 0 | infra/telemetry/logging；config_adapter → app 的配置转换 | S01、S02 |
| internal/pkg/oauth | 2 / 1 / 1 / 0 | Claude OAuth → upstream/anthropic；重复 PKCE 原语 → pkg/oauthpkce；会话持有由账号授权用例负责 | S01、S09.2 |
| internal/pkg/openai | 10 / 4 / 6 / 0 | 授权/默认调用 → upstream/openai；入站客户端识别 → gateway/clientmeta；许可裁决 → gateway/routing | S03、S09.8、S11 |
| internal/pkg/openai_compat | 2 / 1 / 1 / 0 | 旧账号字段转换 → account；选路 → routing；协议枚举 → protocol/openai；按当前统一协议契约删除旧配置依赖 | S03、S06 |
| internal/pkg/pagination | 2 / 1 / 1 / 0 | 保留 pkg/pagination；保持不依赖任何业务或 HTTP 框架 | S01 |
| internal/pkg/proxyurl | 2 / 1 / 1 / 0 | infra/httpclient/proxy，与 proxyutil 合并组织 | S01 |
| internal/pkg/proxyutil | 3 / 1 / 2 / 0 | infra/httpclient/proxy，与 proxyurl 合并组织 | S01 |
| internal/pkg/qoder | 16 / 10 / 6 / 0 | upstream/qoder；站点模型/认证/签名/原生流保留归属；本地凭据读取明确隔离 | S09.1 |
| internal/pkg/redissession | 2 / 1 / 1 / 0 | infra/redis/session；配置与使用语义由授权用例提供 | S01、S09 |
| internal/pkg/response | 2 / 1 / 1 / 0 | server/httpx；保持面板响应兼容和错误脱敏 | S01、S15 |
| internal/pkg/servertiming | 6 / 3 / 3 / 0 | infra/telemetry/timing；HTTP 输出控制留 server/middleware | S01 |
| internal/pkg/sysutil | 1 / 1 / 0 / 0 | app/lifecycle；调用方持有重启请求 Interface，不导入 app 或直接 os.Exit | S02、S14 |
| internal/pkg/timezone | 2 / 1 / 1 / 0 | 保留 pkg/timezone 日期运算；全局初始化 → app；逐步注入时钟/时区但保持结算日界 | S01、S04、S08 |
| internal/pkg/tlsfingerprint | 11 / 4 / 7 / 0 | infra/httpclient/tlsfingerprint；业务 Profile/Router 管理和选择仍归 egress | S01、S06 |
| internal/pkg/usagestats | 3 / 2 / 1 / 0 | internal/usage 查询类型和统计口径；计费金额输入使用 billing/pricing 的专用值类型 | S04、S08 |
| internal/pkg/websearch | 10 / 6 / 4 / 0 | internal/search 及其 provider、rediscache 子包；额度/选择与外部调用分开 | S10 |
| internal/pkg/xai | 17 / 8 / 9 / 0 | upstream/grok；外部账单/订阅解析保留；Redis 适配由外层注入 | S09.7 |
| internal/platform/liveattestation | 4 / 3 / 1 / 0 | internal/upstream/openai/liveattestation；保留 OS 构建标签与不支持平台结果 | S09.8 |
| internal/repository | 331 / 128 / 203 / 0 | 各模块/postgres、rediscache、provider；通用技术 → infra；完整规则见 5.2，最终删除 | S01—S16 |
| internal/server | 6 / 4 / 2 / 0 | 保留 HTTP 装配与静态资源接入；业务实例构造和副作用 → app/各模块 | S02、S15 |
| internal/server/middleware | 49 / 27 / 22 / 0 | 通用部分保留；JWT/step-up → identity/httpapi，Key → apikey/httpapi，审计 → audit/httpapi；见 5.3 | S01、S05、S08、S15 |
| internal/server/routes | 17 / 7 / 10 / 0 | 各模块/httpapi/routes.go；全局挂载和别名汇总 → internal/server，最终删除 routes 包 | S03—S15 |
| internal/service | 1250 / 536 / 714 / 0 | 按 5.1 职责组拆至 internal 下各业务模块及 upstream、protocol、infra、app；最终删除 | S02—S16 |
| internal/service/openai_ws_v2 | 6 / 4 / 2 / 0 | internal/upstream/openai/wsrelay；保留帧转发与取消语义 | S09.8 |
| internal/setup | 4 / 3 / 1 / 0 | 保留独立 setup 入口；配置/数据库初始化调用 app 提供的精简初始化能力 | S14 |
| internal/testutil | 4 / 4 / 0 / 0 | 通用设施保留；业务 fixture/stub → 各模块/testkit 或模块 *_test.go | 随 S03—S15、S16 |
| internal/util/httputil | 1 / 1 / 0 / 0 | 上游响应/Cloudflare 诊断 → internal/upstream 的响应辅助实现；通用截断按实际复用收敛 | S09 |
| internal/util/logredact | 2 / 1 / 1 / 0 | internal/pkg/logredact；保留所有敏感值清理契约 | S01 |
| internal/util/responseheaders | 2 / 1 / 1 / 0 | 过滤配置与编译规则 → internal/egress；实际应用在 HTTP/上游 Adapter，最终删除旧包 | S06、S11 |
| internal/util/urlvalidator | 2 / 1 / 1 / 0 | 目标策略校验 → internal/egress；DNS/重定向实际执行 → infra/httpclient；最终删除旧包 | S01、S06、S09 |
| internal/web | 6 / 4 / 2 / 0 | 保留；不反向导入业务 service，公开设置/CSP 使用注入的投影 | S02、S15 |
| migrations | 32 / 1 / 31 / 0 | 保留 SQL embed 与部署迁移；runner 从 repository 移 infra/postgres | S02、S14、S16 |

## 前缀表未直接覆盖的八个 service 文件

已按声明与调用用途核实，属于现有目标的细化，不改变阶段依赖。

| 文件 | 归属 | 阶段 | 拆分依据 |
| --- | --- | --- | --- |
| admin_service.go | identity、team、apikey、routing、account、egress、billing；聚合装配归 app | S04、S05、S06、S16 | AdminService 输入/接口及共享接收者按用户、分组、账号、代理、兑换操作拆分；签名归 S04—S06 子计划。 |
| anthropic_apikey_auth.go | account、upstream/anthropic | S06、S09.2 | Account.GetAnthropicAPIKeyAuthScheme 归账号配置；setAnthropicAPIKeyAuthHeader 归上游请求。 |
| claude_code_validator.go | gateway/clientmeta、gateway | S03、S11 | 客户端特征解析归 clientmeta；准入判定留 gateway。 |
| domain_constants.go | identity、apikey、routing/capability、protocol、billing、promotion、account、upstream/kimi、upstream/zhipu、upstream/deepseek | S03—S06、S09.6、S12、S16 | 按角色、Key上限、平台/协议、额度、返利与供应商URL拆分常量。 |
| gateway_model_availability.go | routing、gateway | S06、S11 | 持久模型可用性诊断归 routing；错误与 simple 模式入口归 gateway。 |
| oauth_service.go | account、upstream/anthropic、upstream/openai、upstream/grok | S06、S09.2、S09.7、S09.8 | OAuthService 是 Claude 账号授权；同文件 OpenAI/Grok 端口声明分别归相应消费者。 |
| quota_fetcher.go | account、upstream | S06、S09 | QuotaFetcher 是上游账号额度观测端口，不是用户资金扣款。 |
| shadow_routing.go | scheduler、account、upstream/openai | S06、S07、S09.8 | parentHealthyForShadow 归候选资格；sparkModelVariants 归 OpenAI 目录；defaultSparkShadowModelMapping 归账号配置。 |

## 增量与复核

当前 112 个 Go 目录（Ent 54 / 其他 58）、2666 个 Go 文件，与总计划一致；service 536、repository 128、handler 67、handler/admin 61 个非测试文件。以本次工作区快照和逐文件 SHA256 比较后续增量。

后续阶段仍按子计划把混合文件拆到实际新文件，本清单不提前确定尚未设计的新接口签名。
