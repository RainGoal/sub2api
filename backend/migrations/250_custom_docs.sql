-- Independent documentation content. Drafts and published snapshots are isolated.
CREATE TABLE IF NOT EXISTS custom_docs_nodes (
    id BIGSERIAL PRIMARY KEY,
    kind VARCHAR(8) NOT NULL CHECK (kind IN ('group', 'page')),
    parent_id BIGINT REFERENCES custom_docs_nodes(id) ON DELETE RESTRICT,
    slug VARCHAR(96) NOT NULL UNIQUE,
    title_zh VARCHAR(200) NOT NULL,
    title_en VARCHAR(200) NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    revision BIGINT NOT NULL DEFAULT 1,
    slug_locked BOOLEAN NOT NULL DEFAULT FALSE,
    created_by BIGINT,
    updated_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (parent_id IS NULL OR kind = 'page')
);
CREATE INDEX IF NOT EXISTS idx_custom_docs_nodes_parent_sort ON custom_docs_nodes(parent_id, sort_order, id);

CREATE TABLE IF NOT EXISTS custom_docs_contents (
    node_id BIGINT NOT NULL REFERENCES custom_docs_nodes(id) ON DELETE CASCADE,
    locale VARCHAR(5) NOT NULL CHECK (locale IN ('zh-CN', 'en-US')),
    draft_title VARCHAR(200) NOT NULL,
    draft_summary VARCHAR(1000) NOT NULL DEFAULT '',
    draft_content_md TEXT NOT NULL DEFAULT '',
    revision BIGINT NOT NULL DEFAULT 1,
    published_title VARCHAR(200),
    published_summary VARCHAR(1000),
    published_content_md TEXT,
    published_search_text TEXT,
    published_revision BIGINT,
    published_at TIMESTAMPTZ,
    published_by BIGINT,
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (node_id, locale),
    CHECK ((published_revision IS NULL AND published_at IS NULL) OR
           (published_revision IS NOT NULL AND published_at IS NOT NULL AND published_content_md IS NOT NULL))
);
