import { computed, type WritableComputedRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { projects, type ListScope } from '../lib/projects'

/**
 * The page's scope, kept in `?scope=all` (absent means the selected project).
 * With no project selected the scope is all. Writes replace the history entry
 * and keep the page's other query params.
 */
export function useListScope(): WritableComputedRef<ListScope> {
  const route = useRoute()
  const router = useRouter()
  return computed<ListScope>({
    get: () => (!projects.current || route.query.scope === 'all' ? 'all' : 'project'),
    set: (v) => {
      const { scope: _, ...rest } = route.query
      void router.replace({ query: v === 'all' ? { ...rest, scope: 'all' } : rest })
    },
  })
}
