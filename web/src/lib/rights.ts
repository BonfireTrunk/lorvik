import { canIn, canOrg } from './roles'
import type { AgentRight, Role } from './types'

export interface RightInfo {
  value: AgentRight
  label: string
  tip: string
  /** The role in the project the giver needs to hold. */
  needs: Role
}

/** Agent rights in display order. */
export const RIGHTS: RightInfo[] = [
  { value: 'work', label: 'Work', tip: 'Claim cards, work and finish them, file cards, comment.', needs: 'member' },
  { value: 'ready', label: 'Ready', tip: 'Move a backlog card to ready.', needs: 'reviewer' },
  { value: 'triage', label: 'Triage', tip: "Edit and drop other actors' cards.", needs: 'reviewer' },
  { value: 'answer', label: 'Answer', tip: 'Answer questions like a person.', needs: 'reviewer' },
  { value: 'decide', label: 'Decide', tip: "Accept or reject a proposed decision for the agent's owner, never one its own chat proposed.", needs: 'reviewer' },
]

/** True when the signed-in user may give `right` in `slug`, or org-wide when `slug` is undefined. */
export function canGiveRight(slug: string | undefined, right: AgentRight): boolean {
  const info = RIGHTS.find((r) => r.value === right)
  return !!info && (slug === undefined ? canOrg(info.needs) : canIn(slug, info.needs))
}

/** Rights in display order, whatever order they came in. */
export function sortRights(rights: readonly string[] | undefined): AgentRight[] {
  return RIGHTS.map((r) => r.value).filter((v) => rights?.includes(v))
}

/** "work", "work and ready", "work, ready and answer". */
export function rightsInWords(rights: readonly string[] | undefined): string {
  const words = sortRights(rights)
  if (words.length < 2) return words[0] ?? 'nothing'
  return `${words.slice(0, -1).join(', ')} and ${words[words.length - 1]}`
}

export interface RightLock {
  slug: string | undefined
  modelValue: readonly string[]
  inherited?: readonly string[]
  required?: boolean
  disabled?: boolean
}

/** True when `right` cannot be changed: the control is disabled, the agent holds it, the giver may not give it, or it is the last one of a required set. */
export function isRightLocked(c: RightLock, right: AgentRight): boolean {
  if (c.disabled || c.inherited?.includes(right)) return true
  if (!canGiveRight(c.slug, right)) return true
  return !!c.required && c.modelValue.length === 1 && c.modelValue[0] === right
}

/** The set after `right` is checked or unchecked, in display order. */
export function toggledRights(modelValue: readonly string[], right: AgentRight, on: boolean): AgentRight[] {
  return sortRights(on ? [...modelValue, right] : modelValue.filter((r) => r !== right))
}
