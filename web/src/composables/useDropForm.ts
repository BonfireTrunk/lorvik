import { computed, ref } from 'vue'
import type { Card, DropReason, TransitionCard } from '../lib/types'

export interface DropFormFields {
  reason: DropReason
  duplicate: string
  note: string
  cascade: boolean
}

export function useDropForm(card: () => Card) {
  const fields = ref<DropFormFields>({ reason: 'obsolete', duplicate: '', note: '', cascade: false })
  const duplicate = computed(() => Number(fields.value.duplicate.replace('#', '')))
  const ready = computed(() => fields.value.reason !== 'duplicate' || duplicate.value > 0)
  const showCascade = computed(() => {
    const c = card()
    return c.kind === 'story' && !!c.progress && c.progress.done < c.progress.total
  })

  function body(): Omit<TransitionCard, 'version'> | null {
    if (!ready.value) return null
    return {
      status: 'dropped',
      drop_reason: fields.value.reason,
      duplicate: fields.value.reason === 'duplicate' ? duplicate.value : undefined,
      note: fields.value.note.trim() || undefined,
      cascade: card().kind === 'story' && fields.value.cascade ? true : undefined,
    }
  }

  function clearAfterMove(): void {
    fields.value.note = ''
    fields.value.cascade = false
  }

  return { fields, ready, showCascade, body, clearAfterMove }
}
