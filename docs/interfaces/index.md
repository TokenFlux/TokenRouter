# 接口文档目录

> 上级目录：[工程文档](../index.md)

## 范围

本分类拥有对外 HTTP 表面、配置来源以及第三方上游适配契约。内部领域不变量由领域文档拥有，部署和维护步骤由运维文档拥有。

## 文档

- [HTTP 接口边界](http_api.md)：公共、用户、管理员、支付和网关路由族及认证/错误边界。读取时机：新增或移动路由、调整中间件、认证方式或公共响应语义时读取。
- [配置边界](configuration.md)：默认值、YAML、环境变量、数据库运行时设置和首次初始化之间的边界。读取时机：新增配置项、修改加载优先级、设置页面或部署变量时读取。
- [tf CLI 网页导入](tf_cli_web_import.md)：Keys 页、本机回环协议、会话证明、双重确认和浏览器安全头。读取时机：修改 Keys 导入入口、URL fragment、localhost fetch、CSP 或 tf-cli 协议时读取。
- [上游账号能力矩阵](upstream_account_matrix.md)：九个平台、七类账号和全部公开网关协议的正式支持、兼容保留与不支持边界。读取时机：新增平台/账号类型、修改创建导入校验、路由分派或能力承诺时读取。
- [API Key 上游用量查询](upstream_usage.md)：API Key 账号的适配器、管理员查询接口、归一化结果和浏览器缓存边界。读取时机：修改 API Key 用量查询、适配器协议、账号用量展示或查询安全策略时读取。
- [Anthropic 上游](anthropic_upstream.md)：OAuth、Setup Token、API Key、Bedrock、Vertex，以及 Messages/OpenAI 兼容转换和缓存/限流契约。读取时机：修改 Anthropic 认证、协议、beta、thinking、缓存或错误分类时读取。
- [OpenAI 上游](openai_upstream.md)：OAuth/API Key、Responses、Chat、Messages、Embeddings、Images、Realtime 和 Codex 传输契约。读取时机：修改 OpenAI 认证、endpoint capability、WebSocket、模型或配额调度时读取。
- [Gemini 上游](gemini_upstream.md)：OAuth 变体、API Key、Vertex Service Account、v1beta 原生和兼容协议契约。读取时机：修改 Gemini 认证、project/tier、协议转换、thought signature 或配额时读取。
- [Antigravity 上游](antigravity_upstream.md)：Antigravity 专用端点、混合调度及模型协议边界。读取时机：修改 Antigravity 账号、OAuth、Claude/Gemini 转换或调度隔离时读取。
- [Grok / xAI 上游](grok_upstream.md)：Grok OAuth/API Key、媒体资格与 OpenAI 兼容转发契约。读取时机：修改 Grok 登录、聊天、图片、视频、计费探测或模型配置时读取。
- [Qoder 原生上游](qoder_upstream.md)：Qoder 站点、模型别名、思考能力、上下文、计费和刷新契约。读取时机：修改 Qoder 账号、模型能力、请求转换、定价或运维探测时读取。
- [网关错误响应策略](gateway_error_policy.md)：最终错误规则的优先级、匹配、状态/消息改写、监控跳过和缓存一致性。读取时机：修改错误透传规则、平台错误封装或 Ops 跳过语义时读取。
- [模型目录与市场](model_catalog_and_marketplace.md)：可请求模型解析、公开分组可见性、品牌、渠道定价、容量和未知价格。读取时机：修改模型列表、市场接口、显示价格、品牌或可用性探测时读取。
