import type { GroupClientProtocol, GroupPlatform } from '@/types'

import { protocolCatalog } from '@/api/admin/protocolCapabilities'

function orderedProtocols(protocols: Iterable<GroupClientProtocol>): GroupClientProtocol[] {
  const selected = new Set(protocols)
  return (protocolCatalog.value?.protocols ?? []).filter(protocol => !protocol.upstream_only && selected.has(protocol.id)).map(protocol => protocol.id)
}

export function supportedGroupClientProtocols(platform: GroupPlatform): GroupClientProtocol[] {
  return orderedProtocols((protocolCatalog.value?.groups.find(group => group.platform === platform)?.protocols ?? []))
}

export function defaultGroupClientProtocols(platform: GroupPlatform): GroupClientProtocol[] {
  return orderedProtocols((protocolCatalog.value?.groups.find(group => group.platform === platform)?.defaults ?? []))
}

// 过滤不受平台支持的值，并保持公共契约规定的固定顺序。
export function effectiveGroupClientProtocols(
  platform: GroupPlatform,
  protocols: readonly GroupClientProtocol[] | null | undefined
): GroupClientProtocol[] {
  // 用户使用说明直接消费后端已校验的集合，不需要访问管理员目录接口。
  if (!protocolCatalog.value) return [...(protocols ?? [])]
  const supported = new Set((protocolCatalog.value?.groups.find(group => group.platform === platform)?.protocols ?? []))
  return orderedProtocols((protocols ?? []).filter((protocol) => supported.has(protocol)))
}

export function hasGroupClientProtocol(
  protocols: readonly GroupClientProtocol[],
  protocol: GroupClientProtocol
): boolean {
  return protocols.includes(protocol)
}

export function setGroupClientProtocol(
  platform: GroupPlatform,
  protocols: readonly GroupClientProtocol[],
  protocol: GroupClientProtocol,
  enabled: boolean
): GroupClientProtocol[] {
  const supported = supportedGroupClientProtocols(platform)
  if (!supported.includes(protocol)) {
    return effectiveGroupClientProtocols(platform, [...protocols])
  }

  const next = new Set(protocols)
  if (enabled) {
    next.add(protocol)
  } else {
    next.delete(protocol)
  }
  return effectiveGroupClientProtocols(platform, [...next])
}
