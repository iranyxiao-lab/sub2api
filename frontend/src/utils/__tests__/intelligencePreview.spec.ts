import { describe, expect, it } from 'vitest'
import { buildStaticHtmlPreview } from '../intelligencePreview'

describe('intelligence HTML preview', () => {
  it('extracts fenced HTML and installs a restrictive policy before untrusted markup', () => {
    const output = buildStaticHtmlPreview('Here is the page:\n```html\n<h1>Hello</h1><script>alert(1)</script>\n```')
    expect(output.indexOf('Content-Security-Policy')).toBeLessThan(output.indexOf('<h1>'))
    expect(output).toContain("default-src 'none'")
    expect(output).toContain("script-src 'none'")
    expect(output).toContain("form-action 'none'")
    expect(output).toContain('<h1>Hello</h1>')
  })
})
