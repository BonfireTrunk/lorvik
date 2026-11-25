import { reactive } from 'vue'
import { allPages, api } from './api'
import { auth } from './auth'
import type { Actor, LiveClaim } from './types'

/** The org's live claims, as GET /claims lists them: by holder, then session, then card. */
export const working = reactive<{ claims: LiveClaim[]; loaded: boolean; now: number }>({
  claims: [],
  loaded: false,
  now: Date.now(),
})

export async function loadWorking(): Promise<void> {
  const token = auth.token
  const claims = await allPages((cursor) => api.liveClaims(cursor))
  if (token !== auth.token) return
  working.claims = claims
  working.now = Date.now()
  working.loaded = true
}

/** Moves the clock the list is judged by, so a lease that ran out drops off between events. */
export function tickWorking(): void {
  working.now = Date.now()
}

export function resetWorking(): void {
  working.claims = []
  working.loaded = false
}

/** The claims whose lease has not run out yet. */
export function liveClaims(): LiveClaim[] {
  return working.claims.filter((c) => Date.parse(c.expires_at) > working.now)
}

export interface WorkingGroup {
  key: string
  holder: Actor
  session: string
  sessionName?: string
  claims: LiveClaim[]
}

/** One group per holder and session, in the order the server sends them. */
export function groupClaims(claims: LiveClaim[]): WorkingGroup[] {
  const groups: WorkingGroup[] = []
  const byKey = new Map<string, WorkingGroup>()
  for (const c of claims) {
    const session = c.session ?? ''
    const key = `${c.holder.kind}:${c.holder.id}:${session}`
    let g = byKey.get(key)
    if (!g) {
      g = { key, holder: c.holder, session, sessionName: c.session_name, claims: [] }
      byKey.set(key, g)
      groups.push(g)
    }
    g.claims.push(c)
  }
  return groups
}

/** How many cards are being worked on in each project, by slug. */
export function countByProject(claims: LiveClaim[]): Record<string, number> {
  const counts: Record<string, number> = {}
  for (const c of claims) counts[c.card.project] = (counts[c.card.project] ?? 0) + 1
  return counts
}

function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`
}

/** "5 chats of 2 agents and 1 person working in 2 projects"; a chat is one holder's session, people and agents are counted by identity, as `lorvik who` does. */
export function workingSummary(claims: LiveClaim[]): string {
  if (!claims.length) return ''
  const chats = groupClaims(claims).length
  const holders = new Map(claims.map((c) => [`${c.holder.kind}:${c.holder.id}`, c.holder.kind]))
  const kinds = [...holders.values()]
  const agents = kinds.filter((k) => k === 'agent').length
  const people = kinds.length - agents
  const who = []
  if (agents) who.push(count(agents, 'agent', 'agents'))
  if (people) who.push(count(people, 'person', 'people'))
  const projects = new Set(claims.map((c) => c.card.project)).size
  return `${count(chats, 'chat', 'chats')} of ${who.join(' and ')} working in ${count(projects, 'project', 'projects')}`
}
