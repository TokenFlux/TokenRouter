// plugins.ts supplies localized plugin navigation and recovery messages for
// authenticated contribution pages. The host owns these labels while installed
// plugins provide their own page titles and translations through the frontend SDK
// and accepted manifest descriptions.

export default {
  pageTitle: '插件',
  loadFailed: '插件加载失败',
  componentMissing: '插件未注册所请求的页面。',
  notFound: '插件页面不可用',
  notFoundDescription: '此页面可能已被移除、停用，或仅对其他角色开放。',
}
