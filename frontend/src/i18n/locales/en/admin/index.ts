import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'

export default {
    protocols: {
      nativeTitle: 'Native upstream protocols',
      nativeHint: 'Only native protocols for this account type are shown. An empty set disables new calls.',
      loadError: 'Failed to load protocol capabilities. Please reopen the form.',
      fallback: 'Convert when unavailable',
      nativeOnly: 'Native only',
      imagePolicy: 'Responses image policy',
      groupTitle: 'Protocol controls',
      groupHint: 'Control each client entry. Use an enabled native protocol first, otherwise the configured conversion target.',
    },
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
}
