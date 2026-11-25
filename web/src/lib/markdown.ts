import MarkdownIt from 'markdown-it'
import type { Token } from 'markdown-it'
import { router } from '../router'
import { cardHref } from './panelLinks'

// Rendering is sanitised by construction: html is false, so raw tags in the
// source are escaped as text, and markdown-it's validateLink rejects
// javascript:, vbscript:, file: and non-image data: URLs. The output is built
// from tokens, never from concatenated source text.
const md = new MarkdownIt({ html: false, linkify: true, breaks: true })

const REF = /\[\[(card|note):(?:([a-z0-9][a-z0-9-]{0,62})\/)?([A-Za-z0-9][A-Za-z0-9_-]{0,63})\]\]/y

interface Env {
  project?: string
}

function refHref(kind: string, prefix: string | undefined, key: string, project?: string): string | undefined {
  const numeric = /^\d+$/.test(key)
  if (kind === 'card') {
    const slug = prefix ?? project
    return numeric && slug ? cardHref(router, slug, Number(key)) : undefined
  }
  const scope = prefix ?? project
  return router.resolve({
    name: 'note',
    params: { noteKey: key },
    query: scope ? { project: scope } : {},
  }).href
}

// [[card:N]] and [[note:slug]] become in-app links. A
// reference that cannot be resolved (a bare card reference in text with no project)
// stays plain text.
md.inline.ruler.before('link', 'lorvik_ref', (state, silent) => {
  if (state.src.charCodeAt(state.pos) !== 0x5b || state.src.charCodeAt(state.pos + 1) !== 0x5b) return false
  REF.lastIndex = state.pos
  const m = REF.exec(state.src)
  if (!m) return false
  const href = refHref(m[1] ?? '', m[2], m[3] ?? '', (state.env as Env).project)
  if (!href) return false
  if (!silent) {
    const open = state.push('link_open', 'a', 1)
    open.attrs = [['href', href], ['data-route', '1']]
    state.push('text', '', 0).content = m[0]
    state.push('link_close', 'a', -1)
  }
  state.pos += m[0].length
  return true
})

const defaultLinkOpen =
  md.renderer.rules.link_open ?? ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options))

md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const t: Token = tokens[idx]!
  if (t.attrIndex('data-route') < 0) {
    t.attrSet('target', '_blank')
    t.attrSet('rel', 'noopener noreferrer')
  }
  return defaultLinkOpen(tokens, idx, options, env, self)
}

// renderMarkdown returns sanitised HTML. project scopes bare [[card:N]] and
// [[note:slug]] references; leave it empty for text with no project.
export function renderMarkdown(text: string, project?: string): string {
  return md.render(text, { project } satisfies Env)
}

function inlineText(children: Token[]): string {
  let out = ''
  for (const c of children) {
    if (c.type === 'text' || c.type === 'code_inline') out += c.content
    else if (c.type === 'softbreak' || c.type === 'hardbreak') out += ' '
    else if (c.type === 'image') out += inlineText(c.children ?? [])
  }
  return out
}

// markdownText is the readable text of markdown, one line per block, for
// places that cannot hold HTML: tooltips, labels and one-line previews.
export function markdownText(text: string): string {
  const lines: string[] = []
  for (const t of md.parse(text, {} satisfies Env)) {
    if (t.type === 'inline') lines.push(inlineText(t.children ?? []))
    else if (t.type === 'fence' || t.type === 'code_block') lines.push(...t.content.split('\n'))
  }
  return lines.map((l) => l.trim()).filter((l) => l !== '').join('\n')
}
