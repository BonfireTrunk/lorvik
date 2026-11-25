<script setup lang="ts">
import { computed, ref } from 'vue'
import { cardActionState, commentCard, transitionCard } from '../../composables/useCardActions'
import { useDropForm } from '../../composables/useDropForm'
import { useModalDialog } from '../../composables/useModalDialog'
import { useFoldForm } from '../../composables/useFoldForm'
import DropFields from './DropFields.vue'
import FoldFields from './FoldFields.vue'
import { sendOnModEnter } from '../../lib/sendKey'
import { isFoldOptional } from '../../lib/cardActions'
import type { Card } from '../../lib/types'
import MarkdownHint from '../MarkdownHint.vue'

const props = defineProps<{ kind: 'drop' | 'fold' | 'comment'; card: Card }>()
const emit = defineEmits<{ close: [] }>()

const dropForm = useDropForm(() => props.card)
const { fields: dropFields, ready: dropReady, showCascade } = dropForm
const foldOptional = computed(() => isFoldOptional(props.card.kind))
const foldForm = useFoldForm(() => foldOptional.value)
const { fields: foldFields, canSubmit: foldCanSubmit } = foldForm
const text = ref('')

const title = computed(() => {
  const what = props.kind === 'drop' ? 'Drop' : props.kind === 'fold' ? (foldOptional.value ? 'Done' : 'Close story') : 'Comment on'
  return `${what} #${props.card.number}`
})
const ready = computed(() => {
  if (props.kind === 'fold') return foldCanSubmit.value
  if (props.kind === 'comment') return text.value.trim() !== ''
  return dropReady.value
})

async function submit(): Promise<void> {
  if (!ready.value || cardActionState.busy) return
  let ok: boolean
  if (props.kind === 'comment') {
    ok = await commentCard(props.card, text.value.trim())
  } else if (props.kind === 'fold') {
    const body = foldForm.doneBody()
    if (!body) return
    ok = await transitionCard(props.card, body)
  } else {
    const body = dropForm.body()
    if (!body) return
    ok = await transitionCard(props.card, body)
  }
  if (ok) emit('close')
}

const onTextKeydown = sendOnModEnter(() => ready.value && !cardActionState.busy, () => void submit())

const { dialog: dlg, dialogEvents } = useModalDialog(() => emit('close'))
</script>

<template>
  <dialog
    ref="dlg"
    class="float m-auto w-full max-w-md p-5 text-fg backdrop:bg-scrim"
    :aria-label="title"
    v-on="dialogEvents"
  >
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <h2 class="page-title">{{ title }}</h2>
        <p class="meta truncate">{{ card.title }}</p>
      </div>
      <DropFields v-if="kind === 'drop'" v-model="dropFields" id-prefix="ap-drop" autofocus :show-cascade="showCascade" />
      <FoldFields v-else-if="kind === 'fold'" v-model="foldFields" id-prefix="ap-fold" autofocus />
      <div v-else>
        <label class="label" for="ap-text">Comment</label>
        <textarea id="ap-text" v-model="text" class="field" rows="4" autofocus aria-describedby="ap-text-hint" @keydown="onTextKeydown"></textarea>
        <MarkdownHint id="ap-text-hint">Ctrl+Enter sends.</MarkdownHint>
      </div>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn" @click="emit('close')">Cancel</button>
        <button :class="kind === 'drop' ? 'btn btn-danger' : 'btn btn-primary'" :disabled="!ready || cardActionState.busy">
          {{ kind === 'drop' ? 'Drop card' : kind === 'fold' ? (foldOptional ? 'Mark done' : 'Close story') : 'Comment' }}
        </button>
      </div>
    </form>
  </dialog>
</template>
