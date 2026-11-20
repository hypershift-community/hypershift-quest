# AGENTS.md

This file provides guidance to AI coding agents working in this repository.
`CLAUDE.md` is a symlink to this file so that Claude Code auto-loads it; the
`AGENTS.md` name is canonical.

HyperShift Quest is a Kahoot-style multiplayer quiz server: one host runs it
on the company network, players join from browsers, and everyone races tokens
across the 24-tile quest board by answering timed questions about HyperShift.
It consumes the question bank maintained in
[openshift/hypershift](https://github.com/openshift/hypershift) under
`skills/hypershift-quiz/` (113 questions, six categories, four formats), which
also powers the chat-native solo game in that repo. This repo is the party
game: same bank, multiplayer rules.

## Repository map

| Path | Role |
|------|------|
| `main.go` | TOML config, HTTP routes, embedded assets, entrypoint |
| `bank.go` | Question/Option/Rubric/Board types; `LoadBank` (local checkout or embedded) |
| `game.go` | Hub/Room/Player, game state machine, SSE, scoring |
| `web/app.html` | The entire UI — one page, role picked by path (`/`, `/host`, `/board`) |
| `bank/` | Question YAML (synced from upstream) + `board.yaml` + `local.yaml` (community cards) |
| `skills/quest-gamemaster/` | Skill for the host's Claude session — the grading engine for open-format rounds |
| `scripts/` | `sync-bank.sh`, `lint-bank.py`, `smoke.sh` |

Deliberate constraints: Go stdlib plus only `BurntSushi/toml` and
`gopkg.in/yaml.v3`; the web UI is a single vanilla-JS file; no database —
state is in-memory. New dependencies need a reason.

## The rule that must never break

**Answers are server-side only.** `Option.Correct` is tagged `json:"-"` and
question data reaches clients exclusively through the `wireQuestion` /
`wireState` structs in `game.go`. Any new endpoint or event that exposes
question data must go through those wire structs — never marshal a `Question`
directly. `scripts/smoke.sh` asserts that the public state contains no
correct-answer information; run it after touching `game.go` or the web UI.

## Game state machine

```
lobby → question → ┬→ reveal → (host: next) → question … → finished
                   └→ grading → (game master grades) → reveal
```

- **mcq rounds** auto-grade when the deadline expires or everyone has
  answered. **Rubric rounds** (explain, code, scenario) enter the `grading`
  phase: answer texts are held host-only, and the game master — the host's
  Claude session running `skills/quest-gamemaster` — fetches the queue
  (`GET /api/rooms/{code}/grading?hostId=…`), grades each submission against
  the rubric (`POST /api/rooms/{code}/grade`), and completes the round
  (`POST /api/rooms/{code}/complete-grading`), which applies scores, advances
  tiles and reveals with per-player feedback.
- All transitions happen under `Room.mu`; `broadcast()` sends a **full state
  snapshot** to every SSE subscriber (simple and reconnect-friendly — no
  diffing).
- The question phase has a server-side deadline; a `time.AfterFunc` closes
  the answering window if the timer expires, and `answer()` closes it early
  when every player has answered.
- Rooms are in-memory and expire after two idle hours; restarting the binary
  clears everything. This is a game-night tool, not a service.

Scoring (tuning knobs, in `evaluateLocked` and `completeGradingLocked`):
`depth × 10` base; mcq adds up to `+50%` speed bonus (fraction of time
remaining) — open rounds deliberately have no speed bonus, typing speed is
not the game; streak bonus `2 × streak` capped at 10. **Full credit**
(gained ≥ base) advances the token 2 tiles (3 on a 🔥 streak of 3+); half
credit scores points only. First token to 24 wins, else best position after
the deck runs out.

The deck is shuffled and sized by `[game].rounds`; `[game].formats` filters
it (e.g. `["mcq"]` for an auto-graded-only night — empty means all formats,
with rubric rounds waiting for the game master).

## The question bank

Schema (one YAML list per file):

```yaml
- id: unique-kebab-id
  topic: architecture | networking | data-plane | control-plane | lifecycle | kubevirt
  depth: 1..4        # 1 orient · 2 mechanism · 3 wiring · 4 invariants & failure
  format: mcq | explain | code | scenario
  question: |
    ...
  options:           # mcq only, 2-4
    - text: ...
      correct: true
  rubric:            # non-mcq only
    must_cover: [ ... ]      # each item = one grading point
    common_mistakes:
      - mistake: ...
        correction: ...
  source: path/in/hypershift/repo     # relative to openshift/hypershift
  why: |              # the lesson shown after the reveal
```

Authoring rules, inherited from the upstream skill and enforced by review:

- **Cite files and symbols, never line numbers** — lines rot, symbols survive.
- Every question must be **answerable from its cited source alone**.
- Every `why` must teach something beyond the answer.
- A wrong question is a bug, not a typo.
- `depth` measures how many moving parts the answer connects, not how
  surprising the fact is. Single-mechanism facts are depth ≤ 2; cross-owner
  reasoning, invariants and debugging ladders are depth 3+.
- All four formats play: `mcq` auto-grades; `explain`, `code` and
  `scenario` are graded by the game master against `rubric.must_cover`
  (see `skills/quest-gamemaster/SKILL.md` for the grading bands).

### Where cards live

1. **Canonical:** `openshift/hypershift` → `skills/hypershift-quiz/questions/`.
   Reviewed like code; PRs there need a Jira reference in the title.
2. **Synced here:** `bank/*.yaml` via `scripts/sync-bank.sh <hs-checkout>`
   (or `make sync-bank HS_ROOT=...`). Sync overwrites the upstream category
   files — never edit those here.
3. **Community cards:** `bank/local.yaml` — the only bank file sync never
   touches. Use it for team-specific or experimental cards; upstream good
   ones when they mature. The loader reads every `*.yaml` in `bank/`, so no
   code change is needed.

`source:` paths are relative to the **hypershift** repo, so source checking
needs a checkout: `make lint HS_ROOT=/path/to/hs-main` (skipped, with a
warning, when not provided).

## Build, test, verify

```bash
make build   # go build .
make vet     # go vet
make fmt     # gofmt -l -w .
make lint    # scripts/lint-bank.py [HS_ROOT=...] — schema + citation checks
make smoke   # end-to-end: build, boot, drive a full game round over HTTP,
             # assert scoring and no answer leakage
make run     # go run .
```

Run `make vet fmt lint smoke` before considering any change done — CI runs
the same set. The smoke test is the safety net for game-engine and UI
changes; extend it when adding endpoints or phases.

## Configuration

`quest.toml` (gitignored) or `-config <path>`; see `quest.toml.example`.
`[bank].path` accepts a HyperShift checkout root or the questions directory
itself and loads the bank live — the server then quizzes whatever branch is
checked out there. Empty means the embedded bank.

## Web UI conventions

One file, `web/app.html`: dark theme with CSS custom properties on `:root`,
category colors as `--arch`/`--net`/… classes, roles picked from
`location.pathname`, SSE with full-state re-render on every event,
`localStorage` for player/host identity. Keep it one file until that
genuinely hurts; no framework, no build step.

## Roadmap

Ordered by leverage; each is a self-contained improvement:

1. **Depth-aware decks** — host picks a depth mix or per-round category;
   currently the deck is a flat shuffle filtered only by format.
2. **Season leaderboard** — append-only JSON file of finished games.
3. **Container image + OpenShift manifest** — run game night on a hosted
   control plane, for maximum thematic correctness.
4. **Reveal pacing / lobby settings** — per-room time limit, rounds.
5. **Manual grading console** — the grading API already supports it; a
   host-side UI with `must_cover` checkboxes would let a human grade when
   no Claude session is available.

When making changes, prefer extending the wire structs and the state machine
over parallel mechanisms, and keep the public state free of answer data.
