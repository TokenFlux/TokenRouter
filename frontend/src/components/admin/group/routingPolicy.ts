import type { GroupRoutingPolicy } from '@/types'

// 每次创建独立草稿，避免分组之间共享模型规则。
export function defaultRoutingPolicy(): GroupRoutingPolicy {
  return { enabled: true, model_mapping: {}, restrict_models: false, restriction_model_source: 'group_mapped', allowed_models: {}, features: '', features_config: {} }
}

// 表单保存后直接应用各项设置；保留旧协议字段，但不再提供总开关。
export function cloneRoutingPolicy(policy?: GroupRoutingPolicy): GroupRoutingPolicy {
  const copied = policy ? JSON.parse(JSON.stringify(policy)) as GroupRoutingPolicy : defaultRoutingPolicy()
  return { ...defaultRoutingPolicy(), ...copied, enabled: true, model_mapping: copied.model_mapping ?? {}, allowed_models: copied.allowed_models ?? {}, features_config: copied.features_config ?? {}, features: copied.features ?? '' }
}
