#!/usr/bin/env bash
# End-to-end smoke test: build the server, boot it on a scratch port,
# drive a full game round over HTTP (create room, join two players,
# start, answer, reveal) and assert:
#   - the public state never contains correct-answer information
#   - scoring and tile advancement behave
#   - the host view does contain the answer (host-only)
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${SMOKE_PORT:-8199}"
BIN="$(mktemp -d)/quest-smoke"
go build -o "$BIN" .
"$BIN" -addr "127.0.0.1:$PORT" >/dev/null 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null || true' EXIT
sleep 1

B="http://127.0.0.1:$PORT"
if ! curl -sf "$B/" -o /dev/null; then
  echo "server did not come up"; exit 1
fi

python3 - "$B" <<'EOF'
import json, sys, urllib.request

B = sys.argv[1]

def call(path, body=None):
    if body is None:
        r = urllib.request.urlopen(B + path)
    else:
        r = urllib.request.urlopen(urllib.request.Request(
            B + path, json.dumps(body).encode(), {"Content-Type": "application/json"}))
    return json.loads(r.read())

room = call("/api/rooms", {})
code, host_id = room["code"], room["hostId"]
p1 = call(f"/api/rooms/{code}/join", {"name": "Smoke One"})["playerId"]
p2 = call(f"/api/rooms/{code}/join", {"name": "Smoke Two"})["playerId"]
call(f"/api/rooms/{code}/host/start", {"hostId": host_id})

st = call(f"/api/rooms/{code}/state")
assert st["phase"] == "question", st["phase"]
q = st["question"]
assert q and len(q["options"]) >= 2, "no options"
blob = json.dumps(st).lower()
for leak in ('"correct": true', '"correct":true', '"correctid"', '"rubric"'):
    assert leak not in blob, f"answer leak in public state: {leak}"
assert "deadlineMs" in st and st["deadlineMs"] > 0

# player 1 answers the correct option, player 2 a wrong one.
correct_id = None
host_view = call(f"/api/rooms/{code}/hostview?hostId={host_id}")
assert host_view["host"]["correct"], "host view lacks the answer"
for o in q["options"]:
    if o["text"] == host_view["host"]["correct"]:
        correct_id = o["id"]
assert correct_id is not None, "correct option not among shuffled options"
wrong_id = next(o["id"] for o in q["options"] if o["id"] != correct_id)

call(f"/api/rooms/{code}/answer", {"playerId": p1, "optionId": correct_id})
call(f"/api/rooms/{code}/answer", {"playerId": p2, "optionId": wrong_id})

st = call(f"/api/rooms/{code}/state")
assert st["phase"] == "reveal", st["phase"]
rev = st["reveal"]
assert rev["correctId"] == correct_id
assert rev["why"] and rev["source"], "reveal lacks why/source"
by_name = {r["name"]: r for r in rev["results"]}
assert by_name["Smoke One"]["correct"] and by_name["Smoke One"]["gained"] > 0
assert not by_name["Smoke Two"]["correct"]
players = {p["name"]: p for p in st["players"]}
assert players["Smoke One"]["tile"] >= 2 and players["Smoke One"]["score"] > 0
assert players["Smoke Two"]["tile"] == 0 and players["Smoke Two"]["score"] == 0

# double answer is rejected
try:
    call(f"/api/rooms/{code}/answer", {"playerId": p1, "optionId": correct_id})
    sys.exit("double answer was accepted")
except urllib.error.HTTPError:
    pass

# host advances to the next round
call(f"/api/rooms/{code}/host/next", {"hostId": host_id})
st = call(f"/api/rooms/{code}/state")
assert st["phase"] == "question" and st["round"] == 1, st["phase"]

print("SMOKE OK: create/join/start/answer/reveal/score/no-leak/advance")
EOF
