#!/usr/bin/env python3
"""Validate the HyperShift Quest question bank.

Checks: YAML parses; ids unique; topics, formats and depths valid; mcq
questions have 2-4 options with at least one correct; non-mcq questions
have a rubric with must_cover; every question has a why and a source.

Source paths are relative to the openshift/hypershift repository, so
existence is only checked when a checkout is provided:

    scripts/lint-bank.py [--hs-root /path/to/hs-main] [bank-dir]
"""
import sys
import os
import glob
import argparse
import yaml

TOPICS = {"architecture", "networking", "data-plane", "control-plane", "lifecycle", "kubevirt"}
FORMATS = {"mcq", "explain", "code", "scenario"}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("bank_dir", nargs="?", default="bank")
    ap.add_argument("--hs-root", default=os.environ.get("HS_ROOT", ""))
    args = ap.parse_args()

    errors = []
    questions, ids = [], set()
    files = sorted(glob.glob(os.path.join(args.bank_dir, "*.yaml")))
    if not files:
        sys.exit(f"no yaml files in {args.bank_dir}")

    for f in files:
        if os.path.basename(f) == "board.yaml":
            board = yaml.safe_load(open(f))
            nums = [t["n"] for t in (board or {}).get("tiles", [])]
            if nums != list(range(1, 25)):
                errors.append(f"{f}: board tiles are not 1..24 sequential")
            continue
        try:
            qs = yaml.safe_load(open(f))
        except yaml.YAMLError as e:
            errors.append(f"{f}: YAML parse error: {e}")
            continue
        if not isinstance(qs, list):
            errors.append(f"{f}: expected a list of questions")
            continue
        for q in qs:
            questions.append((f, q))

    for f, q in questions:
        qid = q.get("id", "<no id>")
        if qid in ids:
            errors.append(f"{f}: duplicate id {qid}")
        ids.add(qid)
        if q.get("topic") not in TOPICS:
            errors.append(f"{qid}: bad topic {q.get('topic')!r}")
        if q.get("format") not in FORMATS:
            errors.append(f"{qid}: bad format {q.get('format')!r}")
        if not isinstance(q.get("depth"), int) or not 1 <= q.get("depth", 0) <= 4:
            errors.append(f"{qid}: depth must be 1..4")
        if not q.get("question", "").strip():
            errors.append(f"{qid}: empty question")
        if not q.get("why", "").strip():
            errors.append(f"{qid}: missing why (the lesson after the reveal)")
        src = q.get("source", "").strip()
        if not src:
            errors.append(f"{qid}: missing source")
        elif args.hs_root and not os.path.exists(
            os.path.join(args.hs_root, src.split("#")[0].strip())
        ):
            errors.append(f"{qid}: source not found in {args.hs_root}: {src}")
        if q.get("format") == "mcq":
            opts = q.get("options", [])
            if not 2 <= len(opts) <= 4:
                errors.append(f"{qid}: mcq needs 2-4 options, has {len(opts)}")
            if sum(1 for o in opts if o.get("correct")) < 1:
                errors.append(f"{qid}: mcq has no correct option")
        else:
            rubric = q.get("rubric") or {}
            if not rubric.get("must_cover"):
                errors.append(f"{qid}: non-mcq needs rubric.must_cover")
            if q.get("options"):
                errors.append(f"{qid}: non-mcq must not have options")

    hs_note = f" (sources checked against {args.hs_root})" if args.hs_root \
        else " (source existence NOT checked — pass --hs-root or set HS_ROOT)"
    print(f"{len(questions)} questions across {len(files)} files{hs_note}")
    if errors:
        print(f"\nFAIL ({len(errors)}):")
        for e in errors:
            print(f"  - {e}")
        sys.exit(1)
    print("ALL CHECKS PASS")


if __name__ == "__main__":
    main()
