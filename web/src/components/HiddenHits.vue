<script setup lang="ts" generic="R extends { label: string }">
import { computed, nextTick, ref, useId, watch } from 'vue'
import type { HiddenHit } from '../composables/useHiddenHits'
import type { CardBrief } from '../lib/types'

// `what` says why they are not shown, as the end of "3 more ...". `none` is
// set when no match is shown, so the chip reads "3 hidden by filters".
const props = withDefaults(defineProps<{ hits: HiddenHit<R>[]; what?: string; none?: boolean }>(), { what: 'hidden by filters', none: false })
const emit = defineEmits<{ pick: [card: CardBrief] }>()

const open = ref(false)
const id = useId()
const chip = ref<HTMLButtonElement | null>(null)
const more = computed(() => (props.none ? '' : 'more '))
const heard = computed(() => (props.hits.length ? `${props.hits.length} ${more.value}${props.hits.length === 1 ? 'match' : 'matches'} ${props.what}` : ''))

watch(
  () => props.hits.length,
  (n) => {
    if (!n) open.value = false
  },
)

function close(): void {
  open.value = false
  void nextTick(() => chip.value?.focus())
}

function onEscape(e: KeyboardEvent): void {
  if (!open.value) return
  e.stopPropagation()
  e.preventDefault()
  close()
}

defineExpose({ focus: () => chip.value?.focus() })

function pick(card: CardBrief): void {
  open.value = false
  emit('pick', card)
}
</script>

<template>
  <div v-if="hits.length" class="pointer-events-auto flex w-full flex-col items-end gap-1.5" @keydown.esc="onEscape">
    <button
      ref="chip"
      type="button"
      class="float px-2 py-0.5 text-xs text-muted tabular hover:text-fg"
      :aria-expanded="open"
      :aria-controls="id"
      @click="open = !open"
    >
      {{ hits.length }} {{ more }}{{ what }}
    </button>
    <p class="sr-only" role="status">{{ heard }}</p>
    <section v-if="open" :id="id" class="float w-full overflow-hidden" :aria-label="`Matches ${what}`">
      <ul class="max-h-64 divide-y divide-line overflow-y-auto text-sm">
        <li v-for="h in hits" :key="h.card.number">
          <button type="button" class="flex w-full items-start gap-2 px-3 py-1.5 text-left hover:bg-sunken" @click="pick(h.card)">
            <span class="min-w-0 flex-1">
              <span class="block truncate"><span class="ident text-muted">#{{ h.card.number }}</span> {{ h.card.title }}</span>
              <span class="block truncate text-xs text-muted">{{ h.reason.label }}</span>
            </span>
            <span class="badge badge-tight shrink-0" :class="`badge-st-${h.card.status}`"><span class="swatch h-2 w-2" aria-hidden="true"></span>{{ h.card.status }}</span>
          </button>
        </li>
      </ul>
    </section>
  </div>
</template>
