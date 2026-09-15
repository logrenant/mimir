-- The parser's edges keep their direction.
--
-- UpsertBrainEdges has always sorted src and dst lexicographically before
-- writing, so "the same pair discovered from either end is one row rather than
-- two". That is right for the edges it was written for: a semantic or tag link
-- between two files is a statement that they belong together, and it means the
-- same read backwards.
--
-- It is wrong for the parser's. A calls edge is directed — internal/brain/
-- structural.go writes caller to callee — and sorting the endpoints threw that
-- away at write time. The consequence was quiet and total: "who calls this
-- function" could not be answered, and nothing said so, because the edge was
-- still there and still connected the right two nodes.
--
-- Structural edges are derived data, re-parsed deterministically and for free,
-- so the fix is to drop them and let the next sweep write them again with their
-- direction intact. Clearing the digest is what makes that sweep happen:
-- task-75 taught the supervisor to skip a project whose files have not changed,
-- and without this it would skip every one of them.
DELETE FROM brain_edges
 WHERE kind IN ('calls', 'imports', 'defines', 'inherits',
                'implements', 'references', 'uses', 'structural');

DELETE FROM brain_capture_state WHERE key LIKE 'structural:%';
