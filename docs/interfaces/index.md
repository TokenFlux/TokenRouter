# 接口文档目录

> 上级目录：[工程文档](../index.md)

## 范围

本分类记录对外的 HTTP 接口、配置来源，以及第三方上游的适配规则。内部的领域不变量见领域文档，部署和维护步骤见运维文档。

## 文档

- [HTTP 接口](http_api.md)：公共、用户、管理员、支付和网关路由族，认证方式、错误格式和已移除的接口。读取时机：新增或移动路由、调整中间件、认证方式或公共响应格式时读取。
- [配置](configuration.md)：默认值、YAML、环境变量、数据库运行时设置和首次初始化的分工和优先级。读取时机：新增配置项、修改加载优先级、设置页面或部署变量时读取。
- [tf CLI 网页导入](tf_cli_web_import.md)：Keys 页、本机回环协议、会话证明、两次确认和浏览器安全头。读取时机：修改 Keys 导入入口、URL fragment、localhost fetch、CSP 或 tf-cli 协议时读取。
- [统一协议能力](protocol_capabilities.md)：25 项协议目录、提供商的原生协议集合、分组准入和单步转换、图片策略和兼容的旧字段输入。读取时机：修改协议选项、转换路线、公共入口门禁或统一配置时读取。
- [上游提供商能力矩阵](upstream_provider_matrix.md)：十个平台、七类提供商和全部公开网关协议的支持情况：正式支持、兼容保留和不支持。读取时机：新增平台或提供商类型、修改创建和导入校验、路由分派或能力承诺时读取。
- [API Key 上游用量查询](upstream_usage.md)：API Key 提供商的适配器、管理员查询接口、归一化结果、浏览器缓存和国产供应商的周期监控。读取时机：修改 API Key 用量查询、适配器协议、提供商用量展示或查询安全策略时读取。
- [Anthropic 上游](anthropic_upstream.md)：OAuth、Setup Token、API Key、Bedrock 的模型和区域路由、Vertex，以及 Messages 和 OpenAI 兼容转换、缓存和限流。读取时机：修改 Anthropic 认证、协议、模型区域、beta、thinking、缓存或错误分类时读取。
- [OpenAI 上游](openai_upstream.md)：OAuth 和 API Key、Responses、Chat、Messages、Embeddings、Images、Realtime 和 Codex 的传输规则。读取时机：修改 OpenAI 认证、endpoint capability、WebSocket、模型或配额调度时读取。
- [Gemini 上游](gemini_upstream.md)：OAuth 变体、API Key、Vertex Service Account、v1beta 原生和兼容协议。读取时机：修改 Gemini 认证、project 和 tier、协议转换、thought signature 或配额时读取。
- [Antigravity 上游](antigravity_upstream.md)：Antigravity 的专用端点、跨平台分组选号，以及 Claude 和 Gemini 模型的协议适配。读取时机：修改 Antigravity 提供商、OAuth、Claude 和 Gemini 转换或强制平台过滤时读取。
- [Grok / xAI 上游](grok_upstream.md)：Grok OAuth 和 API Key、媒体资格和 OpenAI 兼容转发。读取时机：修改 Grok 登录、聊天、图片、视频、计费探测或模型配置时读取。
- [Qoder 原生上游](qoder_upstream.md)：Qoder 站点、模型别名、思考能力、上下文、计费和刷新。读取时机：修改 Qoder 提供商、模型能力、请求转换、定价或运维探测时读取。
- [Jev 与 SystemOne](jev_upstream.md)：Jev API Key、SystemOne 决策请求、用量异常、模型同步和 TypeSafe 品牌。读取时机：修改 Jev、SystemOne、探测或决策用量处理时读取。
- [网关错误响应策略](gateway_error_policy.md)：最终错误规则的优先级、匹配、状态码和消息的改写、跳过监控和缓存一致性。读取时机：修改错误透传规则、平台错误封装或 Ops 跳过规则时读取。
- [模型目录与市场](model_catalog_and_marketplace.md)：可请求模型的解析、models.dev 统一目录和价格换算、分组属性档案和配置导出、公开分组的可见性、共享定价、容量和未知价格。读取时机：修改模型列表、目录查表、属性管理、客户端配置导出、市场接口、显示价格或可用性探测时读取。
- [用户侧国际化](user_localization.md)：语言目录、用户偏好和运营内容的译文版本。读取时机：修改用户可见内容、语言选择或翻译编辑时读取。
