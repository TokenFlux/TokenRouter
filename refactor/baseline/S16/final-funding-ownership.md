# S16 资金写入销项

S00 的十二组、218个方法均已定位到当前拥有者。以下表格区分资金写入和业务编排；逐方法路径、符号、源摘要及实际测试事件见 `final-funding-ownership.json`。

| 组 | 编排 | 资金/存储 | 事务及失败边界 |
| --- | --- | --- | --- |
| F01 普通请求结算 | gateway/completion；billing | billing/postgres.SettlementStore | 普通结算闭合SQL事务认领去重、锁付款用户并提交余额/订阅/Key/成员/账号及outbox；整段死锁重试，不自动加入外层Ent事务。 提交后按原顺序同步缓存和通知；日志失败不再次扣款，simple跳过资金。 |
| F02 订阅发放、时间链、窗口与兑换 | billing订阅/兑换用例 | billing/postgres；identity/postgres并发参与 | 发放、兑换和窗口变更在闭合或明确参与的同一Ent事务中完成；码锁、权益、usage/次数及Key改绑不拆开提交。 成功提交后才执行原入口的认证/资金失效及返利；空订阅缓存方法不算有效失效。 |
| F03 管理员余额 set/add/subtract | identity.UserAdmin/UserService | billing/postgres.BalanceStore | set/add/subtract在原子SQL/行锁范围取得真实旧值；原生identity存储只是带同一连接的资金委托。 管理员返利与调整记录保留尽力语义，不回滚已经完成的调账。 |
| F04 注册、首次身份绑定与默认赠送 | identity注册/绑定；app/bootstrap | identity/postgres身份初始行；billing参与权益 | 身份创建、绑定与默认赠送保留原事务、savepoint和补偿边界；并发数归identity，余额/订阅/平台额度通过命名明确的billing能力参与。 原入口提交后通知/认证失效与推广处理；首次赠送不改成普通充值。 |
| F05 Key、团队成员与账号限额配置/累计/重置 | apikey/team/account/identity配置和生命周期 | billing/postgres消费写入；对应模块配置Adapter | 配置只更新显式字段；Key/成员/账号消费累计、窗口与重置由billing写入，同请求配置/重置和跨模块生命周期沿用现有连接。 认证触发器/outbox与原尽力调度事件边界保持，不重复入队。 |
| F06 用户平台额度 Redis 执法与数据库镜像 | billing.Eligibility/平台额度/Flusher | billing/rediscache与billing/postgres | 同一进程共享按用户可取消协调器；flusher读取快照前持锁到数据库写回结束，管理重置持锁跨提交与缓存失效。 Redis故障保持降级及dirty回填；不声明跨进程协调或PG/Redis原子提交。 |
| F07 Promo Code 赠送余额 | promotion.PromoService | promotion/postgres及billing同事务余额参与 | 码锁、使用限制、资金、usage和次数在同一事务；沿用对应赠送/累计字段语义。 原提交后缓存失效；失败整体回滚，不把赠送统一成管理员充值。 |
| F08 返利冻结、解冻和转主余额 | promotion.AffiliateService | promotion/postgres及billing参与 | 锁邀请人档案后计算上限并计提；返利转余额的认领、清零、余额与转账事实同事务。 冻结/解冻、查询及支付来源去重保持；通知不替代持久化资金事实。 |
| F09 支付余额和订阅履约 | payment履约 | payment/postgres；billing与promotion参与 | 保留多阶段履约和5分钟lease；订单专属充值码/来源订阅去重，返利审计认领与返利写入同事务。 成功后的通知使用唯一notification；邮件失败不撤销已发放权益。 |
| F10 退款准备、补偿与最终确认 | payment.RefundWorkflow | payment/postgres.RefundStore及billing权益参与 | 准备预扣与恢复审计原子提交，渠道调用在事务外；失败/待确认补偿、最终成功和必要审计用各自短事务，比较版本/退款身份防迟到覆盖。 状态/审计持久化失败返回错误；缺失恢复事实要求人工核实，不盲目重复渠道退款。 |
| F11 创作台/批量图片预占、捕获和释放 | creative/batchimage状态机 | billing.Funds/SettlementStore；任务PostgreSQL参与 | Reserve/Capture/Release与分配及受控任务投影同一SQL事务；任务表写入由app绑定各自FundingParticipant，billing不硬编码任务表。 保留旧请求ID/指纹/快照、删除Key/退出成员及原窗口释放规则；提交后按原入口更新缓存。 |
| F12 setup/simple 初始化、备份恢复及历史 SQL | setup/app/bootstrap；backup | identity/routing初始化；infra/postgres迁移；backup/provider恢复 | 初始化行、默认数据、迁移与恢复为操作级长期写权限；迁移锁/checksum规则保持，psql单事务且ON_ERROR_STOP。 配置文件/安装锁步骤不伪装成整体事务；恢复提交后的状态保存失败不表示数据库回滚。 |
