// fuzzyMatch.ts scores how well a search query matches display text and reports which
// characters of the original text matched, so search results can be ranked and
// highlighted. The global search palette uses it for model identifiers, model names,
// navigation labels, and their hidden aliases.
//
// Model identifiers mix separators unpredictably: users type "opus45" for
// "claude-opus-4-5" or "gpt 4o" for "gpt-4o". Matching therefore compares a
// normalized form that lowercases letters and drops spaces, hyphens, underscores, and
// dots. Highlighting still needs positions in the original text, so normalization
// keeps a map from each normalized character back to its source index, and match
// ranges are rebuilt from that map instead of from normalized offsets.
//
// Scores fall into tiers that are far enough apart that a position penalty inside one
// tier never crosses into the next: an exact match beats a prefix, which beats a match
// starting at a word boundary, then any substring, then a scattered subsequence.

// MATCH_TIER assigns the base score of each match kind. Tier gaps of 200 exceed the
// largest position or spread penalty, so callers can compare scores across kinds.
export const MATCH_TIER = {
  exact: 1000, // Whole normalized text equals the query.
  prefix: 800, // Normalized text starts with the query.
  wordStart: 600, // Query starts at a word boundary.
  substring: 400, // Query appears anywhere contiguously.
  subsequence: 200, // Query characters appear in order with gaps.
} as const

// MAX_PENALTY caps position and spread penalties below the tier gap so a weaker kind
// can never outrank a stronger one.
const MAX_PENALTY = 150

// SEPARATORS lists characters dropped by normalization; WORD_BREAKS additionally
// includes characters that only start a new word for word-boundary detection.
const SEPARATORS = new Set([' ', '-', '_', '.'])
const WORD_BREAKS = new Set([' ', '-', '_', '.', '/', ':', '(', ')', '[', ']', '@'])

// MatchRange marks one highlighted run in the original text as a half-open interval.
export interface MatchRange {
  start: number // First matched source index.
  end: number // Index after the last matched character.
}

// FuzzyMatch reports a successful match's ranking score and highlighted source runs.
export interface FuzzyMatch {
  score: number // Higher values rank first.
  ranges: MatchRange[] // Ordered, non-overlapping source runs.
}

// FieldMatch extends FuzzyMatch with the index of the field that produced the best
// score, so callers highlight only the field that actually matched.
export interface FieldMatch extends FuzzyMatch {
  field: number // Index into the supplied field list.
}

// NormalizedText pairs comparable text with the source index of each character.
interface NormalizedText {
  value: string // Lowercased text without separators.
  sourceIndex: number[] // Original index per normalized character.
  wordStart: boolean[] // Whether each normalized character begins a word.
}

// normalizeQuery lowercases a query and removes separators, matching how candidate text
// is normalized. An all-separator query normalizes to an empty string.
export function normalizeQuery(query: string): string {
  let result = ''
  for (const char of query.toLowerCase()) {
    if (!SEPARATORS.has(char)) result += char
  }
  return result
}

// normalizeText lowercases text, drops separators, and records where each kept
// character came from. A character starts a word when it is first, follows any word
// break, or is an uppercase letter after a lowercase one (camelCase boundaries).
function normalizeText(text: string): NormalizedText {
  const value: string[] = []
  const sourceIndex: number[] = []
  const wordStart: boolean[] = []
  for (let index = 0; index < text.length; index++) {
    const char = text[index]
    if (SEPARATORS.has(char)) continue
    const previous = index > 0 ? text[index - 1] : ''
    const camelBoundary =
      previous !== '' &&
      previous === previous.toLowerCase() &&
      previous !== previous.toUpperCase() &&
      char !== char.toLowerCase()
    value.push(char.toLowerCase())
    sourceIndex.push(index)
    wordStart.push(index === 0 || WORD_BREAKS.has(previous) || camelBoundary)
  }
  return { value: value.join(''), sourceIndex, wordStart }
}

// toRanges converts matched normalized offsets into merged source ranges. Offsets must be
// ascending; adjacent source indices merge into one run for compact highlighting.
function toRanges(normalized: NormalizedText, offsets: number[]): MatchRange[] {
  const ranges: MatchRange[] = []
  for (const offset of offsets) {
    const source = normalized.sourceIndex[offset]
    const last = ranges[ranges.length - 1]
    if (last && last.end === source) {
      last.end = source + 1
    } else {
      ranges.push({ start: source, end: source + 1 })
    }
  }
  return ranges
}

// contiguousOffsets lists the normalized offsets covered by a contiguous match.
function contiguousOffsets(start: number, length: number): number[] {
  return Array.from({ length }, (_, index) => start + index)
}

