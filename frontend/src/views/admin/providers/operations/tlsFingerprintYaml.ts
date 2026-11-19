/**
 * tlsFingerprintYaml.ts provides shared scalar decoding, JSON-quoted YAML
 * encoding, and atomic flat-field parsing for TLS fingerprint profile and
 * router dialogs so invalid YAML pastes never leave partially mutated drafts.
 */

/**
 * ProfileYamlFieldInputs holds comma-separated TLS array text parsed from a
 * profile YAML snippet before numeric or string-array conversion on save.
 */
export interface ProfileYamlFieldInputs {
  cipher_suites: string // Ordered cipher suite identifiers.
  curves: string // Ordered supported curve identifiers.
  point_formats: string // Ordered point format values.
  signature_algorithms: string // Ordered signature algorithm identifiers.
  alpn_protocols: string // Ordered advertised ALPN protocols.
  supported_versions: string // Ordered supported TLS versions.
  key_share_groups: string // Ordered key share groups.
  psk_modes: string // Ordered PSK exchange modes.
  extensions: string // Ordered TLS extension identifiers.
}

/**
 * ParsedTlsProfileYaml carries validated profile metadata and array-input text
 * ready to apply atomically to the profile editor draft.
 */
export interface ParsedTlsProfileYaml {
  name: string // Nonempty decoded profile display name.
  description: string | null // Optional decoded description text.
  enable_grease: boolean // Parsed GREASE toggle value.
  fieldInputs: ProfileYamlFieldInputs // Parsed comma-separated TLS array fields.
}

/**
 * parseYamlScalar reads simple quoted or plain scalar text and treats empty,
 * null, or tilde markers as empty. Double quotes use JSON decoding with
 * outer-quote removal on failure; single quotes collapse doubled apostrophes.
 */
export function parseYamlScalar(value: string): string {
  const trimmed = value.trim()
  if (!trimmed || trimmed === 'null' || trimmed === '~') return ''

  if (trimmed.startsWith('"') && trimmed.endsWith('"')) {
    try {
      return JSON.parse(trimmed)
    } catch {
      // Keep malformed double-quoted input as inner text so the editor can
      // display and correct it instead of aborting the paste.
      return trimmed.replace(/^"|"$/g, '')
    }
  }

  if (trimmed.startsWith("'") && trimmed.endsWith("'")) {
    return trimmed.slice(1, -1).replace(/''/g, "'")
  }

  return trimmed
}

/**
 * formatYamlString encodes profile or router text as a JSON-quoted YAML scalar.
 */
export function formatYamlString(value: string): string {
  return JSON.stringify(value)
}

/**
 * parseTlsProfileYaml scans recognized flat TLS profile fields into an isolated
 * draft based on currentForm and currentInputs, returning null unless a
 * nonempty profile name is present in text.
 */
export function parseTlsProfileYaml(
  text: string,
  currentForm: {
    name: string // Existing profile name.
    description: string | null // Existing profile description.
    enable_grease: boolean // Existing GREASE preference.
  },
  currentInputs: ProfileYamlFieldInputs,
): ParsedTlsProfileYaml | null {
  const trimmedText = text.trim()
  if (!trimmedText) return null

  const draft: ParsedTlsProfileYaml = {
    name: currentForm.name,
    description: currentForm.description,
    enable_grease: currentForm.enable_grease,
    fieldInputs: { ...currentInputs },
  }

  let foundName = false
  for (const line of trimmedText.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) continue

    const match = trimmed.match(/^(\w+):\s*(.+)$/)
    if (!match) continue

    const [, key, rawValue] = match
    const value = rawValue.trim()

    switch (key) {
      case 'name': {
        const unquoted = parseYamlScalar(value)
        if (unquoted) {
          draft.name = unquoted
          foundName = true
        }
        break
      }
      case 'description':
        draft.description = parseYamlScalar(value) || null
        break
      case 'enable_grease':
        draft.enable_grease = value === 'true'
        break
      case 'cipher_suites':
      case 'curves':
      case 'point_formats':
      case 'signature_algorithms':
      case 'supported_versions':
      case 'key_share_groups':
      case 'psk_modes':
      case 'extensions': {
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          draft.fieldInputs[key] = inner
            .split(',')
            .map((entry) => entry.trim())
            .filter((entry) => entry.length > 0)
            .join(', ')
        }
        break
      }
      case 'alpn_protocols': {
        const arrMatch = value.match(/^\[(.*)?\]$/)
        if (arrMatch) {
          const inner = arrMatch[1] || ''
          draft.fieldInputs.alpn_protocols = inner
            .split(',')
            .map((entry) => entry.trim().replace(/^["']|["']$/g, ''))
            .filter((entry) => entry.length > 0)
            .join(', ')
        }
        break
      }
    }
  }

  return foundName ? draft : null
}
