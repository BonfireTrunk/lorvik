import { reactive } from 'vue'
import type { Actor, Actors, Behalf } from './types'

type Loader = (ids: string[]) => Promise<Actors>

const CHUNK = 100

// id -> display name. Resolved once per session; ids the server does not know
// are remembered in `tried` so they are not asked for again.
const names = reactive<Record<string, string>>({})
const tried = new Set<string>()
const queue = new Set<string>()
let loader: Loader | undefined
let scheduled = false

/** Wired by lib/api.ts so this module needs no import of the client. */
export function setActorLoader(fn: Loader): void {
  loader = fn
}

function isActor(v: unknown): v is Actor {
  if (typeof v !== 'object' || v === null) return false
  const o = v as Record<string, unknown>
  return (o.kind === 'user' || o.kind === 'agent') && typeof o.id === 'string'
}

/** Walks a response body and returns every actor id found in it. */
function collectActorIds(root: unknown, out: Set<string> = new Set(), depth = 0): Set<string> {
  if (depth > 8 || typeof root !== 'object' || root === null) return out
  if (Array.isArray(root)) {
    for (const v of root) collectActorIds(v, out, depth + 1)
    return out
  }
  if (isActor(root)) out.add(root.id)
  for (const v of Object.values(root)) collectActorIds(v, out, depth + 1)
  return out
}

async function flush(): Promise<void> {
  scheduled = false
  if (!loader) return
  const ids = [...queue]
  queue.clear()
  for (let i = 0; i < ids.length; i += CHUNK) {
    const part = ids.slice(i, i + CHUNK)
    try {
      record(await loader(part))
    } catch {
      // names are cosmetic: fall back to ids, and allow a retry later
      for (const id of part) tried.delete(id)
    }
  }
}

/** Stores the names of a resolved Actors response in the cache. */
export function record(a: Actors): void {
  for (const u of a.users ?? []) names[u.id] = (u.name || u.login) + (u.status === 'deleted' ? ' (deleted)' : '')
  for (const g of a.agents ?? []) names[g.id] = g.name
}

/** Queues ids found in `root` (plus any extra plain ids) for one batched GET /actors. */
export function resolveActors(root: unknown, extra: Iterable<string> = []): void {
  const ids = collectActorIds(root)
  for (const e of extra) if (e) ids.add(e)
  for (const id of ids) {
    if (tried.has(id) || id in names) continue
    tried.add(id)
    queue.add(id)
  }
  if (queue.size && !scheduled) {
    scheduled = true
    setTimeout(() => void flush(), 0)
  }
}

/** Display name for an actor or a bare id; the id itself when unresolved. */
export function actorName(a: Actor | string | undefined | null): string {
  if (!a) return ''
  const id = typeof a === 'string' ? a : a.id
  return names[id] ?? id
}

/** Whether the name of an actor or bare id has arrived; false while `actorName` still shows the id. */
export function actorResolved(a: Actor | string | undefined | null): boolean {
  if (!a) return true
  return (typeof a === 'string' ? a : a.id) in names
}

/** A chat as people read it: "name (label)", or the bare label when the chat has no name. */
export function chatLabel(label: string, name?: string): string {
  return name ? `${name} (${label})` : label
}

/** The same chat inside brackets that already follow its holder's name: "name, label". */
export function holderChat(label: string, name?: string): string {
  return name ? `${name}, ${label}` : label
}

/** Who settled a card for a person: "claude-code@box (billing migration, calm-lynx-4aa4)", or the agent alone. */
export function behalfBy(b: Behalf): string {
  return b.session ? `${actorName(b.by)} (${holderChat(b.session, b.session_name)})` : actorName(b.by)
}

export function clearActors(): void {
  for (const k of Object.keys(names)) delete names[k]
  tried.clear()
  queue.clear()
}
