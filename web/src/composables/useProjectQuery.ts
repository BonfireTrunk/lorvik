import { computed } from 'vue'
import { useRoute } from 'vue-router'

/** The ?project= of the current route; unset addresses an org-level item. */
export function useProjectQuery() {
  const route = useRoute()
  return computed(() => (typeof route.query.project === 'string' ? route.query.project : undefined))
}
