// The lockdown document that precedes every agent page in its sandboxed frame.
// The dashboard keeps the sign-in token in localStorage, so agent-written script
// must never share its origin: the iframe is sandboxed without allow-same-origin
// (opaque origin), and this prefix narrows what that opaque document may load.

const LIBRARY_HOSTS = 'https://cdnjs.cloudflare.com https://cdn.jsdelivr.net https://unpkg.com'

// A srcdoc document inherits the dashboard's CSP and adds this one; a leading
// meta lands in the implied head, before any agent markup, where it applies.
const POLICY = [
  // Nothing loads unless a directive below allows it.
  "default-src 'none'",
  // Inline script is the page itself; libraries come from three CDNs only.
  `script-src 'unsafe-inline' ${LIBRARY_HOSTS}`,
  `style-src 'unsafe-inline' ${LIBRARY_HOSTS}`,
  // Images are embedded in the page, never fetched.
  'img-src data: blob:',
  // System fonts only.
  "font-src 'none'",
  // No fetch, XHR, WebSocket or beacon: the page cannot send anything out.
  "connect-src 'none'",
  // WebRTC is outside connect-src. Browsers that know this directive refuse
  // peer connections; Chrome 156 ignores it, so there a STUN lookup still
  // reaches out, an accepted channel like the frame navigating itself.
  "webrtc 'block'",
  "media-src 'none'",
  "frame-src 'none'",
  "worker-src 'none'",
  "object-src 'none'",
  "form-action 'none'",
  // A <base> could redirect relative URLs of later markup.
  "base-uri 'none'",
].join('; ')

// The only code of ours in the frame: it reports the document height so the
// parent can size the frame (a sandboxed frame cannot be measured from outside).
// It sends one message shape and receives nothing. The body's bottom margin
// collapses through <html>, so it is added to the measured height.
const HEIGHT_SCRIPT = `<script>(function(){var d=document.documentElement;function s(){var b=document.body,h=d.getBoundingClientRect().height;if(b){h=Math.max(h,b.getBoundingClientRect().bottom+(parseFloat(getComputedStyle(b).marginBottom)||0)+scrollY)}parent.postMessage({lorvikPageHeight:Math.ceil(h)},'*')}addEventListener('load',s);var o=new ResizeObserver(s);o.observe(d);addEventListener('DOMContentLoaded',function(){if(document.body)o.observe(document.body)})})()</script>`

export const PAGE_FRAME_PREFIX = `<meta http-equiv="Content-Security-Policy" content="${POLICY}">${HEIGHT_SCRIPT}`

export const PAGE_MIN_HEIGHT = 240
export const PAGE_MAX_HEIGHT = 4000

export function pageSrcdoc(html: string): string {
  return PAGE_FRAME_PREFIX + html
}

/** The height a frame message reports, or null for anything but exactly { lorvikPageHeight: finite number }. */
export function reportedHeight(data: unknown): number | null {
  if (typeof data !== 'object' || data === null || Array.isArray(data)) return null
  const keys = Object.keys(data)
  if (keys.length !== 1 || keys[0] !== 'lorvikPageHeight') return null
  const h = (data as Record<string, unknown>).lorvikPageHeight
  if (typeof h !== 'number' || !Number.isFinite(h)) return null
  return Math.min(PAGE_MAX_HEIGHT, Math.max(PAGE_MIN_HEIGHT, Math.ceil(h)))
}

export function pageSize(bytes: number): string {
  return bytes < 1024 ? `${bytes} B` : bytes < 1024 * 1024 ? `${Math.floor(bytes / 1024)} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
