export function documentErrorKey(cause: unknown, fallback = 'saveFailed'): string {
  const item = cause as { status?: number; reason?: string }
  const reasons: Record<string, string> = {
    DOCS_NOT_FOUND: 'notFound', DOCS_INVALID: 'invalidDocument', DOCS_CONFLICT: 'conflict',
    DOCS_SLUG_TAKEN: 'slugTaken', DOCS_SLUG_LOCKED: 'slugLocked',
    DOCS_PUBLISHED: 'deletePublished', DOCS_NOT_EMPTY: 'deleteNotEmpty'
  }
  const key = reasons[item.reason || ''] || (item.status === 403 ? 'forbidden' : item.status === 409 ? 'conflict' : item.status === 413 ? 'contentSize' : fallback)
  return 'docsAdmin.' + key
}
