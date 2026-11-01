import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginReleaseNotes from '../PluginReleaseNotes.vue'

// PluginReleaseNotes.spec.ts checks that publisher-authored changelogs render as
// Markdown without active content. The fixture mixes ordinary Markdown with a script
// tag and an event handler, matching what an untrusted catalog could supply.

describe('release notes', () => {
  // A catalog changelog carries an image with an error handler and an inline script.
  // Rendering keeps the Markdown list but strips the script and the handler.
  it('renders Markdown and removes active markup', () => {
    const wrapper = mount(PluginReleaseNotes, {
      props: {
        markdown:
          '## 1.0.0\n\n- Fixed streaks\n\n<img src=x onerror="alert(1)"><script>alert(2)</script>',
      },
    })
    expect(wrapper.find('li').text()).toBe('Fixed streaks')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('img').attributes('onerror')).toBeUndefined()
  })
})
