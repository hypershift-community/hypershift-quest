#!/usr/bin/env bash
# Sync the embedded question bank from a local HyperShift (hs-main)
# checkout, then rebuild the binary so the new bank is embedded.
#
# Usage: scripts/sync-bank.sh /path/to/hypershift-checkout
#
# NOTE: bank/local.yaml is never touched — it holds community-authored
# cards that do not exist upstream. See AGENTS.md.
set -euo pipefail
SRC="${1:?usage: scripts/sync-bank.sh /path/to/hypershift-checkout}"
QUESTIONS="$SRC/skills/hypershift-quiz/questions"
[ -d "$QUESTIONS" ] || { echo "no question bank at $QUESTIONS" >&2; exit 1; }
cp "$QUESTIONS"/*.yaml bank/
cp "$SRC/skills/hypershift-quiz/board.yaml" bank/
echo "bank synced from $QUESTIONS (bank/local.yaml left untouched) — rebuild with: go build ."
