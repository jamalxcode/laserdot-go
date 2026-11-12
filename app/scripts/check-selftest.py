#!/usr/bin/env python3
"""Judges the report of the app's self-test (app/Sources/Laserdot/SelfTest.swift).

Usage: check-selftest.py report.json en|ar
The fixtures (made by the CI step): beach.jpg, holiday photo.jpeg, photo.png,
"jpg notes.txt", project.pdf, "project plan.txt", Projects/readme.md,
Tool.app/Contents/Info.plist, trashme.txt, renameme.txt, memo.txt ("zebra" inside),
clip.mp4, "clip copy.mp4", song.mp3.
"""
import json
import os
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
for name in ("start", "jpg", "proj", "tool", "pdftoday", "commands", "actions", "space", "spacespace", "video", "song", "sorted", "multi", "trash", "trashed", "untrashed", "rename", "renamed", "moveto", "movedto", "moveundone",
             "compress", "compressed", "compressundone", "snippet", "tag", "tagged", "untagged", "history", "drop", "searchin", "url", "similar", "home"):
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

# Space twice (no ↓) previews; only the first space is typed.
ss = steps["spacespace"]
check(ss["query"] == "jpg ", f"one space typed, the second previewed (got {ss['query']!r})")
check(ss["quicklook"], "a second Space opened Quick Look")

# A swapped-letter typo finds the file and says so.
sim = steps["similar"]
check("project.pdf" in sim["results"], f"pfd finds project.pdf (got {sim['results']})")
check(sim["similar"], "the hint says only similar names were found")

# What an accessibility client (VoiceOver) reads, when CI could run one.
ax_file = os.path.join(os.path.dirname(sys.argv[1]), "ax.txt")
if os.path.exists(ax_file):
    ax = open(ax_file, encoding="utf-8", errors="replace").read()
    if "untrusted" in ax or not ax.strip():
        print("skip accessibility tree: this CI machine does not allow an accessibility client")
    else:
        check("beach.jpg" in ax, "VoiceOver can read the beach.jpg row")
        check("jpg notes.txt" not in ax, "VoiceOver reads no stale rows")

# Videos and songs: selected and previewed with a player, without a crash.
check(steps["video"]["selected"] == "clip.mp4", f"the video is selected and previewed (got {steps['video']['selected']!r})")
check(steps["song"]["selected"] == "song.mp3", f"the song is selected and previewed (got {steps['song']['selected']!r})")

# Sorted by size: one list, largest first.
srt = steps["sorted"]
check(srt["section_ids"] == ["sorted"], f"sorted results are one list (got {srt['section_ids']})")
check(srt["sizes"] == sorted(srt["sizes"], reverse=True) and len(srt["sizes"]) >= 2, f"largest first (got {srt['sizes']})")

# ⇧↓ selects a second result.
check(steps["multi"]["selected_count"] == 2, f"two results selected (got {steps['multi']['selected_count']})")

# Move to Trash: gone from the next search.
check("trashme.txt" in steps["trash"]["results"], f"the file to trash was found (got {steps['trash']['results']})")
check(steps["trashed"]["results"] == [], f"after Move to Trash it is gone (got {steps['trashed']['results']})")

check(steps["trashed"]["undo_banner"], "the Undo banner is offered after Move to Trash")
check(steps["untrashed"]["results"] == ["trashme.txt"], f"Undo brings it back (got {steps['untrashed']['results']})")

# ⌘K lists every action for one file.
ids = steps["actions"]["action_ids"]
for a in ("open", "reveal", "copyPath", "terminal", "searchIn", "compress", "moveTo", "rename", "trash"):
    check(a in ids, f"⌘K offers {a} (got {ids})")

# Rename, Move to…, Undo.
check(steps["renamed"]["results"] == ["renamed ok.txt"], f"Rename worked (got {steps['renamed']['results']})")
check(steps["movedto"]["paths"] == ["/Moved Here/renamed ok.txt"], f"Move to… worked (got {steps['movedto']['paths']})")
check(steps["moveundone"]["paths"] == ["/renamed ok.txt"], f"Undo moved it back (got {steps['moveundone']['paths']})")

# Compress, Undo.
check("beach.jpg.zip" in steps["compressed"]["results"], f"Compress made beach.jpg.zip (got {steps['compressed']['results']})")
check("beach.jpg.zip" not in steps["compressundone"]["results"], f"Undo removed the zip (got {steps['compressundone']['results']})")

# Words inside a document: found, and the preview shows the passage.
snip = steps["snippet"]
check("memo.txt" in snip["results"], f"zebra finds memo.txt by its words (got {snip['results']})")
check("zebra" in snip["snippet"], f"the preview shows where (got {snip['snippet']!r})")

