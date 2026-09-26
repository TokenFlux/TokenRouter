import overview from './overview'
import pricing from './pricing'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'

export default {
    protocols: {
      nativeTitle: '原生支持协议',
      nativeHint: '仅列出此账号类型原生支持的协议。全部关闭后不承接新调用。',
      loadError: '协议目录加载失败，请重新打开表单。',
      fallback: '账号不支持时转换为',
      nativeOnly: '仅原生，不转换',
      imagePolicy: 'Responses 图片策略',
      groupTitle: '协议控制',
      groupHint: '按入口控制访问。账号优先使用已启用的原生协议，否则使用指定转换目标。',
    },
  ...overview,
  ...pricing,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
}
