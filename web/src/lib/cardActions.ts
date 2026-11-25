import { allowedMoves, isClosed } from './cards'
import type { CardKind, CardStatus, DropReason } from './types'

export type CardActionId = 'start' | 'ready' | 'wait' | 'backlog' | 'done' | 'drop' | 'comment' | 'accept' | 'reject' | 'revoke' | 'repropose'

export interface CardAction {
  id: CardActionId
  /** The card-action key (dashboard.md 2.9) that does the same move. */
  key: string
  label: string
  reopenLabel?: string
  foldLabel?: string
  reason?: DropReason
  /** The status it moves to; absent for a comment. */
  to?: CardStatus
  /** Asks for more before it runs: a drop reason, a fold, a comment's text. */
  prompt?: 'drop' | 'fold' | 'comment'
  /** The fold prompt may be submitted empty, closing the card unfolded; a story's may not. */
  foldOptional?: boolean
}

/** Every action in key order, for the help and for menus that list what is not offered. */
export const CARD_ACTIONS: CardAction[] = [
  { id: 'start', key: 's', label: 'Start', to: 'working' },
  { id: 'ready', key: 'r', label: 'Ready', reopenLabel: 'Reopen', to: 'ready' },
  { id: 'wait', key: 'w', label: 'Waiting', to: 'waiting' },
  { id: 'backlog', key: 'b', label: 'Backlog', to: 'backlog' },
  { id: 'done', key: 'd', label: 'Done', foldLabel: 'Done…', to: 'done' },
  { id: 'drop', key: 'x', label: 'Drop…', to: 'dropped', prompt: 'drop' },
  { id: 'comment', key: 'c', label: 'Comment…', prompt: 'comment' },
  { id: 'accept', key: '', label: 'Accept', to: 'done' },
  { id: 'reject', key: '', label: 'Reject', to: 'dropped', reason: 'rejected' },
  { id: 'revoke', key: '', label: 'Revoke', to: 'dropped', reason: 'revoked' },
  { id: 'repropose', key: '', label: 'Re-propose', to: 'ready' },
]

/** A task or question may be closed without a fold; a story must name one. */
export function isFoldOptional(kind: CardKind): boolean {
  return kind !== 'story'
}

function action(id: CardActionId): CardAction {
  return { ...CARD_ACTIONS.find((a) => a.id === id)! }
}

interface DecisionActions {
  canDecide: boolean
  isProposer: boolean
  dropReason?: DropReason
}

/**
 * The actions a card offers, from the per-kind table in allowedMoves and the
 * viewer's member role: the same moves the transition bar shows. A decision
 * offers only a comment unless its reviewer or proposer context is supplied,
 * since its verdicts need the deliberate buttons. A task or question in ready
 * or waiting offers Done, which starts it first. A story's Done asks for its
 * fold; so does a task or question's without a parent, optionally. Ready on a
 * closed card reads Reopen.
 */
export function cardActions(kind: CardKind, status: CardStatus, canMove: boolean, hasParent: boolean, decision?: DecisionActions): CardAction[] {
  if (kind === 'decision') {
    const out: CardAction[] = []
    // Accepting, rejecting and revoking a decision are for human reviewers; the server enforces it.
    if (status === 'ready' && decision?.canDecide) out.push(action('accept'), action('reject'))
    if (status === 'done' && decision?.canDecide) out.push(action('revoke'))
    if (status === 'dropped' && decision?.dropReason === 'rejected' && (decision.canDecide || decision.isProposer)) {
      out.push(action('repropose'))
    }
    if (canMove) out.push(action('comment'))
    return out
  }
  if (!canMove) return []
  const m = allowedMoves(kind, status)
  const out: CardAction[] = []
  if (m.includes('working')) out.push(action('start'))
  if (m.includes('ready')) {
    const ready = action('ready')
    out.push(isClosed(status) ? { ...ready, label: ready.reopenLabel! } : ready)
  }
  if (m.includes('waiting')) out.push(action('wait'))
  if (m.includes('backlog')) out.push(action('backlog'))
  if (kind === 'story') {
    if (m.includes('done')) {
      const done = action('done')
      out.push({ ...done, label: done.foldLabel!, prompt: 'fold' })
    }
  } else if (m.includes('done') || (m.includes('working') && !isClosed(status))) {
    const done = action('done')
    out.push(hasParent ? done : { ...done, label: done.foldLabel!, prompt: 'fold', foldOptional: isFoldOptional(kind) })
  }
  if (m.includes('dropped')) out.push(action('drop'))
  out.push(action('comment'))
  return out
}
