-- Which Claude Code identity a coding run spends.
--
-- An account here is a credential slot, not a login: the CLI derives its
-- keychain entry from CLAUDE_SECURESTORAGE_CONFIG_DIR, so a directory path is
-- the whole handle. An empty config_dir is the CLI's default slot — the one it
-- uses when that variable is not set at all.
--
-- Two columns on coding_runs rather than one, because "which account did the
-- operator ask for" and "which account did it actually run on" are different
-- questions: the first is a pin the operator may leave blank, the second is
-- filled in by the dispatcher at the moment it claims the run.

CREATE TABLE IF NOT EXISTS accounts (
    id           TEXT PRIMARY KEY,
    label        TEXT NOT NULL,
    config_dir   TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL DEFAULT 0
);

-- One row per credential slot: registering the same directory twice is the
-- same account, not a second one that could run beside itself.
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_config_dir ON accounts (config_dir);

ALTER TABLE coding_runs ADD COLUMN requested_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE coding_runs ADD COLUMN account_id           TEXT NOT NULL DEFAULT '';

-- The dispatcher asks "is this account busy?" on every claim.
CREATE INDEX IF NOT EXISTS idx_coding_runs_account ON coding_runs (account_id, status);
