export function buildStaticHtmlPreview(response: string): string {
  const html = response.match(/```html\s*([\s\S]*?)```/i)?.[1] ?? response
  // The iframe is sandboxed too; the CSP prevents external resources and form submissions.
  const policy = "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; form-action 'none'; base-uri 'none'"
  return `<meta http-equiv="Content-Security-Policy" content="${policy}"><base target="_self">${html}`
}
