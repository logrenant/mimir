-- 0020_outreach_channel — Track B (WhatsApp + e-posta taslakları).
--
-- outreach_emails now holds a draft per (place_id, channel): the same company
-- gets an email and a WhatsApp message, and they are two different answers to
-- the same question rather than two versions of one.
--
-- The primary key is deliberately NOT changed. `prompt_version` already carries
-- the channel — internal/leadgen composes it as
-- `email-v1[@provider/model]#<channel>[:<rule-hash>]` — so (place_id,
-- prompt_version) is still unique per channel, and rebuilding the table to widen
-- the key would throw away every draft an operator has already marked sent.
--
-- The column is therefore a *filter*, not a key half: it is what lets a read ask
-- "every WhatsApp draft for these forty companies" without parsing a version
-- string in SQL. Rows written before this migration are emails, which is what
-- the default says.
--
-- The rule-hash half of prompt_version is the invalidation this table gains with
-- it: a rule file is part of the drafting prompt, so editing one produces a new
-- key and the drafts written under the old text are neither served nor
-- overwritten. They stay, unreachable, until the operator puts the old rules
-- back — which is the recovery this design gets for free.
--
-- Migrations are append-only: never edit an applied file, add 0021_*.sql.

ALTER TABLE outreach_emails ADD COLUMN channel TEXT NOT NULL DEFAULT 'email';

CREATE INDEX IF NOT EXISTS idx_outreach_emails_channel
    ON outreach_emails (channel, prompt_version);

-- The address the email draft is actually sent to.
--
-- Stage 1b has been finding these since task-57 — internal/contacts reads a
-- company's own site for a phone number *and* an email address — and the ledger
-- had nowhere to put the second one, so every address was discarded the moment
-- the run's response was rendered. A ledger that holds a drafted email and not
-- the address it goes to is a list nobody can work through, which is the whole
-- point of keeping it.
--
-- Empty is the normal state, exactly as it is for website and phone: most
-- listings carry no address, and the upsert in PutLeadRun never overwrites a
-- populated column with an empty one.
ALTER TABLE leads ADD COLUMN email TEXT NOT NULL DEFAULT '';
