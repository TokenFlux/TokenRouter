// socialMetadata.ts renders development HTML sharing tags from public settings.
// Cover bytes remain on the backend image endpoint; this renderer only consumes
// its resource path and descriptor. Production uses the Go renderer, so fixtures
// verify the same escaping, fallback, and absolute URL behavior in both paths.

/** SocialMetadata contains the optional public fields accepted from older and current servers. */
export interface SocialMetadata {
  site_name?: string // Resolved site name.
  site_title?: string // Resolved homepage title.
  site_subtitle?: string // Resolved homepage subtitle.
  site_share_title?: string // Resolved sharing title.
  site_share_description?: string // Resolved sharing description.
  site_share_image_url?: string // Digest-addressed cover path.
  site_share_image_type?: string // Validated cover MIME type.
  site_share_image_width?: number // Cover width in pixels.
  site_share_image_height?: number // Cover height in pixels.
  frontend_url?: string // Configured frontend base URL.
}

/** escapeAttribute encodes text and URL values for quoted HTML attributes. */
function escapeAttribute(value: string): string {
  return value.replace(
    /[&<>"']/g,
    (character) =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;',
      })[character] || character,
  )
}

/**
 * renderSocialMetadata builds Twitter and Open Graph tags for one development page.
 * requestURL supplies an absolute local URL when no frontend address is configured.
 * Invalid bases omit destinations, and query parameters never enter the canonical URL.
 */
export function renderSocialMetadata(settings: SocialMetadata, requestURL: string): string {
  const title =
    settings.site_share_title?.trim() || settings.site_title?.trim() || settings.site_name || ''
  const description = settings.site_share_description?.trim() || settings.site_subtitle || ''
  let canonical = ''
  let image = ''
  try {
    const request = new URL(requestURL)
    const base = new URL(settings.frontend_url?.trim() || request.origin)
    if (['http:', 'https:'].includes(base.protocol) && !base.username && !base.password) {
      canonical = new URL(request.pathname, base).href
      if (settings.site_share_image_url?.startsWith('/api/v1/settings/share-image/')) {
        image = new URL(settings.site_share_image_url, base).href
      }
    }
  } catch {
    // Missing development origins must not prevent startup. Text-only metadata
    // remains usable while invalid canonical and cover URLs are deliberately omitted.
  }
  const tags: string[] = []
  /** add appends a nonempty field after escaping its attribute value. */
  function add(attribute: string, name: string, value: string) {
    if (value) tags.push(`<meta ${attribute}="${name}" content="${escapeAttribute(value)}" />`)
  }
  add('name', 'twitter:card', image ? 'summary_large_image' : 'summary')
  add('name', 'twitter:title', title)
  add('name', 'twitter:description', description)
  add('name', 'twitter:image', image)
  add('property', 'og:type', 'website')
  add('property', 'og:title', title)
  add('property', 'og:description', description)
  add('property', 'og:site_name', settings.site_name || '')
  add('property', 'og:url', canonical)
  add('property', 'og:image', image)
  if (image) {
    add('property', 'og:image:type', settings.site_share_image_type || '')
    if (settings.site_share_image_width)
      add('property', 'og:image:width', String(settings.site_share_image_width))
    if (settings.site_share_image_height)
      add('property', 'og:image:height', String(settings.site_share_image_height))
  }
  return tags.join('\n')
}
