import { apiClient } from '../client'
import type { ModelAttributes } from '@/api/modelAttributes.types'

/**
 * modelAttributes.ts reads model catalog attributes and edits reusable group
 * attribute profiles. Preview requests can include an unsaved profile and client
 * protocol, while saving chooses create or update from the profile ID and
 * catalog refresh remains a separate operation.
 */

/**
 * AttributeRule assigns attributes to model patterns and optional route-match
 * criteria in an attribute profile.
 */
export interface AttributeRule {
  // reference_model selects another model in this profile. Supplied targets require
  // empty attributes; omission retains the rule's ordinary attribute overrides.
  reference_model?: string // Same-profile attribute reference.
  id?: string // Record identifier value.
  models: string[] // Model identifier list.
  attributes: ModelAttributes // Resolved model attributes.
  /**
   * Omitted route criteria leave the rule without that route restriction;
   * supplied fields narrow the previewed execution dimensions.
   */
  match?: AttributeRouteMatch // Route matching criteria.
}

/**
 * AttributeRouteMatch selects execution dimensions used when previewing
 * route-specific model attribute rules.
 */
export interface AttributeRouteMatch {
  platform?: string // Provider platform identifier.
  provider_type?: string // Provider credential type.
  provider_mode?: string // Provider authentication mode.
  site?: string // Provider deployment site.
  client_protocol?: string // Incoming client protocol.
  upstream_protocol?: string // Selected upstream protocol.
}

/**
 * ResolvedAttributes describes one previewed execution target, its resulting
 * attributes and their resolution sources.
 */
export interface ResolvedAttributes {
  reference_model?: string // Effective attribute reference.
  target: AttributeRouteMatch & {
    model: string // Requested model identifier.
    catalog_provider?: string // Catalog provider identifier.
    endpoint?: string // Protocol endpoint path.
  } // Resolved execution target.
  attributes: ModelAttributes // Resolved model attributes.
  /**
   * This mapping names the source of resolved attribute values so preview
   * consumers can explain which configuration supplied them.
   */
  configured_attributes?: ModelAttributes // Profile values before converter restrictions.
  sources: Record<string, string> // Attribute source mapping.
  rules: Record<string, unknown> // Configured matching rules.
  version: string // Reported version identifier.
}

/**
 * AttributeConfig stores a named attribute profile with group assignments and
 * ordered model-matching rules.
 */
export interface AttributeConfig {
  /**
   * A truthy ID selects update in modelAttributesAPI.save; a missing or zero ID
   * follows its creation branch.
   */
  id?: number // Record identifier value.
  name: string // Human-readable display name.
  description: string // Human-readable record description.
  status: string // Current server-reported status.
  group_ids: number[] // Configured group ids.
  rules: AttributeRule[] // Configured matching rules.
  created_at?: string // Record creation timestamp.
  updated_at?: string // Last modification timestamp.
}

/**
 * DefaultAttributes pairs one catalog model with its provider, provenance and
 * default attribute values.
 */
export interface DefaultAttributes {
  model: string // Model identifier value.
  provider: string // Provider identity details.
  source: string // Recorded observation source.
  canonical_model_id?: string // Canonical model ID.
  attributes: ModelAttributes // Resolved model attributes.
}

/**
 * AttributeCatalogResponse contains paginated default attributes, provider
 * choices and catalog refresh metadata.
 */
export interface AttributeCatalogResponse {
  items: DefaultAttributes[] // Returned collection entries.
  total: number // Total matching records.
  providers: string[] // Catalog provider choices.
  version: string // Reported version identifier.
  last_updated: string // Recorded last updated.
  last_error?: string // Recorded last error.
}

/**
 * base identifies the administrator model-attribute route family shared by
 * profile editing, execution previews and default catalog requests.
 */
const base = '/admin/model-attributes'

/**
 * modelAttributesAPI separates profile editing from default-catalog reads;
 * update is the operation that requests a remote catalog refresh.
 */
export const modelAttributesAPI = {
  /**
   * preview submits preview and returns the server response data.
   */
  async preview(groupID: number, model: string, config?: AttributeConfig, clientProtocol?: string) {
    return (
      await apiClient.post<{
        routes: ResolvedAttributes[] // Resolved execution routes.
      }>(`${base}/preview`, {
        group_id: groupID,
        model,
        config,
        client_protocol: clientProtocol,
      })
    ).data
  },
  /**
   * list retrieves configs and returns the server response data.
   */
  async list(params: Record<string, string | number>) {
    return (
      await apiClient.get<{
        items: AttributeConfig[] // Returned collection entries.
        total: number // Total matching records.
      }>(`${base}/configs`, {
        params,
      })
    ).data
  },
  /**
   * save creates or updates an attribute profile and returns its saved record. A
   * truthy profile ID selects the update endpoint; otherwise the complete
   * profile is submitted to create a record.
   */
  async save(config: AttributeConfig) {
    return config.id
      ? (await apiClient.put<AttributeConfig>(`${base}/configs/${config.id}`, config)).data
      : (await apiClient.post<AttributeConfig>(`${base}/configs`, config)).data
  },
  /**
   * remove awaits deletion of one attribute configuration without returning response
   * data to its caller.
   */
  async remove(id: number) {
    await apiClient.delete(`${base}/configs/${id}`)
  },
  /**
   * defaults retrieves defaults and returns the server response data.
   */
  async defaults(params: Record<string, string | number>) {
    return (await apiClient.get<AttributeCatalogResponse>(`${base}/defaults`, { params })).data
  },
  /**
   * getModelDefaultAttributes retrieves model default attributes and returns the
   * server response data.
   */
  async getModelDefaultAttributes(model: string) {
    return (await apiClient.get<ModelAttributes>(`${base}/defaults/model`, { params: { model } }))
      .data
  },
  /**
   * update requests a remote default-attribute catalog refresh from the
   * administrator endpoint. It uses a two-minute request timeout because the
   * operation can include remote catalog checks and downloads.
   */
  async update() {
    await apiClient.post(`${base}/defaults/update`, undefined, { timeout: 120_000 })
  },
}
