# 发布插件与索引

插件、Go SDK、Web SDK 和索引是独立发布资产。源码中的版本号、完成本地测试或生成归档，都不表示 GitHub Release、npm 包或生产部署已经存在。

## 发布插件

更新 plugin.json 的 version 和 CHANGELOG，确认最低宿主版本与实际使用能力一致。运行 `make test pack`，验证五个平台产物：Linux amd64/arm64、macOS amd64/arm64、Windows amd64。Windows 包包含 `bin/plugin.exe`，manifest 仍声明 `bin/plugin`。

创建 `v<version>` 标签触发模板 release.yml。它检查 manifest 与标签一致，构建共享前端与五个平台二进制，再上传 plugin.json、checksums.txt 和全部归档。不要在发布后替换同版本资产；索引保留第一次接受的归档摘要。

Go SDK 使用 `plugin-sdk/go/v<version>` 模块标签。Web SDK 独立发布到 `@tokenflux/tokenrouter-plugin-sdk`，新发布版本使用 `plugin-sdk/web/v<version>` 标签记录来源；已有 0.1.0 使用 npm 的 gitHead 追溯。依赖锁文件和 SDK 版本应随插件源码提交。

发布 TokenRouter 时，`tr-release` 分别对比两个 SDK 最近已发布版本的源码，先发布存在有效变化的 SDK，再发布网关。两个 SDK 保持独立的 `0.x.y`：兼容修复递增 y，新增能力或不兼容变化递增 x 并清零 y。纯测试、格式和开发文档变化不单独触发发布；发布判断必须说明每个变化的影响。完整流程见[发布工作流](../../operations/development_workflow.md#发布)。宿主版本和插件 API 版本继续决定运行兼容性，SDK 发版不会自动改变它们，也不会自动升级独立插件的依赖。

## 提交索引登记

在[官方索引仓库](https://github.com/TokenFlux/tokenrouter-plugins)新增 publisher 文件与 plugin 文件，说明每项权限用途，提供至少一个合格 Release。现有 publisher 的权限依据可信基线中的 GitHub 账号或组织列表；修改提案中的 ownership 不能给自己授权。新 publisher 需要维护者人工确认。

维护者审核后合并，构建器拉取新 Release，校验原始 manifest、每个安装包和 checksums，并生成带版本、权限、兼容性及下载摘要的目录。下载前缀只能转发包地址，不能改变校验内容。

## 配置官方发布资产

官方分发使用 `TokenFlux/tokenrouter-plugins`、`TokenFlux/tokenrouter-plugin-template`（模板仓库）和 `TokenFlux/tr-plugin-checkin`，入口见[官方资源](index.md#官方资源)。这三个仓库分别维护索引、模板和完整签到插件，主仓库不再保留副本。建立其他分发仓库时从对应官方仓库创建，不复制签名私钥或组织凭据。

用 `trplugin keygen --out <安全目录>/index.private-key` 生成索引密钥，把私钥内容保存到 Actions secret `INDEX_SIGNING_KEY`，把输出公钥交给宿主维护者加入 `plugin.OfficialIndexKeys`。不要通过聊天、PR 或仓库提交私钥。启用受保护的 main 与 CODEOWNERS 审核。

build.yml 在 main 更新、每小时或手动触发时生成目录并提交到 dist 分支。记录一次真实运行链接，下载该次 index.json 与 index.json.sig，由宿主验证后再宣布分发可用。本地工作流静态检查不能代替这一步。

## 自建目录与撤回

fork 官方索引仓库后修改名称、维护者与登记。默认仍要求签名；显式设置 `INDEX_UNSIGNED=true` 才不生成签名文件。宿主添加自定义来源时填写 HTTPS dist URL 与公钥；未签名来源留空公钥并显示警告。

撤回单个版本使用 yanked 和明确 reason；下架整个插件使用 delisted。已安装插件仍可查看撤回原因，不会因目录撤回自动卸载。新版本需要新的 SemVer，不能通过改写同版本资产绕过接受摘要。
