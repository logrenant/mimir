-- The daemon's own model calls no longer pick a credential slot.
--
-- The mark existed so refine, distil and recap could be pointed at one identity
-- from the app. In practice it was a switch with no moment attached to it: a
-- coding run has a human who dispatched it and can say which account pays, but
-- a resident sweep has nobody, so the mark silently redirected every summary
-- and recap the daemon made from then on. Those calls now run on the CLI's own
-- login, named in cmd/mimir-daemon rather than stored, and credential slots
-- stay what they were built for — routing a coding run the operator started.
--
-- Dropped rather than left unused: a column nothing writes is a column the next
-- reader has to prove is dead. Slot rows themselves are untouched.
--
-- The index goes first because SQLite refuses to drop a column an index still
-- names, and 0015 put a partial unique index on this one.
DROP INDEX IF EXISTS idx_accounts_background;
ALTER TABLE accounts DROP COLUMN is_background;
