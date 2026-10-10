# Jev 与 SystemOne

本文记录 Jev 提供商的认证、决策请求、模型同步和计费。分组权限见[统一协议能力](protocol_capabilities.md)，价格解析见[模型目录与市场](model_catalog_and_marketplace.md)。

<a id="systemone_execution"></a>
## 请求执行

提供商使用 `platform=jev`、`type=apikey`，品牌标识为 `typesafe`。`credentials.api_key` 保存 TypeSafe 或兼容上游的 API Key，`base_url` 缺省为 `https://api.typesafe.ai`。请求使用 Bearer 认证，地址校验、代理和请求头覆写由共享传输组件处理。

客户端入口为 `POST /v1/systemone`。Jev 提供商默认启用 `systemone`，管理员需要在分组中开放这个入口。空的提供商协议集合关闭新请求。SystemOne 支持同步 JSON 请求，问题类型为 `noul`、`choice`、`score`；`stream=true` 返回 400。

请求需要 `model`、`state` 和非空的 `questions`。state 和问题 instructions 接受字符串、对象或数组。请求按照复合 Key、Key 重定向、分组映射、提供商映射的顺序解析模型，替换顶层 model 后发送。问题和扩展字段随请求转发。内容审核启用时，state 和问题描述进入现有文本审核流程。

`protocol/systemone` 负责结构校验和模型字段修改，`upstream/jev` 执行一次 HTTP 请求。`gateway/systemone` 管理换号和完成资格，HTTP 适配器绑定调度、并发槽、资金准入和完成记录器。每次换号重新验证协议、模型及价格。RPM 在首次准入时累计一次，等待后和换号前的资金复查跳过 RPM 累计。成功答案在客户端写入失败后仍可以提交有效用量。

app 通过 `provideSystemOneExecutor` 构造决策执行器，直接注入共享 HTTP 传输、地址策略、响应头过滤、提供商健康处理和请求活动屏障。价格预检是必需依赖。媒体运行时接收已构造的执行器，绑定 `Forward` 方法。SystemOne 单独识别复合 Key 入口，网关错误由 `WriteSystemOneError` 输出。

422 参数错误直接交付。其他错误先应用提供商策略，认证失败、限流及可恢复的服务错误进入有限换号流程。429 和 529 的 Retry-After 参与提供商冷却，重试耗尽时传给客户端。客户端取消、响应已交付或成功响应格式损坏时结束重试。

<a id="systemone_usage"></a>
## 用量与价格

响应中的 `usage.input_tokens` 和 `usage.output_tokens` 分别记录。TypeSafe 官方输出零价通过价卡配置，输出 token 仍保存在使用记录里。计费模型来源、用户倍率、订阅及提供商成本使用现有规则。

成功响应的用量缺失、为负、类型错误或超出整数范围时，答案照常交付。网关记录 `systemone.usage_invalid` 告警和关联请求的 `usage_unknown` 尝试，跳过资金结算及正常用量账单。有效的零用量按正常响应处理。该响应结束本次上游尝试。

models.dev 的默认 JSON 接口会过滤 decision 等特殊模型。目录同步使用 `catalog.json?type=all`，离线快照包含 Jev 属性及各渠道报价。`model_supplements.json` 在同一型号条目内保存 TypeSafe 直连的价格、缺失属性和核验来源，覆盖 `jev-latest`、`jev-preview`、`jev-1.13.0`。目录已有值优先，渠道价和免费价各自按完整 ID 查询。输入模态为文本，输出为结构化决策，在当前模态分类中标为 text 并声明 structured_output。

## 管理与展示

上游模型同步请求 `GET /v1/models`，解析 `models[].name`。同步结果合并进表单，由管理员保存为白名单；新建提供商的白名单为空。公共模型列表使用 TokenRouter 的统一格式，并声明每个模型可以请求的协议。

手动测试、定时测试和分组探测共用 Jev 执行器。手动测试默认填入 `jev-latest`，管理员可输入其他型号，填写文本或 JSON 状态，并添加 Noul、Choice、Score 问题。单模型和批量测试使用同一份问题，界面分别展示概率、选择、评分、置信度和 token 用量。无效或缺失用量显示为未知。定时测试和分组探测使用最小 Noul 请求检查连通性。

决策测试与文字、图片测试共用弹窗布局和结果面板，指标展示实际模型、总耗时及输入／输出 token。结果、JSON 和日志通过面板页签切换，复制按钮复制当前 JSON 或日志。问题 ID 和额外判断条件在左栏折叠编辑。

管理员测试接口以 `test_type=decision` 和 `systemone={state, questions}` 传入表单数据，选定的 `model_id` 经提供商映射后覆盖请求模型。校验失败时结束测试。TypeSafe 的公开 API 文档和官方 SDK 没有账户余额查询接口（核验日期：2026-10-11），Jev 的上游余额查询默认关闭，兼容中继可以配置已支持的查询适配器。

TypeSafe 品牌用于分组品牌选项、模型图标、市场筛选和 SystemOne 协议展示。Jev 提供商使用同一图标资源。分组的展示品牌优先取 display_brand，为空时使用分组名；执行平台取提供商配置。

参考：[TypeSafe API](https://docs.typesafe.ai/api)、[TypeSafe SDK](https://github.com/typesafe-ai/typesafe-sdk-js/blob/main/src/client.ts)、[官方模型与价格](https://docs.typesafe.ai/models)、[models.dev Jev](https://models.dev/models/typesafe/jev-latest/)。本地图标取自 TypeSafe 官方文档站 favicon。
