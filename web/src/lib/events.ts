import { rightsInWords } from './rights'
import type { LorvikEvent } from './types'

type P = Record<string, unknown>

const str = (p: P, k: string): string => {
  const v = p[k]
  return typeof v === 'string' ? v : typeof v === 'number' ? String(v) : ''
}
const list = (p: P, k: string): string[] => {
  const v = p[k]
  return Array.isArray(v) ? v.map(String) : []
}
const quote = (s: string): string => (s ? ` "${s}"` : '')
const hasRights = (p: P): boolean => Array.isArray(p.rights)
// Agent events carry rights, possibly none; events stored before rights carry a role.
const noRights = 'no rights beyond reading'
const rightsGiven = (p: P): string => (list(p, 'rights').length ? `${rightsInWords(list(p, 'rights'))} rights` : noRights)
const access = (p: P): string => (hasRights(p) ? `with ${rightsGiven(p)}` : `as ${str(p, 'role')}`)
const inSession =(s: string): string => (s ? ` in session ${s}` : '')
const identity = (p: P): string =>
  str(p, 'provider') === 'github' ? `GitHub account ${str(p, 'login')}` : `${str(p, 'provider')} sign-in ${str(p, 'login')}`

/**
 * One sentence for an event, without the actor. `name` resolves an actor id to
 * a display name. Unknown types render as the type plus the subject.
 */
