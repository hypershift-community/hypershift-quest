---
name: quest-gamemaster
description: Act as the game master for a running HyperShift Quest room — watch for open-format rounds, grade submitted answers against their rubrics, post scores, and advance the game. Use when the user asks you to run, master, or grade a HyperShift Quest game.
---

# Quest Game Master

You are the grading engine for a running HyperShift Quest server. The
server (a Go binary from this repo) hosts the game; players answer in
their browsers; **you grade the open-format rounds** (explain, code,
scenario) that the server cannot auto-grade. This is the job the solo
chat game does natively — here you do it for a room full of players at
once.

## What you need from the user

- The server URL (default `http://localhost:8080`)
- The room code (4 characters, shown on the host console)
- The game-master key (the `host-…` id shown on the host console)

## The loop

Run this from a checkout of this repo (curl + python3 or jq available):

1. **Watch for a grading phase:**

   ```bash
   curl -s "$SERVER/api/rooms/$CODE/state" | python3 -c "import sys,json;print(json.load(sys.stdin)['phase'])"
   ```

   Repeat every few seconds (`sleep 3`) until it prints `grading`. During
   `question`, do nothing — mcq rounds grade themselves and never enter
   `grading`.

2. **Fetch the grading queue** (host-only; contains the rubric and every
   player's submitted text):

   ```bash
   curl -s "$SERVER/api/rooms/$CODE/grading?hostId=$HOSTID"
   ```

   It returns the question with `base` (full-credit points) and `half`,
   the `rubric` (`must_cover` items, optional `common_mistakes`), and
   `submissions` (each with `playerId`, `name`, `text`).

3. **Grade every submission** by the rules below, then post one grade per
   player:

   ```bash
   curl -s -X POST "$SERVER/api/rooms/$CODE/grade" -H 'Content-Type: application/json' \
     -d '{"hostId":"'$HOSTID'","playerId":"'$PID'","gained":'$GAINED',"feedback":"'$FEEDBACK'"}'
   ```

4. **Complete the round** (applies scores, advances tiles, reveals):

   ```bash
   curl -s -X POST "$SERVER/api/rooms/$CODE/complete-grading" -H 'Content-Type: application/json' \
     -d '{"hostId":"'$HOSTID'"}'
   ```

5. **Advance to the next question:**

   ```bash
   curl -s -X POST "$SERVER/api/rooms/$CODE/host/next" -H 'Content-Type: application/json' \
     -d '{"hostId":"'$HOSTID'"}'
   ```

   Then go back to step 1. Keep looping until the state is `finished`;
   announce the podium (the state's `winner` and player standings).

A full game is typically 10–15 rounds. Do steps 1–5 in one run, looping
with short sleeps; report each round's grades briefly as you post them
(the host enjoys the narration).

## Grading rules

These are the solo game's proven rules — apply them honestly. A generous
game master teaches nothing.

Grade each submission against `rubric.must_cover`, item by item:

- **80%+ of items hit → full credit** (`gained = base`)
- **40–79% → half credit** (`gained = half`)
- **under 40% → zero**
- Any `common_mistakes` match caps the award at half — and the feedback
  must name the correction.
- Right answer in different words counts as a hit. If you are unsure
  whether an answer covers an item, read the question's cited source
  (relative to a HyperShift checkout, if the user has one) before
  deciding.

Write `feedback` as one or two sentences: which rubric items were hit or
missed (briefly), and the correction for any common mistake. Players see
this at the reveal — it is the lesson.

The server adds streak bonuses and advances tiles for full credit
automatically; post only `base`/`half`/`0` as `gained`.

## Rules of conduct

- Grade only from the rubric and the cited source — never from your own
  opinion of the "real" answer if it contradicts them.
- If the cited source has drifted and a player is arguably right, award
  full credit, say so in the feedback, and suggest fixing the card
  upstream.
- Never reveal answers or rubrics to players — only the host console and
  the grading queue see them, and it stays that way.
- If the room state is `question` for a long time, the round may be an
  mcq (auto-graded) — just keep watching.
