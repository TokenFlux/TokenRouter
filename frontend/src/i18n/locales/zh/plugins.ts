// plugins.ts supplies localized plugin navigation and recovery messages for
// authenticated contribution pages. The host owns these labels while installed
// plugins provide their own page titles and translations through the frontend SDK
// and accepted manifest descriptions.

export default {
  fields: {
    secretSaved: '已保存，不修改则保留',
    secretHint: '输入新值可替换已保存的密钥。',
    secretWillClear: '保存时将清除已有密钥。',
    keepSecret: '保留已保存的值',
    clearSecret: '清除已保存的值',
    errors: {
      required: '请填写此项。',
      number: '请输入有效数字。',
      integer: '请输入安全数值范围内的整数。',
      minimum: '不能小于 {limit}。',
      maximum: '不能大于 {limit}。',
      length: '不能超过 {limit} 个字符。',
      choice: '请选择可用的值。',
      pattern: '输入值不符合要求的格式。',
      rejected: '插件未接受此值。',
    },
  },

  pageTitle: '插件',
  loadFailed: '插件加载失败',
  componentMissing: '插件未注册所请求的页面。',
  notFound: '插件页面不可用',
  notFoundDescription: '此页面可能已被移除、停用，或仅对其他角色开放。',
}