export function describeEvent(e: LorvikEvent, name: (id: string) => string, here = false): string {
  const p: P = e.payload ?? {}
  const title = str(p, 'title')
  const n = e.subject.number
  // here: the event is shown on its own card, so the card is not named again.
  const card = here ? 'this card' : `#${n ?? '?'}${quote(title)}`
  const who = (k: string): string => {
    const a = p[k]
    if (typeof a === 'object' && a !== null && typeof (a as { id?: unknown }).id === 'string') {
      return name((a as { id: string }).id)
    }
    return ''
  }

  const forWho = who('for') ? ` for ${who('for')}` : ''

  switch (e.type) {
    case 'card.created':
      return `created ${str(p, 'kind') || 'card'} ${card}${str(p, 'status') ? ` as ${str(p, 'status')}` : ''}`
    case 'card.updated': {
      const f = list(p, 'fields')
      return `edited ${card}${f.length ? ` (${f.join(', ')})` : ''}`
    }
    case 'card.status_changed': {
      const extra = str(p, 'blocker_title') ? ` (blocked by #${str(p, 'blocked_by')} "${str(p, 'blocker_title')}")` : ''
      let why = ''
      if (str(p, 'superseded_by')) why = ` (superseded by #${str(p, 'superseded_by')})`
      else if (str(p, 'by_child')) why = ` (a child, #${str(p, 'by_child')}, started)`
      else if (str(p, 'cascade_of')) why = ` (dropped with story #${str(p, 'cascade_of')})`
      if (str(p, 'kind') === 'decision') {
        const verdict: Record<string, string> = { done: 'accepted', ready: 're-proposed' }
        const to = str(p, 'to')
        const word = to === 'dropped' ? (str(p, 'superseded_by') ? 'superseded' : str(p, 'from') === 'done' ? 'revoked' : 'rejected') : (verdict[to] ?? `moved to ${to}`)
        return `${word} ${here ? 'this decision' : `decision ${card}`}${forWho}${why}`
      }
      return `moved ${str(p, 'kind') === 'story' && !here ? 'story ' : ''}${card} from ${str(p, 'from')} to ${str(p, 'to')}${extra}${why}`
    }
    case 'card.claimed': {
      const from = who('taken_from') && who('taken_from') + inSession(str(p, 'taken_from_session'))
      return `claimed ${card}${from ? ` (taken over from ${from})` : ''}`
    }
    case 'card.released': {
      const from = who('released_from') && who('released_from') + inSession(str(p, 'released_from_session'))
      return from ? `released the claim of ${from} on ${card}` : `released ${card}`
    }
    case 'card.claim_ending':
      return `is about to lose the claim on ${card}${inSession(str(p, 'session'))} (lease ends soon)`
    case 'card.claim_expired':
      return `lost the claim on ${card}${inSession(str(p, 'session'))} (lease expired)`
    case 'card.answered': {
      const answer = str(p, 'answer') ? `: ${str(p, 'answer')}` : ''
      const prev = str(p, 'previous')
      const decided = p.decided === true ? ' and recorded a decision' : ''
      if (p.relayed === true) return `relayed an answer on ${card}${answer}`
      if (p.confirmed === true && prev) return `changed the answer on ${card}${answer} (was ${prev})${decided}`
      if (p.confirmed === true) return `confirmed the answer on ${card}${decided}`
      return `answered ${card}${forWho}${decided}`
    }
    case 'card.kept':
      return `kept the ${str(p, 'kind') === 'decision' ? 'verdict' : 'answer'} on ${card}`
    case 'card.for_person':
      return p.set === false ? `cleared the for-a-person mark on ${card}` : `marked ${card} for a person`
    case 'card.linked':
      return `linked ${card} ${str(p, 'kind')} #${str(p, 'to')}${quote(str(p, 'to_title'))}`
    case 'card.unlinked':
      return `unlinked ${card} ${str(p, 'kind')} #${str(p, 'to')}${quote(str(p, 'to_title'))}`
    case 'card.comment':
      return `commented on ${card}`
    case 'card.folded':
      return `folded ${card} ${str(p, 'into') ? `into ${str(p, 'into')}` : 'with no contract change'}`
    case 'note.created':
      return `created note${quote(title)}`
    case 'note.updated':
      return str(p, 'status') === 'archived' ? `archived note${quote(title)}` : `updated note${quote(title)}`
    case 'project.created':
      return `created project${quote(title)}`
    case 'project.updated':
      return `updated project${quote(title)}`
    case 'project.archived':
      return `archived project${quote(title)}`
    case 'agent.registered':
      return `registered agent${quote(title)}`
    case 'agent.approved':
      return `approved agent${quote(title)}`
    case 'agent.revoked':
      return `revoked agent${quote(title)}`
    case 'agent.restored':
      return `restored agent${quote(title)}${str(p, 'status') === 'pending' ? ' (pending approval)' : ''}`
    case 'agent.removed':
      return `removed agent${quote(title)}`
    case 'agent.join_requested':
      return `asked to join ${str(p, 'project')}`
    case 'agent.joined':
      return p.auto === true
        ? `joined ${str(p, 'project')} ${access(p)}, allowed by its owner's settings`
        : `let agent${quote(title)} into ${str(p, 'project')} ${access(p)}`
    case 'agent.updated':
      return `set the join setting of agent${quote(title)} to ${str(p, 'agent_join')}`
    case 'agent.join_denied':
      return `denied agent${quote(title)} in ${str(p, 'project')}`
    case 'agent.join_setting':
      return p.project
        ? `set their agents' joining in ${str(p, 'project')} to ${str(p, 'agent_join')}`
        : `set their agents' joining to ${str(p, 'agent_join')}`
    case 'agent.session_rights': {
      const rights = list(p, 'rights')
      const chat = `chat ${str(p, 'session')} of agent${quote(title)}`
      return rights.length
        ? `gave ${chat} ${rightsInWords(rights)} in ${str(p, 'project')}`
        : `took back the rights of ${chat} in ${str(p, 'project')}`
    }
    case 'membership.granted':
    case 'membership.changed': {
      const granted = e.type === 'membership.granted'
      const to = who('actor') || 'a member'
      const where = str(p, 'project') ? ` in ${str(p, 'project')}` : ''
      if (hasRights(p)) return granted ? `granted ${rightsGiven(p)} to ${to}${where}` : `changed the rights of ${to}${where} to ${list(p, 'rights').length ? rightsInWords(list(p, 'rights')) : noRights}`
      const what = str(p, 'role') ? `${str(p, 'role')} to ` : ''
      return `${granted ? 'granted' : 'changed the role of'} ${what}${to}${where}`
    }
    case 'membership.requested':
      return 'asked to join the org'
    case 'membership.invited': {
      const to = who('actor') || str(p, 'login')
      return `invited ${to || 'someone'}${str(p, 'role') ? ` as ${str(p, 'role')}` : ''}`
    }
    case 'membership.removed': {
      const where = str(p, 'project')
      return `removed ${who('actor') || 'a member'}${where ? ` from ${where}` : ' from the org'}`
    }
    case 'identity.linked':
      return `linked ${identity(p)} to their account`
    case 'identity.unlinked':
      return `unlinked ${identity(p)} from their account`
    case 'user.deleted':
      return 'deleted their account'
    case 'identity.signin_failed':
      return 'had a wrong password entered for their login'
    case 'identity.signin_refused':
      return 'had repeated wrong passwords entered for their login, up to a sign-in limit; tries over it are refused for now'
    case 'token.rotated':
      return `token ${str(p, 'prefix')} was rotated`
    case 'token.reuse_detected':
      return `token ${str(p, 'prefix')}: an old secret was used after rotation`
    case 'lock.acquired':
      return `took lock ${str(p, 'name')} for${quote(str(p, 'purpose'))}`
    case 'lock.released': {
      const holder = who('holder')
      return p.forced === true
        ? `force-released lock ${str(p, 'name')}${holder ? ` (held by ${holder})` : ''}`
        : `released lock ${str(p, 'name')}`
    }
    case 'lock.expired': {
      const holder = who('holder')
      return `lock ${str(p, 'name')} expired${holder ? ` (held by ${holder})` : ''}`
    }
    default:
      return `${e.type} ${e.subject.type} ${n ? `#${n}` : e.subject.id}`
  }
}
