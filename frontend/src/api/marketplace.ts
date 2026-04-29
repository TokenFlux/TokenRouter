import { apiClient } from './client'
import type { MarketplaceGroup, MarketplaceStats } from '@/types'

export async function getMarketplaceModels(): Promise<MarketplaceGroup[]> {
  const { data } = await apiClient.get<MarketplaceGroup[]>('/marketplace/models')
  return data
}

export async function getMarketplaceStats(): Promise<MarketplaceStats> {
  const { data } = await apiClient.get<MarketplaceStats>('/marketplace/stats')
  return data
}

export const marketplaceAPI = {
  getMarketplaceModels,
  getMarketplaceStats,
}

export default marketplaceAPI
