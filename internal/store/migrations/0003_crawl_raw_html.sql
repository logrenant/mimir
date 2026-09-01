-- 0003_crawl_raw_html — adds raw_html to crawl_pages for FetchRaw's
-- structured-extraction cache path (Stage F: internal/extract consumers).
--
-- Migrations are append-only: never edit an applied file, add 0004_*.sql.

ALTER TABLE crawl_pages ADD COLUMN raw_html TEXT NOT NULL DEFAULT '';
