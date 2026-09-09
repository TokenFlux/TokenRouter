// 图片生成权限独立于模型定价，按平台能力显示开关。
export const supportsImageGenerationPlatform = (platform: string): boolean =>
  ["antigravity", "gemini", "grok", "openai"].includes(platform)
