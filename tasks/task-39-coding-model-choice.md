# task-39 — Per-task model choice

- **Status:** done
- **Owner:** daemon
- **Prerequisites:** task-35, task-37
- **Primary paths:**
  - `internal/config/config.go`
  - `internal/coderunner/runner.go`, `internal/coderunner/model.go`
  - `internal/api/api.go`, `internal/api/handlers.go`
- **Roadmap bucket:** B / M3.

## Context

`coding_runs.model` has existed since 0001 and `Run.Model` since the first
runner, but nothing ever chose it: `Create` stamped `cfg.CodingModel` on the row
and `args()` passed the same constant to `--model`. The column was therefore
not a decision, only an echo — the result event overwrote it with whatever the
CLI reported having used, which was always the one model the daemon asked for.

The operator wants the axis the CLI already exposes: `claude --help` documents
`--model` as taking either an alias (`opus`, `sonnet`, `fable`) or a full name.
A long refactor is not needed; what is missing is a value on the way in and one
validation.

Two accounts made this matter more, not less. A pinned account decides *whose*
limit a run spends; a pinned model decides *how fast* it spends it. Picking
Haiku for a mechanical edit and Opus for a design question is the difference
between a queue that drains and one that does not.

## Scope

- `config.CodingModels`: the allow-list, as `[]CodingModelChoice{ID, Label}`,
  with `CodingModel` remaining the default and required to appear in it
  (`Validate` enforces this — a default that is not offerable is a bug).
- `coderunner.CreateRequest.Model`, validated against that list at create time,
  persisted on the row, and passed to `args()`.
- `ErrUnknownModel`, a typed sentinel (SD-6), mapped to 400.
- `GET /coding-models` so the desktop picker has one source of truth rather
  than a second copy of the list.
- `POST /coding-tasks` accepts `model`; absent means the default.

## Out of scope

- Per-account default models. An account is an identity, not a preference.
- `--fallback-model`. It changes what a run costs without saying so on the
  card, which is exactly the confusion this task removes.
- Changing the model of a run that already exists. A queued card can be
  deleted and rewritten; a running one cannot be re-pointed mid-flight.
- Asking the CLI for its live model list. There is no such subcommand, and a
  probe per page load is not worth a list that changes twice a year.

## Interfaces

```go
// internal/config
type CodingModelChoice struct {
	ID    string // what --model receives
	Label string // what the picker shows
}
CodingModels []CodingModelChoice

// internal/coderunner
type CreateRequest struct {
	// …
	// Model is one of config.CodingModels' IDs. Empty means the default,
	// which is what every client that predates the picker sends.
	Model string
}
var ErrUnknownModel = errors.New("coderunner: unknown model")
```

```
GET /coding-models → {"models":[{"id","label","default":bool}]}
POST /coding-tasks  {…, "model": "claude-opus-5"}
```

## Definition of Done

- `make check` green.
- A run created with `model` spawns with that `--model` value; one created
  without spawns with `cfg.CodingModel`.
- An unknown model is a 400 at create time, not a failure three seconds into
  the run.
- The result event still overwrites `row.Model` with what the CLI reports:
  the row says what ran, not only what was asked for.
- `Validate` rejects a `CodingModel` absent from `CodingModels`.

## Notes for reviewer

The allow-list is a constant, not a knob (SD-1) — no env variable, no config
file. It is edited when the model lineup changes, the same way `CodingModel`
and `ResearchModel` already are.

Full IDs rather than aliases. An alias resolves to "the latest", which is the
wrong contract for a card that may sit in the backlog for a week: the operator
picked a model, and the run should be the model they picked. The cost is that
this list needs editing when a generation ships, which is the same cost the two
existing model constants already carry.

`--model` takes its value positionally, so a flag-shaped string cannot inject a
second flag; the allow-list is there to give an honest error, not to close an
injection.
