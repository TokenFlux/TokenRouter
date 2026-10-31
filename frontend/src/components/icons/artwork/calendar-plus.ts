// Artwork from Lucide, adapted with the shared scale pulse.
// See ../README.md for the source license and pinned version.
// https://github.com/lucide-icons/lucide/blob/66d8f9fc394b8530377e5f6112f0b8908ba01280/icons/calendar-plus.svg
import { createFallbackIcon } from '../createFallbackIcon'

// calendar-plus.ts supplies a calendar with an addition mark for subscription grants.
// Its unchanged upstream paths inherit host color and stroke width, while the
// shared fallback renderer supplies the same single interaction pulse and cleanup
// behavior used by other static Lucide artwork.

export default createFallbackIcon('calendar-plus', [
  ['path', { d: 'M16 18h6' }],
  ['path', { d: 'M16 2v3' }],
  ['path', { d: 'M19 15v6' }],
  ['path', { d: 'M21 11.5V5a2 2 0 00-2-2H5a2 2 0 00-2 2v14a2 2 0 002 2h8.3' }],
  ['path', { d: 'M3 9h18' }],
  ['path', { d: 'M8 2v3' }],
])
