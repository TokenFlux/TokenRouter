// index.js registers a frontend-only fixture without starting a plugin process.
// Host tests use this asset to verify static installation state and page loading
// independently of the subprocess supervisor, wallet callbacks, or backend route
// contributions that ordinary interactive plugins may also declare.
window.__TR_PLUGIN_SDK__.definePlugin({
  pages: { home: { render: () => window.__TR_VUE__.h('p', 'Static plugin fixture') } },
})
