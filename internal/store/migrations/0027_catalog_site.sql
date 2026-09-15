-- 0027_catalog_site — what the operator's storefront looks like.
--
-- A column rather than a key inside brand_json, and that separation is the
-- point. BrandKit.hash is half of a draft's cache key: it carries the voice,
-- the tag vocabulary and three structure numbers, so that correcting the voice
-- correctly invalidates copy written under the old one. A storefront's
-- background colour is not evidence about any of that. Folding the scan into
-- brand_json would put it one careless hash change away from discarding every
-- approved draft in a catalogue because somebody re-measured a colour.
--
-- Nullable with no default: a file nobody has pointed at a shop has no scan,
-- and that is a different thing from a shop measured as blank. StoredImport
-- leaves the field empty and value() leaves the zero SiteScan, whose Usable()
-- is false — so the preview keeps its own readable default rather than
-- rendering a storefront as an empty page.
--
-- No FOREIGN KEY, for the reason 0026 gives: this store opens SQLite without
-- foreign_keys, and deletion is explicit in DeleteCatalogImport.

ALTER TABLE catalog_imports ADD COLUMN site_json TEXT;
