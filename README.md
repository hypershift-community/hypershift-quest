# HyperShift Quest 🎲

A Kahoot-style multiplayer quiz for learning HyperShift — a team game
where players race tokens across the HyperShift Quest board by answering
questions about HyperShift architecture, networking, CAPI, and the
OpenShift components HyperShift runs.

The question bank comes from
[openshift/hypershift](https://github.com/openshift/hypershift)
(`skills/hypershift-quiz/`): 113 curated, citation-backed questions across
six categories — architecture, networking, data-plane, control-plane,
lifecycle, and KubeVirt — each with a source file and a
why-explanation revealed after the round. The same bank also powers the
chat-native solo game (`/hypershift-quiz` skill in the HyperShift repo).

## Quickstart

```bash
go run .
```

Then, on the company network:

- **Host:** open `http://<host>:8080/host`, create a room, share the code.
- **Players:** open `http://<host>:8080/` on a phone or laptop, enter the
  room code and a name.
- **Big screen (optional):** open `http://<host>:8080/board` — the shared
  24-tile board with every player's token and the live leaderboard.

No accounts, no database, no infrastructure — one process, in-memory
state, everything stays on your network.

## How the game works

- Everyone gets the **same question** each round, with a countdown.
- Answer faster for more points: `depth × 10` base, up to `+50%` speed
  bonus, plus a streak bonus (🔥 3+ correct in a row also advances you an
  extra tile).
- A correct answer advances your token **2 tiles** toward **tile 24**;
  first to 24 wins (or best position after the final round).
- After each round: the answer, the why-explanation, and the source in
  the HyperShift repo.
- Answers are graded **server-side only** — the correct answer never
  reaches a player's browser before the reveal.

## Configuration

Configuration is optional TOML (`quest.toml`, or `-config <path>`); see
[`quest.toml.example`](quest.toml.example). The interesting one:

```toml
[bank]
# Load the question bank live from a local HyperShift checkout —
# the server then always quizzes whatever branch you have checked out.
path = "/home/you/code/github.com/openshift/hs-main"
```

The path accepts either the repository root or the questions directory
itself. Without it, the bank embedded in the binary is used — keep it
current with:

```bash
scripts/sync-bank.sh /path/to/hs-main && go build .
```

## Views

| Path | Who | What |
|------|-----|------|
| `/` | players | join, answer, personal results |
| `/host` | game master | room code, roster, start/next/end, answer preview |
| `/board` | big screen | all tokens on the quest board + leaderboard + question |

## The game master: grading with the host's Claude session

Multiple-choice rounds grade themselves. The rubric rounds — explain-why,
code reading, scenario — are graded by a game master: **the host's Claude
session**, using the [`quest-gamemaster`](skills/quest-gamemaster/SKILL.md)
skill. The host console shows a game-master key; give the room code and that
key to Claude and ask it to run the game. Claude watches for the grading
phase, fetches each player's answer with the question's rubric, grades by
the proven solo-game rules (≥80% of rubric items = full credit, 40–79% =
half, common mistakes capped at half with the correction named), and posts
scores with feedback that players see at the reveal.

Answer texts and rubrics are host-only — the public game state never
contains them, and the smoke test asserts it.

To run an auto-graded-only night instead, set `formats = ["mcq"]` in the
config.

## Notes and limits

- Deliberately **no authentication** — this is a LAN/VPN game night tool,
  not a service. Rooms expire after two idle hours; restarting the binary
  clears everything.
- Roadmap: depth-aware decks, season leaderboards, container image for
  running on an internal OpenShift cluster (playing the HyperShift quiz on
  a hosted control plane is encouraged).

## Contributing questions

Questions live in and are reviewed with
[openshift/hypershift](https://github.com/openshift/hypershift) — see
`skills/hypershift-quiz/SKILL.md` there for the schema and authoring
rules (cite files and symbols, never line numbers; every question must be
answerable from its cited source; a wrong question is a bug). This repo
consumes the bank, and community-authored cards can start life in
`bank/local.yaml` (never touched by sync) before being upstreamed.

## Developing

See [AGENTS.md](AGENTS.md) for the full guide — architecture, the
never-leak-answers rule, the question schema and authoring conventions,
and the roadmap. Before considering a change done:

```bash
make vet fmt lint smoke
```

CI runs the same set on every push and pull request.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
