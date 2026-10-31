// plugins.ts supplies localized plugin navigation and recovery messages for
// authenticated contribution pages. The host owns these labels while installed
// plugins provide their own page titles and translations through the frontend SDK
// and accepted manifest descriptions.

export default {
  pageTitle: 'Plugin',
  loadFailed: 'Could not load plugin',
  componentMissing: 'This plugin has not registered the requested page.',
  notFound: 'Plugin page unavailable',
  notFoundDescription: 'This page may have been removed, disabled, or restricted to another role.',
}
