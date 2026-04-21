import { apiClient } from './client'
import type { MarketplaceGroup } from '@/types'

export async function getMarketplaceModels(): Promise<MarketplaceGroup[]> {
  const { data } = await apiClient.get<MarketplaceGroup[]>('/marketplace/models')
  return data
}

export const marketplaceAPI = {
  getMarketplaceModels,
}

export default marketplaceAPI
