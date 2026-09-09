import { shallowRef } from 'vue'
import apiClient from '@/api/client'
import type { GroupClientProtocol } from '@/types'

export interface ProtocolDefinition {
  id: GroupClientProtocol
  name: string
  endpoint: string
  upstream_only: boolean
  platforms: string[]
}
export interface ProtocolCatalog {
  auxiliary_operations: { operation: string; protocol?: GroupClientProtocol; authorization: string }[]
  protocols: ProtocolDefinition[]
  accounts: { platform: string; type: string; auth_mode: string; protocols: GroupClientProtocol[] }[]
  groups: { platform: string; protocols: GroupClientProtocol[]; defaults: GroupClientProtocol[]; fallback_targets: Partial<Record<GroupClientProtocol, GroupClientProtocol[]>>; default_fallbacks: Partial<Record<GroupClientProtocol, GroupClientProtocol>> }[]
}

// 能力目录不含用户配置，整个管理会话共享一次只读请求；失败后允许重试。
export const protocolCatalog = shallowRef<ProtocolCatalog | null>(null)
let pending: Promise<ProtocolCatalog> | undefined
export function loadProtocolCatalog(): Promise<ProtocolCatalog> {
  if (protocolCatalog.value) return Promise.resolve(protocolCatalog.value)
  if (!pending) pending = apiClient.get<ProtocolCatalog>('/admin/protocol-capabilities').then(({ data }) => {
    protocolCatalog.value = data
    return data
  }).finally(() => { pending = undefined })
  return pending
}

export function nativeProtocolOptions(platform: string, type: string, authMode = ''): GroupClientProtocol[] {
  if (authMode === '*') {
    const profiles = protocolCatalog.value?.accounts.filter(profile => profile.platform === platform && profile.type === type) ?? []
    return (profiles[0]?.protocols ?? []).filter(id => profiles.every(profile => profile.protocols.includes(id)))
  }
  const normalized = authMode.trim().toLowerCase()
  const mode = ['personalaccesstoken', 'personal_access_token'].includes(normalized) ? 'personalAccessToken' : normalized === 'agentidentity' ? 'agentIdentity' : ''
  return protocolCatalog.value?.accounts.find(profile => profile.platform === platform && profile.type === type && profile.auth_mode === mode)?.protocols ?? []
}
