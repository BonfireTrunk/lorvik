import { reactive } from 'vue'
import { clearActors, record } from './actors'
import { clearCardCache } from './cardCache'
import { resetInbox, useOrg } from './inbox'
import { readStored, removeStored, storedKeys, writeStored } from './storage'
import type { Me } from './types'
import { resetWorking } from './working'

const KEY = 'lorvik.token'

export const auth = reactive<{ token: string; me: Me | null }>({ token: readStored(KEY), me: null })

// The storage key for a per-org setting of this browser; project slugs and the choices made on them are unique only inside an org.
// Empty while the org is not known (GET /me failed): a value kept then would sit where every org reads it.
function orgKey(name: string): string {
  const id = auth.me?.org.id
  return id ? `lorvik.${id}.${name}` : ''
}

/** A per-org setting of this browser, or '' when it is not set or the org is not known. */
export function readOrgStored(name: string): string {
  const key = orgKey(name)
  return key ? readStored(key) : ''
}

/** Keeps a per-org setting of this browser; dropped while the org is not known. */
export function writeOrgStored(name: string, value: string): void {
  const key = orgKey(name)
  if (key) writeStored(key, value)
}

const UNSCOPED = ['lorvik.project', 'lorvik.board.hidden']
const UNSCOPED_PREFIXES = ['lorvik.view.', 'lorvik.map.filters.']

/** Before orgs, these keys were shared by every org; the first org signed in to takes the values. */
function adoptUnscopedKeys(orgId: string): void {
  const old = [...UNSCOPED, ...UNSCOPED_PREFIXES.flatMap((p) => storedKeys(p))]
  for (const key of old) {
    const value = readStored(key)
    if (!value) continue
    const scoped = `lorvik.${orgId}.${key.slice('lorvik.'.length)}`
    if (!readStored(scoped)) writeStored(scoped, value)
    removeStored(key)
  }
}

export function setSession(token: string, me: Me): void {
  adoptUnscopedKeys(me.org.id)
  auth.token = token
  auth.me = me
  writeStored(KEY, token)
  useOrg(me.org.id)
  record({ users: me.user ? [me.user] : [], agents: me.agent ? [me.agent] : [] })
}

const HANDOFF_COOKIE = 'lorvik_handoff'
// Over https the server names it __Host-, which only this exact host can set. The plain name is then
// ignored: a sibling subdomain could have planted it to sign the visitor in to its own account.
const HANDOFF_HOST_COOKIE = '__Host-' + HANDOFF_COOKIE

/** The token secret the GitHub callback left in the handoff cookie, or '' when there is none. */
export function parseHandoff(cookies: string, secure: boolean): string {
  const wanted = secure ? HANDOFF_HOST_COOKIE : HANDOFF_COOKIE
  for (const part of cookies.split(';')) {
    const [name, ...value] = part.trim().split('=')
    if (name === wanted) return decodeURIComponent(value.join('='))
  }
  return ''
}

/** Reads the handoff cookie and expires it. */
export function takeHandoff(): string {
  const secure = window.location.protocol === 'https:'
  const secret = parseHandoff(document.cookie, secure)
  if (secret)
    document.cookie = secure
      ? `${HANDOFF_HOST_COOKIE}=; Path=/; Max-Age=0; Secure; SameSite=Strict`
      : `${HANDOFF_COOKIE}=; Path=/login; Max-Age=0; SameSite=Strict`
  return secret
}

/** Replaces the stored secret after the server rotated it (X-Lorvik-Token). */
export function replaceToken(token: string): void {
  if (!token || token === auth.token) return
  auth.token = token
  writeStored(KEY, token)
}

export function clearSession(): void {
  auth.token = ''
  auth.me = null
  resetInbox()
  resetWorking()
  clearActors()
  clearCardCache()
  removeStored(KEY)
}
