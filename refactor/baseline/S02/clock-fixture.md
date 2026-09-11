# 资金测试时钟前提修正

最终 integration 曾有一次 `TestUsageBillingRepositoryBatchImageUnlimitedKeyReleaseKeepsExistingUsage` 失败：释放后预期 1，实际有一项为 1.8。对应资金业务代码和测试在该次执行时均与 HEAD 相同；此前多次 S02 全量与当次单测 20 次都通过，原始 HEAD 隔离副本 100 次也通过。因此不能声称原始 HEAD 自然运行已经重现该波动。

测试用数据库 `NOW()` 初始化 5h 窗口，稍后用主机 `time.Now().UTC()` 作为预占时刻，隐含两者时钟严格排序。原始 HEAD 的隔离测试显式把预占时间设为窗口之前 1ms 后，复现 quota=1、5h=1.8、1d=1、7d=1，命中相同的 0.8 断言差。原释放 SQL 的窗口包含判断拒绝扣减这个 5h 窗口；该 SQL 未在 S02 修改。

本阶段只稳定测试数据：初始化 UPDATE 通过 RETURNING 取回数据库窗口时间，预占使用同一值，明确满足本用例要验证的“窗口内释放”前提。保留全部 1.8→1 金额断言，不改资金代码、时间比较或数据库结构。修改后连续 20 次通过，并再跑全量 integration。

证据：[原始 HEAD 100 次](billing-original-head.result.json)、[时钟差重现](billing-clock-skew.result.json)、[可丢弃夹具差异](clock-skew-fixture.patch)、[修正后 20 次](billing-clock-fixture-fixed.result.json)。这是测试前提的稳定性修正；跨实例时钟偏差下真实业务窗口语义仍归 S04/S06 评审，本阶段不改变它。

提交前将复现补丁保存为无上下文格式，避免补丁上下文行触发仓库空白检查；使用 `git apply --unidiff-zero` 应用。已在临时目录对原始 HEAD 验证，生成的夹具文件与原补丁逐字节一致。
