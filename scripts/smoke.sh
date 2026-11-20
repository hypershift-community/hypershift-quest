#!/usr/bin/env bash
# End-to-end smoke test: build the server, boot it twice (an mcq-only
# config and an open-format config), and drive complete game rounds over
# HTTP. Asserts:
#   - the public state never contains correct-answer information or
#     other players' answer texts
#   - mcq rounds auto-grade with correct scoring and tile advancement
#   - open-format rounds enter the grading phase, grade via the
#     game-master API, and reveal with feedback
#   - the host view and grading queue are host-authenticated
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${SMOKE_PORT:-8199}"
DIR="$(mktemp -d)"
BIN="$DIR/quest-smoke"
go build -o "$BIN" .
# Clear any leftover instance from a previously failed run holding the port.
pkill -f "$BIN" 2>/dev/null || true
fuser -k "$PORT/tcp" 2>/dev/null || true
sleep 0.5

cat > "$DIR/mcq.toml" <<EOF
addr = "127.0.0.1:$PORT"
[game]
formats = ["mcq"]
EOF
cat > "$DIR/open.toml" <<EOF
addr = "127.0.0.1:$PORT"
[game]
formats = ["explain"]
EOF

run_phase () {
  local CFG="$1" SCRIPT="$2"
  "$BIN" -config "$CFG" >/dev/null 2>&1 &
  PID=$!
  trap 'kill $PID 2>/dev/null || true' EXIT
  sleep 1
  B="http://127.0.0.1:$PORT"
  if ! curl -sf "$B/" -o /dev/null; then
    echo "server did not come up"; exit 1
  fi
  python3 - "$B" "$SCRIPT" <<'PYEOF'
import json, sys, urllib.request, urllib.error

B = sys.argv[1]
MODE = sys.argv[2]

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
blob = json.dumps(st).lower()
for leak in ('"correct": true', '"correct":true', '"correctid"', '"rubric"', 'answertext'):
    assert leak not in blob, f"leak in public state: {leak}"

# host-only endpoints reject bad keys
for path in (f"/api/rooms/{code}/hostview?hostId=wrong",
             f"/api/rooms/{code}/grading?hostId=wrong"):
    try:
        urllib.request.urlopen(B + path)
        sys.exit(f"{path} accepted a bad host key")
    except urllib.error.HTTPError:
        pass

if MODE == "mcq":
    assert q["format"] == "mcq" and len(q["options"]) >= 2
    hv = call(f"/api/rooms/{code}/hostview?hostId={host_id}")
    assert hv["host"]["correct"], "host view lacks the answer"
    correct_id = next(o["id"] for o in q["options"] if o["text"] == hv["host"]["correct"])
    wrong_id = next(o["id"] for o in q["options"] if o["id"] != correct_id)
    call(f"/api/rooms/{code}/answer", {"playerId": p1, "optionId": correct_id})
    call(f"/api/rooms/{code}/answer", {"playerId": p2, "optionId": wrong_id})
    st = call(f"/api/rooms/{code}/state")
    assert st["phase"] == "reveal", st["phase"]
    rev = st["reveal"]
    assert rev["format"] == "mcq" and rev["correctId"] == correct_id
    assert rev["why"] and rev["source"]
    by = {r["name"]: r for r in rev["results"]}
    assert by["Smoke One"]["correct"] and by["Smoke One"]["gained"] > 0
    assert not by["Smoke Two"]["correct"]
    players = {p["name"]: p for p in st["players"]}
    assert players["Smoke One"]["tile"] >= 2 and players["Smoke One"]["score"] > 0
    assert players["Smoke Two"]["tile"] == 0
    try:
        call(f"/api/rooms/{code}/answer", {"playerId": p1, "optionId": correct_id})
        sys.exit("double answer accepted")
    except urllib.error.HTTPError:
        pass
    call(f"/api/rooms/{code}/host/next", {"hostId": host_id})
    st = call(f"/api/rooms/{code}/state")
    assert st["phase"] == "question" and st["round"] == 1
    print("SMOKE OK (mcq): auto-grade, scoring, tiles, no leak, advance")

else:
    assert q["format"] != "mcq", "expected an open-format round"
    SECRET1, SECRET2 = "konnectivity reverse tunnel answer one", "a different take two"
    call(f"/api/rooms/{code}/answer", {"playerId": p1, "text": SECRET1})
    call(f"/api/rooms/{code}/answer", {"playerId": p2, "text": SECRET2})
    st = call(f"/api/rooms/{code}/state")
    assert st["phase"] == "grading", st["phase"]
    # answer texts must not leak into the public state
    assert SECRET1 not in json.dumps(st) and SECRET2 not in json.dumps(st)

    gq = call(f"/api/rooms/{code}/grading?hostId={host_id}")
    assert gq["rubric"] and gq["rubric"]["must_cover"], "grading queue lacks rubric"
    subs = {s["playerId"]: s for s in gq["submissions"]}
    assert subs[p1]["text"] == SECRET1 and subs[p2]["text"] == SECRET2
    base = gq["question"]["base"]
    assert base > 0 and gq["question"]["half"] == base // 2

    call(f"/api/rooms/{code}/grade", {"hostId": host_id, "playerId": p1,
        "gained": base, "feedback": "hit 4/4 rubric items"})
    call(f"/api/rooms/{code}/grade", {"hostId": host_id, "playerId": p2,
        "gained": base // 2, "feedback": "hit 2/4 rubric items"})
    call(f"/api/rooms/{code}/complete-grading", {"hostId": host_id})

    st = call(f"/api/rooms/{code}/state")
    assert st["phase"] == "reveal", st["phase"]
    rev = st["reveal"]
    assert rev["format"] != "mcq" and rev["correctId"] == -1
    by = {r["name"]: r for r in rev["results"]}
    assert by["Smoke One"]["correct"] and by["Smoke One"]["feedback"].startswith("hit 4/4")
    assert not by["Smoke Two"]["correct"] and by["Smoke Two"]["gained"] == base // 2
    players = {p["name"]: p for p in st["players"]}
    assert players["Smoke One"]["tile"] >= 2, "full credit must advance the token"
    assert players["Smoke Two"]["tile"] == 0, "half credit must not advance"
    print("SMOKE OK (open): grading phase, rubric queue, grades, feedback reveal")
PYEOF
  kill $PID 2>/dev/null || true
  wait $PID 2>/dev/null || true
  trap - EXIT
}

run_phase "$DIR/mcq.toml" mcq
run_phase "$DIR/open.toml" open
echo "SMOKE OK: all phases"
