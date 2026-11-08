import { apiClient } from '../client'

export type DocLocale = 'zh-CN' | 'en-US'

export interface DocContent {
  title: string
  summary: string
  content_md: string
  revision: number
  published_revision: number | null
  published_at: string | null
}

export interface DocAdminNode {
  id: number
  kind: 'group' | 'page'
  parent_id: number | null
  slug: string
  title_zh: string
  title_en: string
  sort_order: number
  revision: number
  slug_locked?: boolean
  updated_at?: string
  locales: Partial<Record<DocLocale, DocContent>>
}

export interface DocNodeInput {
  kind: 'group' | 'page'
  parent_id: number | null
  slug: string
  title_zh: string
  title_en: string
}

export interface DocTreeItem {
  id: number
  parent_id: number | null
  sort_order: number
}

export const docsAPI = {
  async tree(signal?: AbortSignal): Promise<DocAdminNode[]> {
    return (await apiClient.get<{ items: DocAdminNode[] }>('/admin/docs/tree', { signal })).data.items
  },
  async page(id: number): Promise<DocAdminNode> {
    return (await apiClient.get<DocAdminNode>('/admin/docs/pages/' + id)).data
  },
  async create(input: DocNodeInput): Promise<DocAdminNode> {
    return (await apiClient.post<DocAdminNode>('/admin/docs/nodes', input)).data
  },
  async update(id: number, input: Partial<Omit<DocNodeInput, 'kind'>>): Promise<DocAdminNode> {
    return (await apiClient.put<DocAdminNode>('/admin/docs/nodes/' + id, input)).data
  },
  async remove(id: number): Promise<void> {
    await apiClient.delete('/admin/docs/nodes/' + id)
  },
  async saveTree(items: DocTreeItem[]): Promise<void> {
    await apiClient.put('/admin/docs/tree', { items })
  },
  async saveDraft(id: number, input: { locale: DocLocale; title: string; summary: string; content_md: string; revision: number }): Promise<DocAdminNode> {
    return (await apiClient.put<DocAdminNode>('/admin/docs/pages/' + id + '/draft', input)).data
  },
  async publish(id: number, locale: DocLocale, revision: number): Promise<DocAdminNode> {
    return (await apiClient.post<DocAdminNode>('/admin/docs/pages/' + id + '/publish', { locale, revision })).data
  },
  async unpublish(id: number, locale: DocLocale, revision: number): Promise<DocAdminNode> {
    return (await apiClient.post<DocAdminNode>('/admin/docs/pages/' + id + '/unpublish', { locale, revision })).data
  }
}
