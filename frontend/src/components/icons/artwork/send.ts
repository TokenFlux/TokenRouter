// Artwork from Lucide, adapted with the shared scale pulse.
// See ../README.md for the source license and pinned version.
// https://github.com/lucide-icons/lucide/blob/66d8f9fc394b8530377e5f6112f0b8908ba01280/icons/send.svg
import { createFallbackIcon } from '../createFallbackIcon'

// send.ts supplies a paper plane for outgoing plugin email.
// Its unchanged upstream paths inherit host color and stroke width, while the
// shared fallback renderer supplies the same single interaction pulse and cleanup
// behavior used by other static Lucide artwork.

export default createFallbackIcon('send', [
  [
    'path',
    {
      d: 'M14.536 21.686a.5.5 0 0 0 .937-.024l6.5-19a.496.496 0 0 0-.635-.635l-19 6.5a.5.5 0 0 0-.024.937l7.93 3.18a2 2 0 0 1 1.112 1.11z',
    },
  ],
  ['path', { d: 'm21.854 2.147-10.94 10.939' }],
])
