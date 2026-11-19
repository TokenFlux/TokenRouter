# 用户侧国际化

本文记录访客、登录用户和团队所有者所见内容的语言选择、多语言内容版本控制、归属入口及缓存规则。运营文案由管理员维护，用户属性与内部资料按原文保存；设置提交见[配置](configuration.md)，迁移步骤见[版本升级说明](../operations/upgrade_notes.md#user_localization_migration)。

## 章节导航

- [语言选择](#language_selection)：语言目录、浏览器/账户/邮件/网关各自的解析顺序。
- [内容和译文](#localized_content)：版本模型、核对语义、并发冲突与管理端编辑流程。
- [内容归属和展示入口](#content_owners)：各运营文案的存储位置与中英文边界。
- [协议、文件页面和订单](#legal_pages_orders)：协议确认版本、Markdown 文件路径与订单快照。
- [缓存、切换和验证](#refresh_and_validation)：缓存键、前端切换联动与测试入口。

<a id="language_selection"></a>
## 语言选择

`backend/internal/pkg/locale/manifest.json` 是前后端共享语言目录。当前生产语言为 `en`（默认 `default_locale`）和 `zh-Hans`（`zh`、`zh-CN`、`zh-SG` 均映射至 `zh-Hans`）；`Accept-Language` 按权重匹配，跳过权重为 0 的项。

| 场景 | 语言解析优先级 | 备注 |
| --- | --- | --- |
| 匿名浏览器 | 本地存储 > `tokenrouter_locale` Cookie > 浏览器语言 > 站点默认 | 主动切换同步更新界面、本地存储、Cookie 与账户偏好 |
| 已登录用户 | `User.preferred_locale`（空时回写当前浏览器语言） | 日期时区与支付币种独立配置 |
| 网站 HTTP / 首屏 HTML | API 看 `Accept-Language`，HTML 首屏优先读语言 Cookie | 响应带 `Content-Language` 与 `Vary: Accept-Language` |
| 用户邮件 | 事件指定 > 收件人账户偏好 > 历史用户偏好 > 邮箱偏好 > 站点默认 | 订单结果用下单快照语言，到期提醒按发送时收件人偏好选套餐名 |
| 网关内置错误 | 固定英文 | 第三方支付/验证码控件经 `vendorLocale` 映射各自代码 |

<a id="localized_content"></a>
## 内容和译文

### 数据模型与版本控制

- `locale.Content[T]` 保存 `source_locale`（`null` 表示历史原文语言未知）、`source`、`translations`（含 `value` 与已核对的 `source_revision`）、`revision` 和 `source_revision`。
- 展示优先级：目标语言 > 目录兼容语言 > 原文。只有核对版本等于当前 `source_revision` 的译文才会展示，过期译文不展示也不进入搜索语料。
- 空值语义：可选字段允许为空；站点设置区分「未配置」（用内置默认文案）与「已保存为空」（有意留空）。

### 编辑与并发冲突

- 提交编辑必须携带读取时的 `revision`；修改原文或原文语言会递增 `source_revision`，`reviewed_locales` 声明本次已核对的译文（由服务端计算版本），`deleted_locales` 删除译文，省略则保持现值。
- 首次编辑未知语言原文必须先选定 `source_locale`；套餐文案更新须通过 `localization` 提交，单独改旧名称/介绍/权益字段返回 `LOCALIZATION_REQUIRED`。
- 持久化执行原子版本检查，冲突返回 `409 LOCALIZATION_CONFLICT`（`settings` 批次位于同一事务，任一项冲突整批回滚）。列表条目须按稳定 ID（最长 32 字符）对齐译文，不依赖排序位置。
- 管理端交互：`LocalizedEditor.vue` 与 `LocalizedFieldsEditor.vue` 编辑原文，右侧入口打开 `LocalizedTranslationDialog.vue` 维护各语言草稿（草稿逻辑集中于 `frontend/src/i18n/contentEdit.ts`）。修改原文后旧译文标为过期，可改写或点「仍然适用」；给未知语言原文补选语言时，若该语言已有译文由管理员决定保留原文或改用译文。

<a id="content_owners"></a>
## 内容归属和展示入口

| 内容 | 存储与管理入口 | 用户展示入口 |
| --- | --- | --- |
| 站点名称、标题、副标题、分享文案、联系说明、文档/购买链接、首页、页脚文字 | `site_texts`；综合设置 | 首页、认证页、公共设置、HTML 注入 |
| 菜单、页脚分组及链接、自定义接口名称与说明 | 各设置 JSON 稳定条目及 `localization`；综合设置 | 导航、页脚、Key 使用说明 |
| 协议标题与正文 | `login_agreement_documents`；综合设置 | 公开协议页、登录与协议确认弹窗 |
| Markdown 页面与图片 | `pages/` 目录；文件维护 | 用户自定义页面 |
| 公告标题与正文 | `announcements.localization`；公告管理 | 公告列表、弹窗 |
| 分组展示名与描述 | `groups.localization`；分组管理 | 分组选择器、市场、Key、用量、订阅、创作台 |
| 模型自定义显示名 | 属性规则 `display_name_localization`；模型属性管理 | 模型目录、市场、用户配置导出 |
| 套餐名称、介绍、权益、商品名 | `subscription_plans.localization`；套餐编辑 | 购买、订阅、Key 计费选择、订单快照 |
| 余额单位、充值链接、OIDC 名称、支付帮助/图片、商品名前后缀、发件人名 | `localized_settings` 及各原文键对应 `_localized` JSON | 登录、余额、支付、用户邮件 |
| 易支付自定义方式名称 | 支付实例 `customMethods`；支付方式编辑 | 收银台 |

语言边界约定：

- 单文本配置：错误规则、审核阻断与 Beta/Fast 提示使用单文本英文；上游错误按原文透传。
- 执行标识符：分组业务名、模型 ID、匹配条件与用户自填内容按原值执行，不做翻译；模型市场与分组选择器通过 `search_terms` 匹配原文及已核对译文。
- 日志与错误展示：内部日志、技术异常、后端错误消息与探测输出保持英文；前端表单校验和面板错误按 `reason` 与用户语言取界面译文（如 `CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE`），中文匹配词仅用于上游错误识别和历史兼容。

<a id="legal_pages_orders"></a>
## 协议、文件页面和订单

- 协议确认版本：仅由原文内容与配置日期计算，改译文或切语言不触发重新确认，改原文才要求重新确认。`GET /api/v1/settings/legal/:id` 按需返回选定语言正文，HTML 注入仅含摘要。
- 文件页面路径：原文为 `pages/<slug>.md`，译文为 `pages/<slug>/<locale>.md`（管理员专用页读原文）。图片按正文实际语言优先查 `pages/<slug>/<locale>/<image>`，缺失回退 `pages/<slug>/<image>`；安全校验与 1 MiB 上限见[站点页面](http_api.md#site_pages)。
- 订单与公告：下单时固化套餐名、商品名、语言及渠道商品描述，后续改译文不影响历史订单；公告多语言共用公告 ID 与已读记录。

<a id="refresh_and_validation"></a>
## 缓存、切换和验证

- 服务端缓存：原始内容跨语言共享，HTML 缓存与用户用量展示缓存按规范语言区分；ETag 绑定语言与内容，`Vary` 包含 `Accept-Language` 和 Cookie，失效代次防迟到响应覆写新缓存。
- 前端联动：切语言派发 `locale-changed` 刷新数据；公共设置与首屏注入按 `locale` 判断复用；Axios 对语言已变更的 GET 响应自动重发，Markdown/协议页额外校验页面与请求代次，未提交表单保持挂载。
- 验证入口：`locale/content_test.go`、站点本地化测试、`migrations/user_localization_integration_test.go`、settings 并发集成测试及前端 `i18n/__tests__/contentEdit.spec.ts`、`userTranslationKeys.spec.ts`。新增语言时同步登记目录、资源和用户邮件模板。
