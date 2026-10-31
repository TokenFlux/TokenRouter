import mcp from './mcp'
import modelAttributes from './modelAttributes'
import overview from './overview'
import pricing from './pricing'
import providers from './providers'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import plugins from './plugins'

// index.ts assembles the Simplified Chinese administrator catalog and defines
// protocol-control messages. Imported domain dictionaries contribute their top-
// level namespaces in spread order, while protocol text describes native
// support, conversion choices, and image-tool policies used by configuration
// forms.

export default {
  plugins,
  protocols: {
    imageModes: {
      title: '上游支持模式',
      hint: '请求模式自动转换。仅非流式的上游返回最终图片，不提供中间预览。',
      both: '两者支持',
      non_streaming: '仅非流式',
      streaming: '仅流式',
      generationMode: '图片生成模式',
      editMode: '图片编辑模式',
    },

    responsesWebSocketHTTPHint:
      '允许转换时，客户端继续使用长连接，上游通过普通 HTTP Responses 接收请求。',
    responsesWebSocket: '长连接对话（WebSocket）',
    responsesWebSocketHTTP: '转成普通 Responses 请求',
    nativeTitle: '原生支持协议',
    nativeHint: '仅列出此提供商类型原生支持的协议。全部关闭后不承接新调用。',
    loadError: '协议目录加载失败，请重新打开表单。',
    fallback: '提供商不支持时转换为',
    fallbackWhen: '不支持时',
    auto: '自动匹配转换',
    restricted: '限制转换目标（按顺序）',
    nativeOnly: '仅原生，不转换',
    imagePolicy: '对话中的图片生成（Responses）',
    imagePolicyHint: '控制对话请求中的生图工具，不影响独立的图片生成、编辑接口。',
    imagePolicyOptions: {
      inherit: {
        label: '跟随提供商和全局设置',
        description: '本分组不单独指定，先使用提供商设置；提供商未配置时，使用全局设置。',
      },
      enabled: {
        label: '自动添加生图工具',
        description:
          '普通对话会自动添加生图工具，精简模式（Responses Lite）除外。客户端自带的生图工具仍会保留。',
      },
      disabled: {
        label: '仅保留客户端自带的生图工具',
        description: '网关不额外添加生图工具；客户端请求中已有的生图工具会保留。',
      },
      block: {
        label: '移除生图工具',
        description:
          '不自动添加生图工具，并移除客户端请求中的生图工具。直接调用图片模型或图片接口不受影响。',
      },
    },
    groupTitle: '协议控制',
    groupHint:
      '先尝试提供商原生协议，再按允许的转换目标顺序尝试。自动模式使用服务端支持的转换路线。',
  },
  ...modelAttributes,
  ...overview,
  ...pricing,
  ...providers,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...mcp,
}
