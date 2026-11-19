import { apiClient } from './client'

/**
 * pages.ts retrieves server-hosted Markdown custom pages through the shared API
 * client so locale selection, optional bearer authentication, and 401 token
 * rotation follow standard browser session rules. HTTP status failures resolve
 * with ok false while transport failures reject so callers can distinguish
 * missing pages from network interruptions.
 */

/**
 * PageMarkdownResponse pairs HTTP status availability with raw Markdown text
 * and the server's Content-Language header for relative asset resolution.
 */
export interface PageMarkdownResponse {
  ok: boolean // True when the server returned a 2xx Markdown document.
  status: number // HTTP response status code.
  text: string // Raw Markdown source text when ok is true.
  contentLanguage: string // Server Content-Language header or empty string.
}

/**
 * fetchPageMarkdown requests the raw Markdown document for slug using apiClient
 * without JSON envelope parsing. HTTP status errors resolve with ok false, while
 * transport-level failures rethrow for caller error presentation.
 */
export async function fetchPageMarkdown(slug: string): Promise<PageMarkdownResponse> {
  try {
    const response = await apiClient.get<string>(`/pages/${encodeURIComponent(slug)}`, {
      responseType: 'text',
      transformResponse: [(raw: unknown) => (typeof raw === 'string' ? raw : String(raw ?? ''))],
    })
    const headerVal = response.headers?.['content-language']
    return {
      ok: true,
      status: response.status,
      text: typeof response.data === 'string' ? response.data : String(response.data ?? ''),
      contentLanguage: typeof headerVal === 'string' ? headerVal : '',
    }
  } catch (err: unknown) {
    const status =
      typeof err === 'object' && err !== null && 'status' in err
        ? Number((err as { status?: unknown }).status)
        : 0
    // Return HTTP status rejections as non-ok responses so missing or forbidden
    // pages display the not-found state while network errors reach catch handlers.
    if (Number.isFinite(status) && status > 0) {
      return {
        ok: false,
        status,
        text: '',
        contentLanguage: '',
      }
    }
    throw err
  }
}
