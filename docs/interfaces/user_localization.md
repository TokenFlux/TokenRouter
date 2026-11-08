# 用户侧国际化

本文记录访客、登录用户和团队所有者所见内容的语言选择、编辑与发布规则。运营文案由管理员维护，用户属性和管理员内部资料按原文保存。设置提交流程见[配置](configuration.md)，升级步骤见[版本升级说明](../operations/upgrade_notes.md#user_localization_migration)。

<a id="language_selection"></a>
## 语言选择

`backend/internal/pkg/locale/manifest.json` 是 Go 和前端共用的语言目录，登记语言代码、名称、文字方向、别名、兼容语言和前端资源目录。生产语言为 `en` 和 `zh-Hans`，`zh`、`zh-CN` 和 `zh-SG` 映射到 `zh-Hans`。`Accept-Language` 按权重匹配，权重为零的条目跳过。站点 `default_locale` 初始为 `en`。

匿名浏览器依次使用本地保存的选择、语言 Cookie、浏览器语言和站点默认语言。登录后读取 `User.preferred_locale`；账户尚无选择时，把当前语言写入账户。主动切换同时更新界面、本地存储、`tokenrouter_locale` Cookie 和账户偏好。日期时区和支付币种由各自配置决定。

网站 HTTP 请求携带 `Accept-Language`，HTML 首屏优先读取语言 Cookie。中间件把规范语言放入请求上下文，响应提供 `Content-Language`，并通过 `Vary: Accept-Language` 区分语言缓存。网站前后端共用语言规范化规则。网关内置错误固定使用英文，HTTP、SSE 和 WebSocket 使用各自协议的错误类型。支付、验证码等第三方控件通过 `vendorLocale` 使用各供应商支持的代码。

邮件依次采用事件指定语言、收件人账户偏好、历史用户偏好、邮箱偏好和站点默认语言。订单结果通知使用下单快照中的语言。订阅到期提醒在发送时按收件人偏好选择套餐名称。

<a id="localized_content"></a>
## 内容和译文

`locale.Content[T]` 保存 `source_locale`、业务类型明确的 `source`、`translations`、`revision` 和 `source_revision`。`source_locale=null` 表示历史原文的语言未知。译文包含 `value` 和已经核对的 `source_revision`。独立短文本各自保存，公告、协议和套餐的标题、正文或权益组成完整内容块。

展示按目标语言、目录中的兼容语言、原文依次选择。只有核对版本等于当前原文版本的译文参与展示。可选字段允许保存空值，站点设置另外区分“尚未配置”和“已保存为空”。必填标题和名称由所属业务校验。迁移时，历史标题和副标题的原文与译文均为空白时跳过导入，页面使用内置文案。保存后的空内容块表示有意留空，公开接口和首屏注入按已配置处理。

编辑请求携带读取时的 `revision`。原文或其语言变化推进 `source_revision`，`reviewed_locales` 指定本次已经核对的译文。服务端计算版本，客户端填写译文版本不能代替核对操作。省略内容块或译文表示保持现值；删除译文使用 `deleted_locales`。首次修改未知语言的原文时需要选择语言。套餐文案更新通过 `localization` 提交，单独更新旧的名称、介绍、权益或商品名字段返回 `LOCALIZATION_REQUIRED`。

持久化层执行原子版本检查，冲突返回 HTTP 409 和 `LOCALIZATION_CONFLICT`。JSONB 内容随业务对象保存；settings 内容的检查与整个保存批次位于同一数据库事务。某项冲突时，同批其他配置也不会写入。列表使用稳定 ID 对应译文，排序位置不能替代 ID。前端生成的 ID 最长 32 字符，与菜单保存接口的限制一致。

管理表单里，`LocalizedEditor.vue`（单个字段）和 `LocalizedFieldsEditor.vue`（一组字段共用一份内容）只编辑原文。标签行右侧的入口显示译文状态，点开后在 `LocalizedTranslationDialog.vue` 里编辑各语言译文。弹窗左栏列出原文和语言目录里的每种语言及其状态，右栏编辑选中的一项，编辑译文时上方显示原文参照。弹窗编辑草稿，点“完成”写回表单，再随页面的保存按钮提交。编辑或新增译文时，前端把该语言写进 `reviewed_locales`。原文修改后，旧译文标为过期，管理员可以改写译文，也可以点“仍然适用”确认。原文语言未知时，修改原文会填入当前界面语言；该语言已有译文时，管理员需要在弹窗里选择原文语言。选中的语言已有译文时，弹窗让管理员决定保留当前原文（删除那条译文）还是改用那条译文作为原文。给未知语言的原文补上语言时，原文内容不变，之前有效的译文继续有效。草稿操作集中在 `frontend/src/i18n/contentEdit.ts`。译文由编辑者提供。余额单位和商品标题预览按当前界面语言读取草稿，刚核对的译文立即参与预览。

<a id="content_owners"></a>
## 内容归属和展示入口

| 内容 | 存储和管理入口 | 用户展示入口 |
| --- | --- | --- |
| 站点名称、标题、副标题、分享标题及简介、联系说明、文档及购买链接、首页、页脚文字 | `site_texts`；综合设置 | 首页、认证页、公共设置、HTML 注入 |
| 菜单、页脚分组及链接、自定义接口名称和说明 | 各设置 JSON 中的稳定条目及 `localization`；综合设置 | 导航、页脚、Key 使用说明 |
| 协议标题及正文 | `login_agreement_documents`；综合设置 | 公开协议页、登录和协议确认弹窗 |
| Markdown 页面及图片 | `pages/` 目录；文件维护 | 用户自定义页面 |
| 公告标题及正文 | `announcements.localization`；公告管理 | 公告列表、弹窗 |
| 分组展示名称和描述 | `groups.localization`；分组管理 | 分组选择器、市场、Key、用量、订阅适用分组、创作台 |
| 模型自定义显示名 | 属性规则的 `display_name_localization`；模型属性管理 | 模型目录、市场、用户配置导出 |
| 套餐名称、介绍、权益、商品名 | `subscription_plans.localization`；套餐编辑 | 购买、订阅、Key 计费选择、订单快照 |
| 余额单位、充值链接、OIDC 名称、支付帮助及图片、商品名前后缀、发件人名称 | `localized_settings` 管理对象，各原文键对应的 `_localized` JSON | 登录、余额、支付、用户邮件 |
| 易支付自定义方式名称 | 支付实例 `customMethods` 条目；支付方式编辑 | 收银台 |

错误规则、审核阻断和 Beta/Fast 提示使用单文本配置，管理员填写英文提示。上游错误透传按供应商原文返回。

业务分组名称、模型 ID、匹配条件、用户填写的名称和提示词按原值执行。用户展示接口选择语言后返回普通字段；管理编辑接口返回完整内容及版本。模型市场和分组选择器通过 `search_terms` 匹配原文及已核对译文，过期译文不进入搜索语料。

前后端内部日志、技术异常、后端错误消息和提供商探测输出使用英文。面向用户的表单校验使用界面译文，面板错误展示继续按 `reason` 和用户语言选择译文。第三方原始错误、管理员自定义错误文本和审计证据按原文保留，中文匹配词继续用于上游错误识别和历史数据兼容。

系统日志页的加载、保存和回滚失败提示从界面语言目录读取，控制台诊断保持英文。Codex 邀请状态和操作错误中的 `CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE` 使用前端已有译文；未知原因保留服务端说明，说明为空时使用当前语言的默认提示。

<a id="legal_pages_orders"></a>
## 协议、文件页面和订单

协议确认版本由原文内容和配置日期计算。译文编辑或切换显示语言保持同一确认版本；原文变更要求重新确认。`GET /api/v1/settings/legal/:id` 提供选定语言的协议，HTML 注入只包含协议摘要，正文由页面按需读取。

Markdown 原文为 `pages/<slug>.md`，译文为 `pages/<slug>/<locale>.md`。公开图片接口检查请求文件和符号链接目标的图片扩展名。正文响应中的实际语言决定图片目录；先找 `pages/<slug>/<locale>/<image>`，再找 `pages/<slug>/<image>`。管理员专用页面读取原文。JWT、菜单可见性、符号链接检查和 1 MiB 正文限制见[站点页面](http_api.md#site_pages)。

下单时保存套餐名称、商品名称和语言，以及渠道实际使用的商品描述。继续付款和历史订单读取下单快照，之后编辑运营译文不会更新已经存在的订单。公告语言版本共用公告 ID 和已读记录。

<a id="refresh_and_validation"></a>
## 缓存、切换和验证

原始内容缓存可由各语言共用，HTML 缓存按规范语言存储。ETag 绑定语言和渲染内容，`Vary` 包含 `Accept-Language` 与 Cookie。失效代次阻止迟到的渲染结果覆盖更新后的缓存。用户用量统计的语言展示缓存也包含语言和用户展示范围。

前端语言切换发布 `locale-changed`，页面刷新语言相关数据。公共设置缓存和首屏注入按返回的 `locale` 判断能否复用，账户语言在组件挂载前恢复时，后续读取也会刷新不匹配的配置。Axios 对语言已经改变的 GET 响应重新请求，Markdown 和协议页面还核对当前页面及请求代次。组件保持挂载，未提交表单由页面继续持有。账户偏好的写入按顺序执行。

验证入口包括 `locale/content_test.go`、站点本地化和文件测试、`migrations/user_localization_integration_test.go`、settings 并发写入集成测试、前端翻译编辑器测试、`i18n/__tests__/contentEdit.spec.ts` 及 `i18n/__tests__/userTranslationKeys.spec.ts`。新增语言时登记目录、添加资源和用户邮件模板，并检查长文案、文字方向、占位符与第三方控件回退。