# Tags: added from ⌘K, searchable, and Undo takes them off.
check(steps["tagged"]["results"] == ["beach.jpg"], f"tag:red finds the tagged file (got {steps['tagged']['results']})")
check(steps["untagged"]["results"] == [], f"Undo removed the tag (got {steps['untagged']['results']})")
for a in ("tags",):
    check(a in steps["actions"]["action_ids"], f"⌘K offers {a}")

# ↑ in the empty box: the last search comes back.
check(steps["history"]["query"] == "holiday", f"↑ recalls the last search (got {steps['history']['query']!r})")

# A folder dropped on the window.
drop = steps["drop"]
check(drop["query"].startswith('in:"') and "readme.md" in drop["results"], f"dropping Projects searches in it (got {drop['query']!r}, {drop['results']})")

# Search in This Folder.
sin = steps["searchin"]
check(sin["query"].startswith('in:"') and sin["query"].rstrip().endswith('/Projects"'), f"the query is in:\"…/Projects\" (got {sin['query']!r})")
check("readme.md" in sin["results"] and "beach.jpg" not in sin["results"], f"only that folder's files (got {sin['results']})")

# Battery pausing, resizing, privacy page, cold launch.
check(report.get("paused_on") is True and report.get("paused_off") is False, f"background work pauses and resumes (got {report.get('paused_on')}, {report.get('paused_off')})")
check(report.get("saved_width") == 1000 and report.get("saved_height") == 600, f"the overlay size is remembered (got {report.get('saved_width')}x{report.get('saved_height')})")
items = {i["id"]: i["bytes"] for i in report.get("privacy_items", [])}
check(items.get("index", 0) > 0, f"the Privacy page shows the index size (got {items})")
# What could not be read is listed.
check("/Locked" in report.get("unreadable", []), f"the locked folder is listed as unreadable (got {report.get('unreadable')})")

# Remembered passphrases (login Keychain).
kc = report.get("keychain", "")
if kc.startswith("unavailable"):
    print(f"skip Keychain: this CI machine's Keychain is locked ({kc})")
else:
    check(kc == "ok", f"a passphrase is saved, read back and forgotten (got {kc!r})")

# Erase All Laserdot Data (last).
er = report.get("erase", {})
check(er.get("data_before") is True and er.get("data_gone") is True, f"Erase deletes the data folder (got {er})")
check(er.get("daemon_stopped") is True, f"Erase stops the index (got {er})")
check(er.get("welcomed") is True and er.get("no_reindex") is True, f"after Erase: no tour, no automatic indexing (got {er})")
check(er.get("history_gone") is True, f"Erase forgets the search history (got {er})")
if not kc.startswith("unavailable"):
    check(er.get("passphrases") == 0, f"Erase forgets the passphrases (got {er.get('passphrases')})")

hung = report.get("hung_place_seconds", -1)
check(0 <= hung <= 12, f"a drive that never answers is skipped after its time limit (took {hung} s)")
check(report.get("refused_places") == ["refused"], f"a refused place is reported (got {report.get('refused_places')})")
places = report.get("permission_places", [])
check(any(p.endswith("/Desktop") for p in places) and any(p.endswith("/Documents") for p in places),
      f"the permission step covers Desktop and Documents (got {places})")

print(f"info ready to search: {report.get('ready_ms')} ms after start")
launch = report.get("launch_ms", -1)
print(f"info cold launch to ready: {launch} ms (target < 300 ms on a real Mac)")
check(0 < launch < 5000, f"the app is ready in under 5 s on CI (got {launch} ms)")

# A laserdot:// link opens the search.
url = steps["url"]
check(url["query"] == "beach" and "beach.jpg" in url["results"], f"laserdot://search?q=beach (got {url['query']!r}, {url['results']})")

# Storage and Duplicates windows.
check(report.get("storage_items", -1) >= 1, f"the Storage window lists folders (got {report.get('storage_items')})")
check(report.get("duplicate_groups", -1) >= 1, f"the Duplicates window found the copied clip (got {report.get('duplicate_groups')})")
for shot in ("storage.png", "duplicates.png", "settings-folders.png", "settings-drives.png", "settings-privacy.png"):
    check(shot in report["screenshots"], f"screenshot {shot}")

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

check("settings.png" in report["screenshots"], "Settings opened and came to the front")
check(len(report["screenshots"]) >= 8, f"screenshots saved (got {report['screenshots']})")
sys.exit(1 if failures else 0)
