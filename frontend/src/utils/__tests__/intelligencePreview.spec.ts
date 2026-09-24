import { describe, expect, it } from 'vitest'
import { buildStaticHtmlPreview, extractIntelligenceHtml } from '../intelligencePreview'

describe('intelligence HTML preview', () => {
  it('extracts fenced HTML and installs a restrictive policy before untrusted markup', () => {
    const output = buildStaticHtmlPreview('Here is the page:\n```html\n<h1>Hello</h1><script>alert(1)</script>\n```')
    expect(output.indexOf('Content-Security-Policy')).toBeLessThan(output.indexOf('<h1>'))
    expect(output).toContain("default-src 'none'")
    expect(output).toContain("script-src 'none'")
    expect(output).toContain("form-action 'none'")
    expect(output).toContain('<h1>Hello</h1>')
    expect(output).not.toContain('<script>')
  })

  it('removes self-navigation and nested documents while retaining static layout', () => {
    const output = buildStaticHtmlPreview('<!doctype html><html><head><style>h1{color:red}</style><meta http-equiv="refresh" content="0;url=https://example.invalid"></head><body><a href="https://example.invalid">link</a><iframe srcdoc="danger"></iframe><form action="https://example.invalid"><button>Submit</button></form><h1>Safe</h1></body></html>')
    expect(output).toContain('<style>h1{color:red}</style>')
    expect(output).toContain('<h1>Safe</h1>')
    expect(output).not.toMatch(/http-equiv="refresh"|<iframe|<form|href=|action=/)
    expect(output).not.toContain('https://example.invalid')
  })

  it('recognizes unfinished HTML fences in partial output but not ordinary text', () => {
    expect(extractIntelligenceHtml('Partial:\n```html\n<div>hello')).toBe('<div>hello')
    expect(extractIntelligenceHtml('A plain answer')).toBeNull()
  })
})
