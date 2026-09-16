# S11 WS/Live 子任务交接（共享树，未提交）

## 已接入生产路径

- `gateway/live` 唯一拥有 Live 创建尝试循环、普通账号槽到 Live 租约接替、保存会话、身份绑定检查、每轮模型改写/响应恢复、sideband 双向 relay、B06 接管取消、observer/控制权轮询、续租/到期、关闭认领与零费用记录触发。
- `ObserverState` 接收旧应用 mutex/stopped/cancels/WaitGroup 的明确引用，Begin/Stop 算法在新核心；没有复制生命周期表。旧 `StopLiveObservers` 生命周期绑定继续有效。
- `service/openai_live.go` 和两个新增 adapter 文件保留凭据/TLS/Header/HTTP/WS 平台技术、已有路由/策略能力调用及逐字段 usage 展示投影。旧创建/代理/查询/观察入口已委托新核心。Live 仍只写零费用用量。
- `gateway/httpapi.LiveHandler` 拥有原 Live 创建与 sideband 的 HTTP/SDP/升级/错误行为。旧 `OpenAIGatewayHandler.Live`、`LiveSideband` 已委托，生产链已接入。
- `gateway/ws` 唯一拥有 turn 载荷快照队列、终态写出与下一轮准入屏障、首语义输出/活跃读取预算、客户端控制读循环、会话抢占注册表和 Redis owner 观察、turn 重试资格/当前轮重放载荷、双向 relay 原子会话模型与 tier/effort 元数据。旧 WS 调用点已委托，不复制状态。

## 仍未完成（不能据此宣称 S11.5 全部完成）

- `service/openai_ws_forwarder_ingress.go` 的 `ProxyResponsesWebSocketFromClient` 主循环，以及 `service/openai_ws_v2_passthrough_adapter.go` 的 `proxyResponsesWebSocketV2Passthrough` 尚持有旧实体/Gin、逐轮策略与完成 hooks。这两个大函数的完整编排仍需提取；已迁状态/资格/预算应继续复用，不再复制。
- 用户禁止子任务编辑的 `handler/openai_gateway_handler.go` 的 `ResponsesWebSocket` 完整准入/lease/turn 计费仍未移到新 handler。
- 旧 OpenAIGatewayService observer 字段仍是唯一状态持有者（核心通过 ObserverState 明确投影使用）。后续可由父任务迁为 app 注入所有者，但不必为当前生产接线改字段。
- Live HTTP 的认证/审核/资金依赖通过 `legacyLiveHTTP` 投影旧已迁能力，父任务统一原生准入后可直接提供 `gateway/httpapi.LiveHTTPPorts` 替换它。

## 精确接入

现阶段没有必须修改的 Wire/provider 构造签名，也不需改 `service/openai_gateway_service.go`。

原 handler 新方法：

```go
func (h *OpenAIGatewayHandler) NewLiveHTTPHandler() *gatewayhttp.LiveHandler
```

父任务可在路由汇总一次取得：

```go
live := h.OpenAIGateway.NewLiveHTTPHandler() // 变量字段名沿所在聚合类型调整
// 将原来的 h.OpenAIGateway.Live / LiveSideband 路由绑定替换为：
live.Live
live.LiveSideband
```

独立构造器：

```go
// github.com/TokenFlux/TokenRouter/internal/gateway/httpapi
NewLiveHandler(ports LiveHTTPPorts) *LiveHandler

// github.com/TokenFlux/TokenRouter/internal/gateway/live
New(ports Ports, retryInterval time.Duration, readLimit int64) *Service
NewCreator(runtime *Service, ports CreatePorts, maxDuration time.Duration) *Creator
```

`Service` 与 `Creator` 无自己的可变缓存/注册表；过渡入口创建只读门面时复用应用唯一存储和生命周期。Live 主体、记录与请求继续来自 gateway/session，不定义第二份。

## 依赖门禁输入

完整逐文件 import 见 `/tmp/s11-ws-live-files.json`。新核心没有旧 service/repository/handler/domain/model/config、Gin、HTTP/SQL/Redis 或具体 upstream import。

- gateway/live: gateway/session、gateway/modeltrace、routing/modelmap、scheduler、protocol/openai；google/uuid、gjson、sjson；标准库包含 context/errors/time/sync/strings/bytes/fmt/maps/crypto/sha256/encoding/hex。
- gateway/ws: gateway/session、scheduler；google/uuid、gjson；标准库 context/errors/fmt/strings/sync/sync/atomic/time。
- gateway/httpapi/live.go: gateway/live、gateway/session、gateway/modeltrace、protocol/openai、server/clientip；Gin、coder/websocket、gjson、sjson。
- gateway/httpapi/ws_frames.go: coder/websocket + context。
- 旧 service adapter 新增精确 import gateway/live、gateway/ws、gateway/httpapi。

## 验证与偶然观察

- `/tmp/s11-ws-live-race.json`: 初次定向 unit race 890 pass、1 既有 skip、0 fail（父子事件），之后继续增加了纯状态迁移，需结合最终日志。
- `/tmp/s11-native-live-ws-final.json`: 新核心 5 pass，无失败/跳过；包括 B06 交接取消、关闭认领、停止取消等待、turn 屏障、快照复制。
- `/tmp/s11-ws-live-final-race.json`: 一次把筛选扩到 `Turn` 后捕获 `openai_compat_model_test.go` 多个并行测试调用 `gin.SetMode` 的竞争。仅保存当次证据，未另行复现、未修复、不作为通过；原日志完整保留。该文件非本子任务修改范围。
- 后续暂态编译阻塞：父侧 ConcurrencyHelper 测试字面量、gateway_request_test.go 缺少正在迁移的 refreshGatewayRequestRanges、generate_session_hash_test.go 在 go vet 读取期间已移动。未修改父任务文件。
- 最终专项 `/tmp/s11-ws-live-verification.json`: 248 pass、1 既有 skip、0 fail，命令退出码 0。5 个新核心测试单独完整执行通过。28 个本子任务文件的已跟踪/新增 diff 检查均通过。

未提交/推送/切分支；未改 Wire、depguard、docs/refactor、Ent、migration 或 upstream 算法。所有本子任务文件在 `/tmp/s11-ws-live-files.json`。
