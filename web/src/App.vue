<script setup lang="ts">
import { useRoute } from 'vue-router'
import ActionToast from './components/ActionToast.vue'
import AppHeader from './components/AppHeader.vue'
import ActionPrompt from './components/card/ActionPrompt.vue'
import CommandPalette from './components/CommandPalette.vue'
import KeymapHelp from './components/KeymapHelp.vue'
import { cardActionState } from './composables/useCardActions'
import { overlays, useKeymapRoot } from './composables/useKeymap'
import { auth } from './lib/auth'

const route = useRoute()
useKeymapRoot()
</script>

<template>
  <a class="skip-link" href="#main">Skip to content</a>
  <AppHeader v-if="auth.token && route.name !== 'login'" />
  <main id="main" class="mx-auto max-w-[1600px] px-4 py-4">
    <router-view />
  </main>
  <KeymapHelp v-if="overlays.help" @close="overlays.help = false" />
  <CommandPalette v-if="overlays.palette" @close="overlays.palette = false" />
  <ActionPrompt
    v-if="cardActionState.prompt"
    :key="cardActionState.prompt.card.id + cardActionState.prompt.kind"
    :kind="cardActionState.prompt.kind"
    :card="cardActionState.prompt.card"
    @close="cardActionState.prompt = null"
  />
  <ActionToast />
</template>
