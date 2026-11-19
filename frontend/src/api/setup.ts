import axios from 'axios'
import { buildGatewayUrl } from './url'

/**
 * setup.ts sends initial installation checks through a dedicated Axios instance
 * rooted at the gateway origin. Setup endpoints live outside the versioned
 * browser API, so status and install functions unwrap their envelope explicitly
 * while connection tests await completion.
 */

/**
 * setupClient sends root-level installation requests at the configured gateway
 * origin, using JSON payloads and a thirty-second default timeout.
 */
const setupClient = axios.create({
  baseURL: buildGatewayUrl('/').replace(/\/+$/, ''),
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
})

/**
 * SetupStatus reports whether installation is required and which setup step the
 * server exposes.
 */
export interface SetupStatus {
  needs_setup: boolean // Recorded needs setup.
  step: string // Current installation step.
}

/**
 * DatabaseConfig supplies PostgreSQL host, credentials, database name and SSL
 * mode for installation checks.
 */
export interface DatabaseConfig {
  host: string // Service host address.
  port: number // Service TCP port.
  user: string // User identity details.
  password: string // Submitted password text.
  dbname: string // PostgreSQL database name.
  sslmode: string // PostgreSQL SSL mode.
}

/**
 * RedisConfig supplies Redis connection credentials, database index and TLS
 * selection for installation checks.
 */
export interface RedisConfig {
  host: string // Service host address.
  port: number // Service TCP port.
  username: string // Redis authentication or ACL username.
  password: string // Submitted password text.
  db: number // Redis database index.
  enable_tls: boolean // Configured enable tls.
}

/**
 * AdminConfig supplies the initial administrator's email and password during
 * installation.
 */
export interface AdminConfig {
  email: string // Account email address.
  password: string // Submitted password text.
}

/**
 * ServerConfig supplies the server listen address, port and operating mode
 * during installation.
 */
export interface ServerConfig {
  host: string // Service host address.
  port: number // Service TCP port.
  mode: string // Selected operating mode.
}

/**
 * InstallRequest combines database, Redis, administrator and server
 * configuration for initial installation.
 */
export interface InstallRequest {
  database: DatabaseConfig // PostgreSQL connection settings.
  redis: RedisConfig // Redis connection settings.
  admin: AdminConfig // Initial administrator credentials.
  server: ServerConfig // Server listener settings.
}

/**
 * InstallResponse reports the installation outcome message and whether the
 * server requests a restart.
 */
export interface InstallResponse {
  message: string // Server-provided result message.
  restart: boolean // Service restart request.
}

/**
 * getSetupStatus retrieves setup status and returns the server result.
 */
export async function getSetupStatus(): Promise<SetupStatus> {
  const response = await setupClient.get('/setup/status')
  return response.data.data
}

/**
 * testDatabase awaits the setup endpoint's database connection probe without
 * returning response data.
 */
export async function testDatabase(config: DatabaseConfig): Promise<void> {
  await setupClient.post('/setup/test-db', config)
}

/**
 * testRedis awaits the setup endpoint's Redis connection probe without returning
 * response data.
 */
export async function testRedis(config: RedisConfig): Promise<void> {
  await setupClient.post('/setup/test-redis', config)
}

/**
 * install requests installation and returns the server result.
 */
export async function install(config: InstallRequest): Promise<InstallResponse> {
  const response = await setupClient.post('/setup/install', config)
  return response.data.data
}

/**
 * probeSetupRestartReady polls /setup/status with no-store cache control after
 * installation acceptance and reports whether normal application mode is ready.
 */
export async function probeSetupRestartReady(): Promise<boolean> {
  const response = await fetch(buildGatewayUrl('/setup/status'), {
    method: 'GET',
    cache: 'no-store',
  })
  if (!response.ok) {
    return false
  }
  const payload = await response.json()
  return Boolean(payload?.data && !payload.data.needs_setup)
}
