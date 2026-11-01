# 创建第一个插件

使用[官方模板仓库](https://github.com/TokenFlux/tokenrouter-plugin-template)创建新仓库。模板提供可运行的每日签到后端、用户页面、管理历史页和发布工作流；完整示例在独立仓库维护，主仓库只保留 SDK 测试夹具。

## 准备依赖

工具链以模板仓库的版本文件为准。TokenRouter v0.6.0 对应 Go SDK v0.1.1 与 `@tokenflux/tokenrouter-plugin-sdk@0.2.0`，模板仓库的依赖版本独立维护。源码联调可以在临时副本中使用 Go replace 与本地 Web SDK 链接，发布前恢复正式依赖并生成锁文件。

从 Web SDK 0.1.0 升级时，检查定义了本地 keyframes 的样式：动画名称必须是可静态确定的字面量。使用 `animation-name: fade; animation-duration: var(--duration)` 可以保留动态时长；由变量决定名称的写法会使构建失败。

1. 把 `go.mod` 的 module 和 `cmd/plugin/main.go` 的内部导入从 `github.com/TokenFlux/tokenrouter-plugin-template` 改为自己的仓库路径。
2. 在 `plugin.json` 和 `web/vite.config.ts` 中同步设置插件 ID，例如 `yourname.checkin`，并更新发布者与仓库地址。
3. 保留英文 name、description 等必需本地化值，按需要添加中文。
4. 运行 `make bootstrap`，提交 `go.sum` 和 `web/pnpm-lock.yaml`。工作流使用冻结的前端依赖，不会代替作者补锁文件。

## 本地联调

示例声明最低宿主版本为 0.6.0。源码联调时可使用 `go build -ldflags="-X main.Version=0.0.0-dev" ./cmd/server` 构建开发宿主；开发版本仅跳过宿主版本上下界，API、权限与平台检查仍然执行。

`make dev` 生成当前机器平台的 `dev/` 目录。把其绝对路径加入宿主的 `plugins.dev_dirs`，设置 `plugins.dev_mode=true`，重启宿主。开发目录中的权限默认授予，且跳过二进制摘要校验，只应在隔离开发实例中使用。

修改代码后重新执行 `make dev`，再从宿主管理页重启插件。开发记录和配置仅存在于内存；重启宿主会重新读取目录。`pnpm --dir web dev` 使用 SDK 的独立页面 mock，适合界面开发，但 mock 余额和记录不能证明真实宿主持久化。

从用户侧菜单完成一次签到，再检查管理历史、账户余额、重复提交和页面刷新。调整 reward 后，新的一天或尚未认领的用户使用新奖励；已认领但未完成的请求继续使用原金额和原幂等键。

## 构建与安装

```sh
make test
make pack
```

打包会构建五个平台，验证 manifest、引用资源和归档结构，并产生 `plugin.json`、`checksums.txt` 和 `.tar.gz`。在宿主“插件”页上传当前平台归档，审阅权限并安装。缺少必填配置时安装保持禁用，配置完成后再启用。

业务记录放在宿主 KV；`StateDir` 只保存可丢弃缓存。前端资源公开可读，不得包含密钥。安装目录不可写入运行时数据，版本更新可能删除旧目录。
