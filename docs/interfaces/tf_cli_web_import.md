# tf CLI 网页导入

本文只记录 TokenRouter 对 [tf-cli 网页导入协议](https://github.com/TokenFlux/tf-cli/blob/main/docs/integrations/web-import.md) 的接入责任。端口、字段、错误码和签名格式以上游文档为准。

TokenRouter 后端不接收导入请求，也不新增授权接口。Keys 页把当前用户已加载到内存的 API Key 直接发给本机 `tf`；Key 不进入 URL、浏览器持久化、日志或分析事件。

<a id="session_fragment"></a>
## 会话片段

`tf login --from-web` 打开 `/keys#tf=1.<port>.<base64url-secret>`。`frontend/src/router/index.ts` 在 Router 创建和登录守卫运行前调用 `initializeTfCliImportSession()`：

- 任何 `#tf=` fragment 都立即通过 `history.replaceState` 删除，畸形值也删除。
- 只接受 Keys 路径、`43110` 至 `43119` 端口和 16 字节规范无 padding base64url secret。
- session 只在模块内存保留十分钟；过期、终端取消或请求被终端接受后清零。
- 同一 SPA 登录跳转可继续使用 session；刷新和外部认证整页跳转会丢失证明，之后只能走未验证兼容路径。

<a id="session_proof"></a>
## 回环协议

页面只访问 `http://127.0.0.1:43110` 至 `43119`。发现使用 `GET /ping`；有 session 时携带随机 challenge，并用 Web Crypto 校验 tf 返回的 HMAC。导入使用 `POST /import`，只在发现证明有效时发送 `X-TF-Session-Proof`。

页面只称“已验证当前 tf 会话”。proof 不验证网页、API Key、本机程序来源或后续网关结果，也不提供 freshness 或防重放。没有 session、proof 不匹配或 Web Crypto 不可用时仍可导入，但发送前必须显示未验证警告。若已验证后导入 proof 过期或计算失败，本次不发送 Key；页面先降级为未验证状态，再要求用户确认。

回环 fetch 固定使用 CORS、`credentials: omit`、`cache: no-store`、`redirect: error`、`referrerPolicy: no-referrer` 和 `targetAddressSpace: loopback`。发现总预算为 30 秒。浏览器自动完成 `OPTIONS` 预检，前端不手动发送。

## 字段映射

- `host` 使用页面 Origin。tf 校验同源后会恢复 CLI 自己的完整服务地址。
- `key_name` 取 Keys 页名称。tf 会保存该来源元数据；未显式指定本地名称时，合法值还会成为终端命名候选。
- 普通 Key 发送其 `group_id` 和 `group_name`。
- 复合 Key 不发送单一分组元数据；实际能力由 tf 查询模型目录识别。

HTTP `202 Accepted` 只表示终端已确认。tf 之后才校验网关，并在未显式指定名称时让用户选择自动识别、网页名称或自订名称；页面只能提示最终结果以终端为准，不能显示“导入成功”或“Key 已保存”。

<a id="user_confirmation"></a>
## 页面交互

Keys 行的更多菜单提供“导入 TF CLI”，不增加第二套入口或 Key 选择状态。弹窗先发现服务，再显示已验证状态或未验证警告；用户点击“发送到 TF CLI”后才发送 Key。POST 等待期间提示用户在终端核对来源并确认。网页确认决定是否发送 Key，终端确认决定 tf 是否继续处理，两者不能互相替代。

<a id="browser_security_headers"></a>
## 浏览器安全头

默认 CSP、旧自定义 CSP 的运行时补全和 `deploy/config.example.yaml` 都在 `connect-src` 中精确列出十个回环 Origin。不得扩大为端口通配符、`localhost` 或局域网地址。

TokenRouter 默认不限制 `local-network-access`。若反向代理自行设置 `Permissions-Policy`，必须允许顶层页面访问本地网络。现代 Chromium 首次访问可能请求本地网络权限；拒绝时页面显示未找到本机会话并允许重试。

## 验证

- `tfCliImport.spec.ts`：fragment、过期、固定 HMAC 向量、验证降级、请求选项和 proof。
- `TfCliImportDialog.spec.ts`、`KeysView.spec.ts`、`KeyActionMenu.spec.ts`：发送确认、状态文案、字段映射和菜单入口。
- `security_headers_test.go`：十个精确 CSP Origin 及默认策略同步。

相关文档：[接口目录](index.md)、[配置边界](configuration.md)、[复合 API Key](../domains/composite_api_keys.md)。
