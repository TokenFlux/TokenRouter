// modelAuthor.ts assigns developer bylines to known model families on the homepage.
// Public marketplace groups describe how a model is served, and a single group
// can contain models from several developers. The byline follows the model ID.

// MODEL_AUTHORS maps verified model families to the developer names used on homepage cards.
const MODEL_AUTHORS = [
  { family: /^(?:gpt|chatgpt|codex|dall-e|sora|whisper|o[134])(?:[-_.]|$)/, name: 'OpenAI' },
  { family: /^claude(?:[-_.]|$)/, name: 'Anthropic' },
  { family: /^(?:gemini|gemma|imagen|veo|nano-banana)(?:[-_.]|$)/, name: 'Google' },
  { family: /^deepseek(?:[-_.]|$)/, name: 'DeepSeek' },
  { family: /^grok(?:[-_.]|$)/, name: 'xAI' },
  { family: /^(?:glm|chatglm)(?:\d|[-_.]|$)/, name: 'Z.AI' },
  { family: /^(?:kimi|moonshot)(?:[-_.]|$)/, name: 'Moonshot AI' },
  { family: /^(?:qwen|qwq|qvq)(?:\d|[-_.]|$)/, name: 'Alibaba' },
  { family: /^jev(?:[-_.]|$)/, name: 'TypeSafe AI' },
]

// modelAuthor resolves the developer of a recognized model family. A slash
// prefix can identify a hosting service, so matching uses the final segment.
// Unrecognized model IDs return null, allowing the card to omit its byline.
export function modelAuthor(modelId: string): string | null {
  const familyId = modelId
    .slice(modelId.lastIndexOf('/') + 1)
    .trim()
    .toLowerCase()
  return MODEL_AUTHORS.find(({ family }) => family.test(familyId))?.name ?? null
}
