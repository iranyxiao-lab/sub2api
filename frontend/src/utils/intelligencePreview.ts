export function extractIntelligenceHtml(response: string): string | null {
  const fenced = response.match(/```html\s*([\s\S]*?)(?:```|$)/i)?.[1]
  if (fenced !== undefined) return fenced
  return /^\s*(?:<!doctype\s+html|<(?:html|head|body|div|main|section|article|table|h[1-6]|p|style)(?:\s|>))/i.test(response) ? response : null
}

export function buildStaticHtmlPreview(response: string): string {
  // Sandbox and resource CSP do not prohibit self-navigation (links/meta refresh).
  // Strip navigation and nested documents as a separate layer of isolation.
  const html = DOMPurify.sanitize(extractIntelligenceHtml(response) ?? response, {
    WHOLE_DOCUMENT: true,
    FORBID_TAGS: ['script', 'iframe', 'frame', 'object', 'embed', 'form', 'meta', 'base', 'link'],
    FORBID_ATTR: ['href', 'xlink:href', 'action', 'formaction', 'srcdoc', 'srcset', 'ping', 'target']
  })
  // The iframe is sandboxed too; the CSP prevents external resources and form submissions.
  const policy = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; form-action 'none'; base-uri 'none'"
  return `<meta http-equiv="Content-Security-Policy" content="${policy}"><base target="_self">${html}`
}
import DOMPurify from 'dompurify'
