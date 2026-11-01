# 发布插件与索引

插件、Go SDK、Web SDK 和索引是独立发布资产。源码中的版本号、完成本地测试或生成归档，都不表示 GitHub Release、npm 包或生产部署已经存在。

## 发布插件

更新 plugin.json 的 version 和 CHANGELOG，确认最低宿主版本与实际使用能力一致。运行 `make test pack`，验证五个平台产物：Linux amd64/arm64、macOS amd64/arm64、Windows amd64。Windows 包包含 `bin/plugin.exe`，manifest 仍声明 `bin/plugin`。

创建 `v<version>` 标签触发模板 release.yml。它检查 manifest 与标签一致，构建共享前端与五个平台二进制，再上传 plugin.json、checksums.txt 和全部归档。不要在发布后替换同版本资产；索引保留第一次接受的归档摘要。

Go SDK 使用 `plugin-sdk/go/v<version>` 模块标签。Web SDK 独立发布到 `@tokenflux/tokenrouter-plugin-sdk`；依赖锁文件和 SDK 版本应随插件源码提交。首次公开发布前，项目方需创建 npm 组织和发布凭据。

## 提交索引登记

索引仓库种子在 [`plugin-sdk/templates/index-repo`](../../../plugin-sdk/templates/index-repo/)。新增 publisher 文件与 plugin 文件，说明每项权限用途，提供至少一个合格 Release。现有 publisher 的权限依据可信基线中的 GitHub 账号或组织列表；修改提案中的 ownership 不能给自己授权。新 publisher 需要维护者人工确认。

维护者审核后合并，构建器拉取新 Release，校验原始 manifest、每个安装包和 checksums，并生成带版本、权限、兼容性及下载摘要的目录。下载前缀只能转发包地址，不能改变校验内容。

## 配置官方发布资产

项目方创建 `TokenFlux/tokenrouter-plugins`、`TokenFlux/tokenrouter-plugin-template`（模板仓库）和 `TokenFlux/tr-plugin-checkin`。准备的种子文件不会自动创建远程仓库或填入组织凭据。

用 `trplugin keygen --out <安全目录>/index.private-key` 生成索引密钥，把私钥内容保存到 Actions secret `INDEX_SIGNING_KEY`，把输出公钥交给宿主维护者加入 `plugin.OfficialIndexKeys`。不要通过聊天、PR 或仓库提交私钥。启用受保护的 main 与 CODEOWNERS 审核。

build.yml 在 main 更新、每小时或手动触发时生成目录并提交到 dist 分支。记录一次真实运行链接，下载该次 index.json 与 index.json.sig，由宿主验证后再宣布分发可用。本地工作流静态检查不能代替这一步。

## 自建目录与撤回

fork 索引种子后修改名称、维护者与登记。默认仍要求签名；显式设置 `INDEX_UNSIGNED=true` 才不生成签名文件。宿主添加自定义来源时填写 HTTPS dist URL 与公钥；未签名来源留空公钥并显示警告。

撤回单个版本使用 yanked 和明确 reason；下架整个插件使用 delisted。已安装插件仍可查看撤回原因，不会因目录撤回自动卸载。新版本需要新的 SemVer，不能通过改写同版本资产绕过接受摘要。