// findWordStartMatch returns the first normalized offset where the query begins at a
// word boundary, or -1 when every occurrence starts mid-word.
function findWordStartMatch(normalized: NormalizedText, query: string): number {
  let offset = normalized.value.indexOf(query)
  while (offset !== -1) {
    if (normalized.wordStart[offset]) return offset
    offset = normalized.value.indexOf(query, offset + 1)
  }
  return -1
}

// findSubsequence greedily matches query characters in order and returns their
// normalized offsets, or null when some character cannot be placed.
function findSubsequence(normalized: NormalizedText, query: string): number[] | null {
  const offsets: number[] = []
  let cursor = 0
  for (const char of query) {
    const offset = normalized.value.indexOf(char, cursor)
    if (offset === -1) return null
    offsets.push(offset)
    cursor = offset + 1
  }
  return offsets
}

// fuzzyMatch scores text against a query and returns highlighted source ranges, or
// null when the query does not match. An empty or all-separator query never matches,
// so callers decide separately what to show before the user types.
//
// The strongest applicable tier wins: exact, prefix, word start, substring, then
// subsequence. Inside a contiguous tier, later starting positions lose up to
// MAX_PENALTY points; a subsequence loses points for the characters skipped between
// its first and last match. Both penalties stay below the 200-point tier gap.
//
// Ranges always refer to the original text, including when separators were skipped:
// "opus45" against "claude-opus-4-5" highlights "opus", "4", and "5" as three runs.
export function fuzzyMatch(query: string, text: string): FuzzyMatch | null {
  const normalizedQuery = normalizeQuery(query)
  if (normalizedQuery === '' || text === '') return null
  const normalized = normalizeText(text)
  const length = normalizedQuery.length

  if (normalized.value === normalizedQuery) {
    return { score: MATCH_TIER.exact, ranges: toRanges(normalized, contiguousOffsets(0, length)) }
  }
  if (normalized.value.startsWith(normalizedQuery)) {
    return { score: MATCH_TIER.prefix, ranges: toRanges(normalized, contiguousOffsets(0, length)) }
  }
  const wordOffset = findWordStartMatch(normalized, normalizedQuery)
  if (wordOffset !== -1) {
    return {
      score: MATCH_TIER.wordStart - Math.min(wordOffset, MAX_PENALTY),
      ranges: toRanges(normalized, contiguousOffsets(wordOffset, length)),
    }
  }
  const substringOffset = normalized.value.indexOf(normalizedQuery)
  if (substringOffset !== -1) {
    return {
      score: MATCH_TIER.substring - Math.min(substringOffset, MAX_PENALTY),
      ranges: toRanges(normalized, contiguousOffsets(substringOffset, length)),
    }
  }
  const offsets = findSubsequence(normalized, normalizedQuery)
  if (!offsets) return null
  // Spread counts characters skipped between the first and last matched character, so
  // tightly clustered subsequences rank above scattered ones.
  const spread = offsets[offsets.length - 1] - offsets[0] + 1 - length
  return {
    score: MATCH_TIER.subsequence - Math.min(spread, MAX_PENALTY),
    ranges: toRanges(normalized, offsets),
  }
}

// bestMatch returns the highest-scoring match across several fields of one candidate,
// or null when no field matches. Earlier fields win ties, so callers list the visible
// title before secondary text and hidden aliases. Empty fields are skipped.
export function bestMatch(query: string, fields: readonly string[]): FieldMatch | null {
  let best: FieldMatch | null = null
  for (let field = 0; field < fields.length; field++) {
    const match = fuzzyMatch(query, fields[field])
    if (match && (best === null || match.score > best.score)) {
      best = { ...match, field }
    }
  }
  return best
}

// TextSegment is one run of display text, marked when it belongs to a match range.
export interface TextSegment {
  text: string // Run of the original text.
  matched: boolean // Whether the run is highlighted.
}

// highlightSegments splits text into alternating plain and matched runs for rendering.
// Ranges must be ordered and non-overlapping, as fuzzyMatch returns them; ranges outside
// the text are clipped, and no ranges yield the whole text as one plain run.
export function highlightSegments(text: string, ranges: readonly MatchRange[]): TextSegment[] {
  const segments: TextSegment[] = []
  let cursor = 0
  for (const range of ranges) {
    const start = Math.max(range.start, cursor)
    const end = Math.min(range.end, text.length)
    if (start >= end) continue
    if (start > cursor) segments.push({ text: text.slice(cursor, start), matched: false })
    segments.push({ text: text.slice(start, end), matched: true })
    cursor = end
  }
  if (cursor < text.length) segments.push({ text: text.slice(cursor), matched: false })
  return segments
}
