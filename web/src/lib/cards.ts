import type { Actor, CardBrief, CardDecision, CardKind, CardStatus, DropReason } from './types'

export type DecisionWord = 'proposed' | 'accepted' | 'rejected' | 'revoked' | 'superseded'

export const STATUS_ORDER: CardStatus[] = ['backlog', 'ready', 'working', 'waiting', 'done', 'dropped']

/** The statuses of a card still open: everything but done and dropped. */
export const OPEN_STATUSES: CardStatus[] = ['backlog', 'ready', 'working', 'waiting']
export const DROP_REASONS = ['obsolete', 'duplicate', 'wontfix'] as const satisfies readonly DropReason[]

/**
 * The decision vocabulary over a decision card's state. Backlog, working and
 * waiting are not used by decisions; they fall through as their status word.
 * Without a drop_reason (cards from an older server) a dropped decision falls
 * back on its decision block: a successor means superseded, an acceptor (kept
 * on revoke) means revoked, neither means rejected.
 */
export function decisionWord(status: CardStatus, reason?: DropReason, decision?: CardDecision): string {
  if (status === 'ready') return 'proposed'
  if (status === 'done') return 'accepted'
  if (status !== 'dropped') return status
  if (reason === 'revoked' || reason === 'superseded' || reason === 'rejected') return reason
  if (decision?.superseded_by) return 'superseded'
  return decision?.accepted_by ? 'revoked' : 'rejected'
}

/** The word to show for a card's state: the decision word for decisions, else the status. */
export function stateWord(kind: CardKind, status: CardStatus, reason?: DropReason, decision?: CardDecision): string {
  return kind === 'decision' ? decisionWord(status, reason, decision) : status
}

/** What an agent did for a person on a card: answered a question, or accepted or rejected a decision. */
export function behalfVerb(kind: CardKind, status: CardStatus): string {
  if (kind !== 'decision') return 'Answered'
  return status === 'done' ? 'Accepted' : 'Rejected'
}

/** A card that is done or dropped. */
export function isClosed(status: CardStatus): boolean {
  return status === 'done' || status === 'dropped'
}

// The per-kind transition tables, mirroring internal/domain/card.go. The server
// stays the authority; these only decide which buttons to offer.
const TASK_MOVES: Record<CardStatus, CardStatus[]> = {
  backlog: ['ready', 'dropped'],
  ready: ['backlog', 'working', 'waiting', 'dropped'],
  working: ['ready', 'waiting', 'done', 'dropped'],
  waiting: ['ready', 'working', 'dropped'],
  done: ['ready'],
  dropped: ['ready'],
}
const STORY_MOVES: Record<CardStatus, CardStatus[]> = {
  backlog: ['ready', 'dropped'],
  ready: ['backlog', 'waiting', 'dropped', 'done'],
  working: ['ready', 'waiting', 'done', 'dropped'],
  waiting: ['ready', 'dropped', 'done'],
  done: [],
  dropped: [],
}
const DECISION_MOVES: Record<CardStatus, CardStatus[]> = {
  backlog: [],
  ready: ['done', 'dropped'],
  working: [],
  waiting: [],
  done: ['dropped'],
  dropped: ['ready'],
}

/** The statuses a card of this kind may move to from `status`. */
export function allowedMoves(kind: CardKind, status: CardStatus): CardStatus[] {
  if (kind === 'story') return STORY_MOVES[status]
  if (kind === 'decision') return DECISION_MOVES[status]
  return TASK_MOVES[status]
}

/**
 * A proposed decision or an open question waits on a human, unless it is
 * assigned to someone other than `me`.
 */
export function awaitsHuman(c: CardBrief, me?: Actor): boolean {
  if ((c.kind !== 'decision' && c.kind !== 'question') || c.status !== 'ready') return false
  return !c.assignee || (c.assignee.kind === me?.kind && c.assignee.id === me?.id)
}

/** What a human does with a card that awaits them. */
export function humanActionWord(kind: CardKind): string {
  return kind === 'decision' ? 'accept or reject' : 'answer'
}

function countLabel(kind: string, cards: CardBrief[]): string {
  return cards.length === 1 ? `${kind} #${cards[0]!.number}` : `${cards.length} ${kind}s`
}

/** What a story waits on, from the cards awaiting a human: "decision #91", "2 questions". */
export function storyWaitsOn(children: CardBrief[]): string {
  const decisions = children.filter((c) => c.kind === 'decision')
  const questions = children.filter((c) => c.kind === 'question')
  const parts: string[] = []
  if (decisions.length) parts.push(countLabel('decision', decisions))
  if (questions.length) parts.push(countLabel('question', questions))
  return parts.join(', ')
}
