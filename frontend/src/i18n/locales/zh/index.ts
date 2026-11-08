import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import creative from './creative'
import admin from './admin'
import misc from './misc'
import team from './team'
import requests from './requests'
import plugins from './plugins'

// index.ts assembles the Simplified Chinese interface catalog from domain
// dictionaries and shared legal and localization messages. Named request and
// administrator namespaces coexist with root-level spreads, whose order
// determines the value retained if domain modules define the same key.

export default {
  plugins,
  requests,
  legal: {
    login: '登录',
    loadFailed: '文档加载失败',
    retry: '请刷新页面重试。',
    notFound: '文档不存在',
    notFoundDescription: '当前文档不存在或已被移除。',
    title: '登录条款',
    empty: '暂无正文内容',
    updatedAt: '更新日期：{date}',
    acceptedPrefix: '我已阅读并同意',
    consentRequired: '继续登录前需要先同意最新条款。',
    disabledUntilAccepted: '同意后可以使用账号密码和快捷登录。',
    viewTerms: '查看条款',
    updateNotice: '条款更新通知',
    changedNotice: '服务条款于 {date} 更新，请阅读相关文档后再继续。',
    recently: '近期',
    documents: '相关文档',
    reject: '拒绝',
    accept: '同意并继续',
  },
  notFoundPage: {
    description: '页面不存在或已被移动。',
    back: '返回上一页',
    dashboard: '前往控制台',
    help: '需要帮助？',
    support: '联系我们',
  },

  localization: {
    useAsOriginal: '设为原文',
    languageConflict: '{language} 已有一条译文。选哪一份作为原文？',
    nameRequired: '请填写名称。',
    displayName: '展示名称',
    productName: '支付商品名称',
    fields: {
      site_name: '站点名称',
      site_title: '首页标题',
      site_subtitle: '副标题',
      site_share_title: '分享标题',
      site_share_description: '分享简介',
    },
    original: '原文',
    originalLanguage: '原文语言',
    chooseOriginalLanguage: '选择原文语言',
    unknownOriginal: '这段原文还没有标注语言。在上方选择它的语言后，才能修改原文。',
    addTranslation: '添加翻译',
    languageCount: '{n} 种语言',
    needsUpdate: '待更新',
    dialogTitle: '翻译 · {field}',
    dialogHint: '在左侧选择语言。没有译文或原文修改后，用户看到原文。',
    status: {
      translated: '已翻译',
      stale: '待更新',
      missing: '未翻译',
    },
    staleNotice: '原文修改过，这条译文暂不展示。',
    stillValid: '仍然适用',
    removeTranslation: '删除译文',
    languages: '语言',
    translations: '译文',
    originalLanguageUnset: '未设置语言',
    originalHint: '用户的语言没有可用译文时，看到这段原文。',
    reference: '原文参照 · {language}',
    missingHint: '这个语言还没有译文，用户现在看到原文。',
    startTranslation: '开始翻译',
    emptyValue: '未填写',
    keepOriginal: '保留当前原文，删除那条译文',
    useTranslationAsOriginal: '改用 {language} 译文作为原文',
    done: '完成',
    defaultLanguage: '默认语言',
  },
  ...landing,
  ...common,
  ...dashboard,
  ...creative,
  admin,
  ...misc,
  ...team,
}
