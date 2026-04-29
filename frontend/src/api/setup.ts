/**
 * Setup API endpoints
 */
import axios from 'axios'

// Create a separate client for setup endpoints (not under /api/v1)
const setupClient = axios.create({
  baseURL: '',
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json'
  }
})

export interface SetupStatus {
  needs_setup: boolean
  step: string
}

export interface DatabaseConfig {
  host: string
  port: number
  user: string
  password: string
  dbname: string
  sslmode: string
}

export interface RedisConfig {
  host: string
  port: number
  password: string
  db: number
  enable_tls: boolean
}

export interface AdminConfig {
  email: string
  password: string
}

export interface ServerConfig {
  host: string
  port: number
  mode: string
}

export interface InstallRequest {
  database: DatabaseConfig
  redis: RedisConfig
  admin: AdminConfig
  server: ServerConfig
}

export interface InstallResponse {
  message: string
  restart: boolean
}

const SETUP_PREVIEW_BYPASS_KEY = 'setup_preview_bypass'

function shouldBypassSetupForPreview(): boolean {
  if (!import.meta.env.DEV || typeof window === 'undefined') {
    return false
  }

  const params = new URLSearchParams(window.location.search)
  if (params.get('preview') === '1') {
    localStorage.setItem(SETUP_PREVIEW_BYPASS_KEY, '1')
    return true
  }

  return localStorage.getItem(SETUP_PREVIEW_BYPASS_KEY) === '1'
}

/**
 * Get setup status
 */
export async function getSetupStatus(): Promise<SetupStatus> {
  if (shouldBypassSetupForPreview()) {
    return { needs_setup: false, step: 'preview' }
  }

  const response = await setupClient.get('/setup/status')
  return response.data.data
}

/**
 * Test database connection
 */
export async function testDatabase(config: DatabaseConfig): Promise<void> {
  await setupClient.post('/setup/test-db', config)
}

/**
 * Test Redis connection
 */
export async function testRedis(config: RedisConfig): Promise<void> {
  await setupClient.post('/setup/test-redis', config)
}

/**
 * Perform installation
 */
export async function install(config: InstallRequest): Promise<InstallResponse> {
  const response = await setupClient.post('/setup/install', config)
  return response.data.data
}
