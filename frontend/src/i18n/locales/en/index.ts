import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import creative from './creative'
import admin from './admin'
import misc from './misc'
import team from './team'
import requests from './requests'
import plugins from './plugins'

// index.ts assembles the English interface catalog from domain dictionaries and
// shared legal and localization messages. Named request and administrator
// namespaces coexist with root-level spreads, whose order determines the value
// retained if domain modules define the same key.

export default {
  plugins,
  requests,
  legal: {
    login: 'Log in',
    loadFailed: 'Could not load document',
    retry: 'Refresh the page to try again.',
    notFound: 'Document not found',
    notFoundDescription: 'This document does not exist or has been removed.',
    title: 'Login terms',
    empty: 'No content has been provided.',
    updatedAt: 'Updated: {date}',
    acceptedPrefix: 'I have read and agree to',
    consentRequired: 'Accept the latest terms to continue signing in.',
    disabledUntilAccepted: 'Password and quick sign-in become available after you accept.',
    viewTerms: 'View terms',
    updateNotice: 'Terms updated',
    changedNotice: 'Our terms were updated on {date}. Read the documents before continuing.',
    recently: 'a recent date',
    documents: 'Documents',
    reject: 'Decline',
    accept: 'Accept and continue',
  },
  notFoundPage: {
    description: 'The page you are looking for does not exist or has moved.',
    back: 'Go back',
    dashboard: 'Go to dashboard',
    help: 'Need help?',
    support: 'Contact Us',
  },

  localization: {
    useAsOriginal: 'Use as original',
    languageConflict: '{language} already has a translation. Which one should be the original?',
    nameRequired: 'Enter a name.',
    displayName: 'Display name',
    productName: 'Payment product name',
    fields: {
      site_name: 'Site name',
      site_title: 'Homepage title',
      site_subtitle: 'Subtitle',
    },
    original: 'Original',
    originalLanguage: 'Original language',
    chooseOriginalLanguage: 'Choose original language',
    unknownOriginal:
      'This original has no language yet. Choose its language above before editing it.',
    addTranslation: 'Add translation',
    languageCount: '{n} languages',
    needsUpdate: 'Needs update',
    dialogTitle: 'Translate · {field}',
    dialogHint:
      'Pick a language on the left. Users see the original when a translation is missing or the original has changed.',
    status: {
      translated: 'Translated',
      stale: 'Needs update',
      missing: 'Not translated',
    },
    staleNotice: 'The original changed, so this translation is hidden.',
    stillValid: 'Still fits',
    removeTranslation: 'Remove translation',
    languages: 'Languages',
    translations: 'Translations',
    originalLanguageUnset: 'Language not set',
    originalHint: 'Users see this original when their language has no usable translation.',
    reference: 'Original · {language}',
    missingHint: 'This language has no translation yet. Users see the original for now.',
    startTranslation: 'Start translating',
    emptyValue: 'Empty',
    keepOriginal: 'Keep the current original and delete that translation',
    useTranslationAsOriginal: 'Use the {language} translation as the original',
    done: 'Done',
    defaultLanguage: 'Default language',
  },
  ...landing,
  ...common,
  ...dashboard,
  ...creative,
  admin,
  ...misc,
  ...team,
}
