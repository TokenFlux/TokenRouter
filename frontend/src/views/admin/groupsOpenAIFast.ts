import type { GroupOpenAIFastPolicy } from "@/types";
// 判断分组是否支持 OpenAI Fast 强制策略。
export function supportsGroupOpenAIFast(platform: string): boolean {
  return platform === "openai";
}

// 仅在支持的平台上保留开关值，避免前端提交无效配置。
export function normalizeGroupOpenAIFast(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupOpenAIFast(platform) && enabled;
}

// 不支持的平台一律恢复跟随请求。
export function normalizeGroupOpenAIFastPolicy(platform:string, policy:string):GroupOpenAIFastPolicy {
 if (supportsGroupOpenAIFast(platform) && ["follow_request","force_priority","force_ultrafast","force_off"].includes(policy)) return policy as GroupOpenAIFastPolicy;
 return "follow_request";
}
