// Artwork from Lucide, adapted with the shared scale pulse.
// See ../README.md for the source license and pinned version.
// https://github.com/lucide-icons/lucide/blob/66d8f9fc394b8530377e5f6112f0b8908ba01280/icons/crown.svg
import { createFallbackIcon } from '../createFallbackIcon'

// crown.ts supplies the Lucide crown artwork for exclusive access labels.
// The shared icon wrapper controls size, inherited foreground, and accessibility,
// while createFallbackIcon provides the standard interaction pulse when a caller
// enables animation for this otherwise decorative emblem.

export default createFallbackIcon('crown', [
  [
    'path',
    {
      d: 'M11.562 3.266a.5.5 0 0 1 .876 0L15.39 8.87a1 1 0 0 0 1.516.294L21.183 5.5a.5.5 0 0 1 .798.519l-2.834 10.246a1 1 0 0 1-.956.734H5.81a1 1 0 0 1-.957-.734L2.02 6.02a.5.5 0 0 1 .798-.519l4.276 3.664a1 1 0 0 0 1.516-.294z',
    },
  ],
  ['path', { d: 'M5 21h14' }],
])
