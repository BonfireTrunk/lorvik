import ELK from 'elkjs/lib/elk-api'
import type { LayoutEngine } from './layout'

let elk: InstanceType<typeof ELK> | undefined

/** The ELK engine, running in a web worker so layout never blocks the page. */
export function workerEngine(): LayoutEngine {
  elk ??= new ELK({ workerFactory: () => new Worker(new URL('elkjs/lib/elk-worker.min.js', import.meta.url)) })
  return elk
}
