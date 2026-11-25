import { computed, ref } from 'vue'
import type { Fold, TransitionCard } from '../lib/types'

export interface FoldFormFields {
  path: string
  none: boolean
}

/** The fold a story or done card is closed with, or null while it names neither a path nor no change. */
export function foldBody(f: FoldFormFields): Fold | null {
  if (f.none) return { none: true }
  const into = f.path.trim()
  return into ? { into } : null
}

/** `optional` says whether the fold may be left empty, which closes the card unfolded. */
export function useFoldForm(optional: () => boolean) {
  const fields = ref<FoldFormFields>({ path: '', none: false })
  const canSubmit = computed(() => optional() || foldBody(fields.value) !== null)

  /** The move to done with this fold; an optional fold left empty closes the card unfolded, a required one yields null. */
  function doneBody(): Omit<TransitionCard, 'version'> | null {
    const fold = foldBody(fields.value)
    if (fold) return { status: 'done', fold }
    return optional() ? { status: 'done' } : null
  }

  function reset(): void {
    fields.value = { path: '', none: false }
  }

  return { fields, canSubmit, doneBody, reset }
}
