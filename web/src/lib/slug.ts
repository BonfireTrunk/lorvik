/** A project slug suggested from a name: lowercase, runs of other characters become one hyphen, trimmed, at most 64. The server's rule decides what is valid. */
export function slugFromName(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+/, '')
    .slice(0, 64)
    .replace(/-+$/, '')
}
