import type { IconName } from '@/components/icons/registry'
import type { attributeCapabilities } from '@/api/modelAttributes.types'

// modelCapabilityIcons.ts maps each boolean model attribute capability to the glyph shown
// beside its name. The model attribute summary and the capability filter both read this
// table, so a capability keeps the same icon wherever it appears.

// MODEL_CAPABILITY_ICONS lists one distinct glyph for every attribute capability; the
// Record type requires an entry for each capability declared by the API types.
export const MODEL_CAPABILITY_ICONS: Record<(typeof attributeCapabilities)[number], IconName> = {
  reasoning: 'brain',
  tool_call: 'toolCall',
  structured_output: 'structuredOutput',
  temperature: 'temperature',
  top_p: 'sliders',
  attachment: 'attachment',
}
