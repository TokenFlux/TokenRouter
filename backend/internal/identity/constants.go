// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

const DefaultUserAPIKeyLimit = 100
const MaxUserAPIKeyLimit = 2_147_483_647
const LinuxDoConnectSyntheticEmailDomain = "@linuxdo-connect.invalid"
const OIDCConnectSyntheticEmailDomain = "@oidc-connect.invalid"
const WeChatConnectSyntheticEmailDomain = "@wechat-connect.invalid"
const DingTalkConnectSyntheticEmailDomain = "@dingtalk-connect.invalid"
