#!/usr/bin/env python3
"""Judges the report of the app's self-test (app/Sources/Laserdot/SelfTest.swift).

Usage: check-selftest.py report.json en|ar
The fixtures (made by the CI step): beach.jpg, holiday photo.jpeg, photo.png,
"jpg notes.txt", project.pdf, "project plan.txt", Projects/readme.md,
Tool.app/Contents/Info.plist.
"""
import json
import re
import sys

report = json.load(open(sys.argv[1]))
lang = sys.argv[2]
failures = []


def check(ok, what):
    print(("ok   " if ok else "FAIL ") + what)
    if not ok:
        failures.append(what)


check(report["error"] == "", "the self-test ran: " + report["error"])
steps = {s["name"]: s for s in report["steps"]}
for name in ("start", "jpg", "proj", "tool", "pdftoday", "commands", "actions", "space", "home"):
    check(name in steps, f"step {name} was captured")
if failures:
    sys.exit(1)

# Typing into the overlay reaches the model: every keystroke, not the first.
jpg = steps["jpg"]
check(jpg["query"] == "jpg", f"typed query is jpg (got {jpg['query']!r})")
check("ext:jpg" in jpg["hint"], f"hint explains the type word (got {jpg['hint']!r})")
names = jpg["results"]
check(sorted(names) == ["beach.jpg", "holiday photo.jpeg"], f"jpg finds the two JPEGs only (got {names})")
# What is on screen (accessibility tree), not just the model, shows them.
a11y = jpg["accessibility"]
if not a11y:
    # SwiftUI builds its accessibility tree only for an assistive client;
    # the screenshots in the log show the rows instead.
    print("skip rows on screen: no accessibility tree without an assistive app (see the screenshots)")
else:
    check(any(l.startswith("beach.jpg") for l in a11y), "a beach.jpg row is on screen")
    check(not any(l.startswith("jpg notes.txt") or l.startswith("photo.png") for l in a11y), "no stale or wrong rows on screen")

# Sections: a Top Hit, then kinds; files before folders within equal matches.
proj = steps["proj"]
check(proj["query"] == "proj", f"second query typed after a reset (got {proj['query']!r})")
check({"Projects", "project.pdf", "project plan.txt"} <= set(proj["results"]), f"proj results (got {proj['results']})")
titles = [s["title"] for s in proj["sections"]]
check(len(titles) >= 3, f"proj results come in sections (got {titles})")
check(proj["results"][0] != "Projects", "a file, not the folder, is the top hit")
check(len(proj["chips"]) >= 2, f"kind chips offered for mixed results (got {proj['chips']})")

# Apps are one result; their contents are not indexed.
tool = steps["tool"]
check(tool["results"] == ["Tool.app"], f"Tool.app is one result (got {tool['results']})")

# The overlay starts as a slim bar (nothing typed, nothing opened yet).
check(steps.get("start", {}).get("compact") is True, "the overlay starts slim")
check(not jpg["compact"], "the overlay grows for results")

# Plain-language dates.
pdft = steps["pdftoday"]
check(pdft["results"] == ["project.pdf"], f"pdf today finds today's PDF (got {pdft['results']})")
check("modified:today" in pdft["hint"], f"the hint explains today (got {pdft['hint']!r})")

# The generic hint is not shown.
check(steps["proj"]["visible_hint"] == "", f"no generic hint for a plain word (got {steps['proj']['visible_hint']!r})")

# Commands with >.
cmds = steps["commands"]["commands"]
check(len(cmds) >= 1, f"> offers commands (got {cmds})")

# ⌘K actions for the selection.
acts = steps["actions"]["actions"]
check(len(acts) >= 6, f"⌘K lists the actions (got {acts})")

# Space after ↓ opens Quick Look (and is not typed into the query).
space = steps["space"]
check(space["query"] == "jpg", f"Space was not typed into the query (got {space['query']!r})")
check(space["browsing"], "↓ moved into the results")
check(space["quicklook"], "Space opened Quick Look")

# The empty box shows what was opened.
home = steps["home"]
check("beach.jpg" in home["results"], f"the empty box lists recently opened files (got {home['results']})")
check(not home["compact"], "the overlay grows for recent files")

# Language: the UI strings follow the language, and Arabic is right-to-left.
arabic = re.compile("[؀-ۿ]")
if lang == "ar":
    check(report["layout_rtl"], "Arabic lays out right-to-left")
    check(any(arabic.search(t) for t in titles), f"section titles are Arabic (got {titles})")
else:
    check(not report["layout_rtl"], "English lays out left-to-right")
    check("Top Hit" in titles, f"English section titles (got {titles})")

check(len(report["screenshots"]) >= 8, f"screenshots saved (got {report['screenshots']})")
sys.exit(1 if failures else 0)
