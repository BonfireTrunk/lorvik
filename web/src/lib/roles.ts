import { auth } from './auth'
import { awaitsHuman, humanActionWord } from './cards'
import { projects } from './projects'
import type { CardBrief, Role } from './types'

/** Roles from least to most privileged. */
export const ROLES: Role[] = ['viewer', 'member', 'reviewer', 'admin', 'owner']

const RANK: Record<string, number> = Object.fromEntries(ROLES.map((r, i) => [r, (i + 1) * 10]))

/** True when `have` is at least `need`. Unknown or empty roles rank below viewer. */
export function atLeast(have: string | undefined, need: Role): boolean {
  return (RANK[have ?? ''] ?? 0) >= (RANK[need] ?? Infinity)
}

/** The viewer's effective role in a project: the higher of the project role and the org role. */
function roleIn(slug: string | undefined): string {
  const org = auth.me?.role ?? ''
  const proj = slug ? (projects.list.find((p) => p.slug === slug)?.role ?? '') : ''
  return (RANK[proj] ?? 0) >= (RANK[org] ?? 0) ? proj : org
}

export function canIn(slug: string | undefined, need: Role): boolean {
  return atLeast(roleIn(slug), need)
}

/** Org-level role check, for org-wide decisions and admin pages. */
export function canOrg(need: Role): boolean {
  return atLeast(auth.me?.role, need)
}

/** Humans answer questions; agents cannot. */
export function isUser(): boolean {
  return auth.me?.actor.kind === 'user'
}

export function canHumanIn(slug: string | undefined, need: Role): boolean {
  return isUser() && canIn(slug, need)
}

/** What the viewer must do with the card in `slug` ("answer", "accept or reject"), or undefined when it waits on no one here. */
export function humanAsk(slug: string | undefined, c: CardBrief): string | undefined {
  return canHumanIn(slug, 'reviewer') && awaitsHuman(c, auth.me?.actor) ? humanActionWord(c.kind) : undefined
}
