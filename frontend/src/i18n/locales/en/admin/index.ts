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

// index.ts assembles the English administrator catalog and defines protocol-
// control messages. Imported domain dictionaries contribute their top-level
// namespaces in spread order, while protocol text describes native support,
// conversion choices, and image-tool policies used by configuration forms.

export default {
  plugins,
  protocols: {
    imageModes: {
      title: 'Upstream modes',
      hint: 'Requests are converted automatically. Non-streaming upstreams return the final image without intermediate previews.',
      both: 'Both modes',
      non_streaming: 'Non-streaming only',
      streaming: 'Streaming only',
      generationMode: 'Image generation modes',
      editMode: 'Image editing modes',
    },

    responsesWebSocketHTTPHint:
      'When conversion is allowed, the client keeps its WebSocket connection while the upstream receives regular HTTP Responses requests.',
    responsesWebSocket: 'Long conversations (WebSocket)',
    responsesWebSocketHTTP: 'Convert to a regular Responses request',
    nativeTitle: 'Native upstream protocols',
    nativeHint:
      'Only native protocols for this provider type are shown. An empty set disables new calls.',
    loadError: 'Failed to load protocol capabilities. Please reopen the form.',
    fallback: 'Convert when unavailable',
    fallbackWhen: 'If unavailable',
    auto: 'Automatic conversion',
    restricted: 'Ordered conversion targets',
    nativeOnly: 'Native only',
    imagePolicy: 'Image generation in conversations (Responses)',
    imagePolicyHint:
      'Controls image-generation tools in conversations. Separate image generation and editing endpoints are unaffected.',
    imagePolicyOptions: {
      inherit: {
        label: 'Follow provider and global settings',
        description:
          'Use the provider setting for this group. If the provider has no override, use the global setting.',
      },
      enabled: {
        label: 'Automatically add an image tool',
        description:
          'Add an image-generation tool to regular conversations, except Responses Lite requests. Keep any image tools provided by the client.',
      },
      disabled: {
        label: 'Keep only client-provided image tools',
        description:
          'Do not add an image-generation tool. Keep image tools already included in the client request.',
      },
      block: {
        label: 'Remove image tools',
        description:
          'Do not add image tools, and remove those included in the client request. Direct calls to image models or image endpoints are unaffected.',
      },
    },
    groupTitle: 'Protocol controls',
    groupHint:
      'Try each provider’s native protocol first, then the allowed conversion targets in order. Automatic mode uses the routes supported by the server.',
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
