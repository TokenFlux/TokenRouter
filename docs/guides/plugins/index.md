# 插件开发手册

插件通过独立 Go 进程和共享 Vue 的前端页面扩展 TokenRouter。当前支持页面、HTTP 接口与定时任务；自定义提供商不属于本轮实现。安装要求管理员信任代码，宿主能力的权限、额度和审计不能替代操作系统或浏览器沙箱。

- [快速开始](quick_start.md)：从模板创建、运行、测试和打包。
- [SDK 参考](sdk_reference.md)：生命周期、宿主能力、KV、前端 API 与控件。
- [发布与索引](publishing.md)：五平台资产、签名目录、版本撤回与密钥配置。
- [审核标准](review.md)：代码对应关系、权限用途、资金和投递重试。

声明与协议的精确约束见[插件契约](../../interfaces/plugin_contracts.md)，部署者的恢复流程见[插件运维](../../operations/plugin_operations.md)。Go 与 Web SDK 独立采用 MIT 许可证；宿主许可证保持不变。
