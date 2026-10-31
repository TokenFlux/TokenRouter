// plugins.ts supplies localized plugin navigation and recovery messages for
// authenticated contribution pages. The host owns these labels while installed
// plugins provide their own page titles and translations through the frontend SDK
// and accepted manifest descriptions.

export default {
  fields: {
    secretSaved: 'Saved; leave unchanged to keep',
    secretHint: 'Enter a new value to replace the saved secret.',
    secretWillClear: 'The saved secret will be cleared on save.',
    keepSecret: 'Keep saved value',
    clearSecret: 'Clear saved value',
    errors: {
      required: 'Enter a value.',
      number: 'Enter a finite number.',
      integer: 'Enter an integer within the safe numeric range.',
      minimum: 'Enter at least {limit}.',
      maximum: 'Enter no more than {limit}.',
      length: 'Use no more than {limit} characters.',
      choice: 'Select an available value.',
      pattern: 'The value does not match the required format.',
      rejected: 'The plugin rejected this value.',
    },
  },

  pageTitle: 'Plugin',
  loadFailed: 'Could not load plugin',
  componentMissing: 'This plugin has not registered the requested page.',
  notFound: 'Plugin page unavailable',
  notFoundDescription: 'This page may have been removed, disabled, or restricted to another role.',
}
