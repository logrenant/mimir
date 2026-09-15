-- 0026_catalog_langs — a review decision per language, and the index the
-- cross-import outputs listing reads.
--
-- The source language is deliberately NOT in this table. Every row in
-- catalog_products.status was written as a decision about the file's own
-- language, and moving them here would mean either a backfill that leaves two
-- homes for one fact or a rewrite of the one GROUP BY the catalogs list is
-- built on. It is the same argument DraftVersion makes about the draft key:
-- the language is a suffix, and only for a target. The CHECK is what keeps
-- that argument true against a hand-written row.
--
-- Absent means pending. A product nobody has judged in Arabic has no row here,
-- so every read is a LEFT JOIN and every count of "bekliyor" is a count of
-- rows that are not there. An INNER JOIN would answer "hiç bekleyen yok" for a
-- catalogue nobody has touched, which is the exact opposite of the truth.
--
-- No FOREIGN KEY: this store opens SQLite without foreign_keys, so a declared
-- key would be decoration. Deletion is explicit in DeleteCatalogImport, the
-- same way catalog_drafts and catalog_research already are.

CREATE TABLE IF NOT EXISTS catalog_product_langs (
    product_id TEXT    NOT NULL,
    lang       TEXT    NOT NULL,
    status     TEXT    NOT NULL DEFAULT 'pending',
    reason     TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (product_id, lang),
    CHECK (lang <> '')
);

-- catalog_drafts is keyed (product_id, version), so a read that knows the
-- version and not the product — which is every read the outputs listing makes —
-- has no index to use and scans the table. This is that index.
CREATE INDEX IF NOT EXISTS idx_catalog_drafts_version
    ON catalog_drafts (version);
