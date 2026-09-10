import { beforeEach } from 'vitest'
import { protocolCatalog, protocolCatalogError, protocolCatalogLoading, type ProtocolCatalog } from '@/api/admin/protocolCapabilities'
import fixture from '../fixtures/protocol-catalog.json'

// 仅目录相关测试显式预热；每次复制夹具，防止测试之间共享可变配置。
export function useProtocolCatalogFixture() {
  beforeEach(() => {
    protocolCatalog.value = structuredClone(fixture) as ProtocolCatalog
    protocolCatalogError.value = false
    protocolCatalogLoading.value = false
  })
}
