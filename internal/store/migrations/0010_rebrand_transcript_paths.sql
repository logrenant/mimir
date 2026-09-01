-- The rebrand moved the transcript directory with the store:
-- ~/Library/Application Support/goat-mcp/transcripts -> .../mimir/transcripts.
--
-- Two tables hold that path as an absolute string written at the time the run
-- happened, and both are read back later: coding_runs.transcript_path is what
-- /ws/runs/{id} replays a finished run from, and memory_episodes.source_path is
-- where the ingest resumes reading. Carrying the database forward without
-- rewriting them leaves every pre-rebrand run pointing into a directory the
-- operator is being told they can now delete.
--
-- Matched on the directory pair rather than a full prefix so it is independent
-- of the home directory, and narrow enough that it cannot touch a Claude Code
-- session transcript under ~/.claude/projects. Episode keys are `run:<id>` and
-- carry no path, so nothing here can orphan or duplicate a row.
--
-- scripts/install-agent.sh copies the transcript files themselves; this only
-- moves the pointers.
UPDATE coding_runs
   SET transcript_path = replace(transcript_path,
       '/Application Support/goat-mcp/transcripts/', '/Application Support/mimir/transcripts/')
 WHERE transcript_path LIKE '%/Application Support/goat-mcp/transcripts/%';

UPDATE memory_episodes
   SET source_path = replace(source_path,
       '/Application Support/goat-mcp/transcripts/', '/Application Support/mimir/transcripts/')
 WHERE source_path LIKE '%/Application Support/goat-mcp/transcripts/%';

-- memory_ingest_state keys resumable reads by the same path.
UPDATE memory_ingest_state
   SET source_path = replace(source_path,
       '/Application Support/goat-mcp/transcripts/', '/Application Support/mimir/transcripts/')
 WHERE source_path LIKE '%/Application Support/goat-mcp/transcripts/%';
