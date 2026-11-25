import { reactive } from 'vue'
import { ApiError, api, errorText } from '../lib/api'
import { CARD_ACTIONS, cardActions, type CardActionId } from '../lib/cardActions'
import { allowedMoves, stateWord } from '../lib/cards'
import { canIn } from '../lib/roles'
import type { Card, TransitionCard } from '../lib/types'
import { useAction } from './useAction'
import { useKeymap, type KeyLayer } from './useKeymap'

export interface CardTarget {
  slug: string
  number: number
}

export interface Toast {
  text: string
  error?: boolean
}

const { busy, run } = useAction()

/**
 * Shared by every place a card action starts (keys on the board and in the
 * panel, the context menu): the open prompt, the last outcome, and `changed`,
 * which a card view watches to reload after a move it did not make itself.
 */
export const cardActionState = reactive({
  busy,
  prompt: null as null | { kind: 'drop' | 'fold' | 'comment'; card: Card },
  toast: null as Toast | null,
  changed: { key: '', tick: 0 },
})

function say(text: string, error = false): void {
  cardActionState.toast = { text, error }
}

function fail(n: number, e: unknown): void {
  const { detail, hint } = errorText(e)
  say(`#${n}: ${detail}${hint ? ` (${hint})` : ''}`, true)
}

function markChanged(card: Card): void {
  cardActionState.changed = { key: `${card.project}/${card.number}`, tick: cardActionState.changed.tick + 1 }
}

/**
 * Moves a card through the API and throws on failure. Done on a task or
 * question that is not working starts it first (which claims it), as the
 * transition bar and `lorvik done` do.
 */
export async function moveCard(card: Card, body: Omit<TransitionCard, 'version'>): Promise<void> {
  let version = card.version
  const plain = card.kind !== 'story' && card.kind !== 'decision'
  if (body.status === 'done' && plain && !allowedMoves(card.kind, card.status).includes('done')) {
    version = (await api.transition(card.project, card.number, { version, status: 'working' })).version
  }
  await api.transition(card.project, card.number, { version, ...body })
}

/**
 * Moves a card and reports the outcome in the shared toast. A 409 means the
 * card changed under us: it is reloaded.
 */
export async function transitionCard(card: Card, body: Omit<TransitionCard, 'version'>): Promise<boolean> {
  return run(
    async () => {
      await moveCard(card, body)
      // A move shows itself (the tile changes column, the panel its status), so success stays silent.
      markChanged(card)
    },
    {
      onError: (e) => {
        fail(card.number, e)
        // A stale version or invalid transition: show the problem, then refresh the card.
        if (e instanceof ApiError && e.isConflict) markChanged(card)
      },
    },
  )
}

export async function commentCard(card: Card, body: string): Promise<boolean> {
  return run(
    async () => {
      await api.createComment(card.project, card.number, body)
      say(`Commented on #${card.number}`)
    },
    { onError: (e) => fail(card.number, e) },
  )
}

/**
 * Runs one action on a card. The card is read first, so the move is checked
 * against its current status and sent with its current version, whatever a
 * board tile showed; the server stays the authority.
 */
export async function runCardAction(id: CardActionId, t: CardTarget): Promise<void> {
  if (cardActionState.busy) return
  let read: Card | undefined
  const ok = await run(
    async () => {
      read = await api.card(t.slug, t.number)
    },
    { onError: (e) => fail(t.number, e) },
  )
  if (!ok || !read) return
  const card = read
  const a = cardActions(card.kind, card.status, canIn(card.project, 'member'), !!card.parent).find((x) => x.id === id)
  if (!a) {
    const what = CARD_ACTIONS.find((x) => x.id === id)!.label.replace('…', '')
    say(`#${card.number}: ${what} is not offered on a ${card.kind} that is ${stateWord(card.kind, card.status, card.drop_reason, card.decision)}`, true)
    return
  }
  if (a.prompt) {
    cardActionState.prompt = { kind: a.prompt, card }
    return
  }
  await transitionCard(card, { status: a.to!, drop_reason: a.reason })
}

/**
 * Binds the action keys for as long as the calling component is mounted.
 * `target` names the card they act on, or null when none is focused; `hint`
 * then tells the viewer how to focus one.
 */
export function useCardActionKeys(layer: KeyLayer, target: () => CardTarget | null, hint = 'Focus a card first: j and k walk the board'): void {
  useKeymap(
    CARD_ACTIONS.filter((a) => a.key).map((a) => ({
      keys: a.key,
      label: a.label.replace('…', ''),
      group: 'Card',
      layers: [layer],
      run: () => {
        const t = target()
        if (t) void runCardAction(a.id, t)
        else say(hint, true)
      },
    })),
  )
}
