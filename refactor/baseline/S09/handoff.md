# S09 后续职责与回退

这里记录仍由旧入站及业务拥有者执行的职责；具体静态消费者由 final-consumer-refs 的普通/unit/integration 数据定位，文件与符号摘要见 final-file-declarations。字段投影不是第二份业务实现。

| 后续阶段 | 保留职责与实际入口 | 退出条件 |
| --- | --- | --- |
| S10 | 内容审核、websearch 编排、通知；Qoder/其它入口通过 app/legacybridge 调用旧审核和完成观察端口 | 对应模块提供同一用例和配置读取端口，删除旧审核/搜索转接 |
| S11 | 除 Qoder Chat 外的 HTTP 入站；旧 Gateway/OpenAIGateway/Gemini/Qoder handler 的请求授权、模型恢复、心跳、全局 failover、完成 worker | 保持已迁 upstream 的单次执行/读取与资源所有权，替换旧输入上下文；不增加外层重试循环 |
| S11 | OpenAI WS 入站 relay 编排、previous-response 归属、reasoning/invalid-encrypted 会话缓存、每轮价格与 usage 快照 | 原 WS pool/wsrelay 和报文算法继续由 upstream 唯一提供；按请求/turn 接管旧上下文与缓存拥有者 |
| S11 | count_tokens 本地估算及平台资格、HTTP 错误和账号健康的调用时机 | 使用当前原生查询端口与唯一 wire/估算策略，保持原生 Responses 和 Anthropic 两种返回形状 |
| S12 | 商业订单、推广、退款与专项审计 | 沿用 billing 资金参与能力；供应商使用结果不代替资金提交证明 |
| S13 | batchimage/creative 状态机、视频归属/完成、Batch/GCS/JSONL 任务编排 | 使用 upstream 的批量、对象流和媒体原语，保持任务与资金闭合事务 |
| S14 | 备份恢复、安装维护与二进制替换 | 继续复用精简 bootstrap/CLI；不构造完整后台图 |
| S15/S16 | pkg/service/repository/admin handler 的别名、投影、构造委托及 unit 专属转接 | 以实际消费者清零为删除条件；不让迁出文件继承旧依赖例外 |

账号授权会话、token cache、刷新协调及条件持久化属于 account。平台只做交换、签名、原生报文、协议恢复和技术观测，不持有任意访问旧账号凭据的能力。旧 session/credential 形状的兼容必须继续保持原 key、TTL、序列化、一次性消费和各平台失败语义。

S07 outbox 仍依靠周期全量重建恢复迟提交的低 ID 事件；本阶段没有新增逐事件严格消费或多实例协调承诺。S04 平台额度协调仍只适用于单服务进程。

固定修复 B01—B04 和测试夹具 T01 的原失败证据保存在 planning 及对应 regression 日志。回退 B01 恢复 Qoder 部分用量遗漏；B02 恢复推理前取消仍发送；B03 恢复 Vertex 等锁不响应取消；B04 恢复 WS 模型字段竞争。T01 回退恢复替身缺少 GetMultiple 的 panic。

验收阻塞例外 X01/X02 仅修正测试夹具，回退恢复夹具竞争/独立运行 panic，不改变生产行为。X03 保存原始 Alpha Response Body 再关闭；回退恢复错误回卷时原始响应未关闭的风险。没有追加同类历史审计或修改其他响应路径。

本地 HTTP/TLS/WS、真实 PostgreSQL/Redis 和进程验证不等价于真实供应商、硬件 attestation 或外部 TLS 捕获。原有 E2E 地址/密钥及缺少脚本限制沿用冻结资料。跳过与仅构建分别记录，不计行为通过。
