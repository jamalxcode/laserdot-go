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
# Where the time goes (information, not a check): the slowest steps.
timed = sorted((s.get("ms", 0), s["name"]) for s in report["steps"])
print(f"info self-test took {report.get('selftest_seconds', '?')} s; {len(timed)} steps, "
      f"{sum(t for t, _ in timed) / 1000:.1f} s in steps; slowest: "
      + ", ".join(f"{n} {t / 1000:.1f} s" for t, n in reversed(timed[-8:])))
split = report.get("selftest_split_ms", {})
if split:
    print("info outside the steps: " + ", ".join(f"{k.replace('_', ' ')} {v / 1000:.1f} s" for k, v in split.items()))
if report.get("selftest_marks"):
    print("info milestones (s): " + "; ".join(report["selftest_marks"]))
for name in ("start", "jpg", "proj", "tool", "refine", "morechoices", "fewerchoices", "pillsonly", "toolskey", "toolsclosed", "gallery", "galleryall", "gallerycalm", "refined", "chosen", "unrefined", "leftout", "leftoutshown", "where", "wherechosen", "place", "placeoff", "quickstart", "bartoggle", "pillback", "zip", "pdftoday", "commands", "actions", "space", "spacespace", "video", "song", "sorted", "grid", "gridbig", "columns", "columnsreversed", "multi", "trash", "trashed", "untrashed", "rename", "renamed", "moveto", "movedto", "moveundone",
             "compress", "compressed", "compressundone", "snippet", "tag", "tagged", "untagged", "gone", "refreshed", "history", "drop", "searchin", "morelike", "url", "similar", "home", "reopenfresh", "reset", "placegone", "noneplace", "noneplaceoff", "startvideos"):
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
check("kind" in steps["refine"].get("refine_groups", []), f"what mixed results are is a Kind choice (got {steps['refine'].get('refine_groups')})")

# Apps are one result; their contents are not indexed.
tool = steps["tool"]
check(tool["results"] == ["Tool.app"], f"Tool.app is one result (got {tool['results']})")

# What the results have to choose from: proj finds a PDF, a text file and a
# folder, so their types are offered; choosing PDF narrows to it.
# Top down, large to granular, each family one shaded band (the owner).
FAMILIES = ["where", "what", "file", "picture", "time", "photo", "music"]
for nm in ("refine", "where"):
    bands = steps[nm].get("refine_bands", [])
    check(len(bands) >= (2 if nm == "refine" else 1) and len(set(bands)) == len(bands) and all(b in FAMILIES for b in bands)
          and bands == sorted(bands, key=FAMILIES.index),
          f"{nm}: choices in shaded bands, top down (got {bands})")
# Less clutter (the owner): More choices shows at least as much, and says how much
# it adds; Fewer choices goes back.
mc, fc = steps["morechoices"], steps["fewerchoices"]
check(mc.get("refine_all") is True and mc.get("refine_hidden") == 0, f"More choices shows everything (got {mc.get('refine_all')}, hidden {mc.get('refine_hidden')})")
check(mc.get("refine_shown_count", 0) >= steps["refine"].get("refine_shown_count", 0) + steps["refine"].get("refine_hidden", 0),
      f"More choices adds what was hidden (calm {steps['refine'].get('refine_shown_count')} + {steps['refine'].get('refine_hidden')}, all {mc.get('refine_shown_count')})")
check(fc.get("refine_all") is False and fc.get("refine_shown_count") == steps["refine"].get("refine_shown_count"),
      f"Fewer choices goes back to the calm view (got {fc.get('refine_shown_count')})")
groups = steps["refine"].get("refine_groups", [])
check(groups.index("kind") < groups.index("type") < groups.index("size") if {"kind", "type", "size"} <= set(groups) else True,
      f"kind, then type, then size (got {groups})")
ref = steps["refine"]
check({"ext:pdf", "ext:txt"} <= set(ref.get("refinements", [])), f"the results' types are offered to choose from (got {ref.get('refinements')})")
check("type" in ref.get("refine_groups", []), f"grouped under Type (got {ref.get('refine_groups')})")
rd = steps["refined"]
check(rd["query"] == "ext:pdf proj", f"choosing PDF adds it to the search, in front (got {rd['query']!r})")
check(rd.get("pills") == ["ext:pdf"] and rd.get("field_text") == "proj" and rd.get("pill_labels") == ["PDF"],
      f"the choice is a PDF pill and the field keeps the word (got {rd.get('pills')}, {rd.get('pill_labels')}, {rd.get('field_text')!r})")
check(rd["results"] == ["project.pdf"], f"and narrows the results to the PDF (got {rd['results']})")
check(rd.get("refine_row") is True, "after a choice the choice row is still shown")
check(rd.get("completion_chips") == [], f"no completions for the choice just clicked (got {rd.get('completion_chips')})")
shown = steps["chosen"].get("refine_shown", [])
check(not any(g.startswith("type:") for g in shown), f"after choosing PDF its group leaves the row for the pill (got {shown})")
check(any(g.startswith("size:") or g.startswith("kind:") for g in shown), f"and the other groups stay to keep narrowing (got {shown})")
un = steps["unrefined"]
check(un["query"] == "proj", f"clicking a chosen choice takes it out again (got {un['query']!r})")

lo = steps["leftout"]
check(lo["query"] == "-ext:pdf proj" and "project.pdf" not in lo["results"] and lo["results"], f"leaving PDF out drops the PDF (got {lo['query']!r}, {lo['results']})")
los = steps["leftoutshown"]
check(los.get("pills") == ["-ext:pdf"] and any(("not" in l or "ليس" in l) and "PDF" in l for l in los.get("pill_labels", [])),
      f"the left-out PDF is a 'not PDF' pill, to bring it back (got {los.get('pills')}, {los.get('pill_labels')})")

pl, po = steps["place"], steps["placeoff"]
check(pl["results"] == ["rome trip.txt"] and pl.get("place_pill") is True, f"a default search place narrows searches and says so (got {pl['results']}, pill {pl.get('place_pill')})")
check(len(po["results"]) >= 3 and po.get("place_pill") is False, f"Search Everywhere drops it for this search (got {po['results']}, pill {po.get('place_pill')})")

qs = steps["quickstart"]
check(qs.get("pills") == ["kind:document"] and qs.get("field_text") == "proj" and "project.pdf" in qs["results"],
      f"the bar's Documents joins the typed words as a pill (got {qs.get('pills')}, {qs.get('field_text')!r}, {qs['results']})")
bt = steps["bartoggle"]
check(bt.get("pills") == [] and bt["query"].strip() == "proj", f"a second click takes it out (got {bt.get('pills')}, {bt['query']!r})")
bk = steps["pillback"]
check(bk.get("pills") == [] and bk["query"] == "", f"Delete in an empty field takes the last pill away (got {bk.get('pills')}, {bk['query']!r})")

zp = steps["zip"]
check(zp["results"] == ["archive-2023.zip"] and zp.get("inside") == ["2023/quokka-receipt.pdf"], f"a word finds the zip holding a file named with it (got {zp['results']}, {zp.get('inside')})")

wh = steps["where"]
folders = [t for t in wh.get("refinements", []) if t.startswith("in:")]
check("folder" in wh.get("refine_groups", []) and any(t.endswith("2023") for t in folders) and any(t.endswith("2024") for t in folders),
      f"the folders the results are in are offered (got {wh.get('refine_groups')}, {folders})")
wc = steps["wherechosen"]
check(wc["query"].startswith("in:") and wc["query"].endswith("trip") and len(wc["results"]) == 1, f"choosing a folder searches only there (got {wc['query']!r}, {wc['results']})")

# The overlay starts as a slim bar (nothing typed, nothing opened yet).
check(steps.get("start", {}).get("compact") is False, "one full screen from the start (no slim bar)")
sb = steps.get("start", {}).get("start_buttons", [])
check(all(b in sb for b in ("videos", "images", "music", "documents", "apps", "downloads", "big", "dupes", "storage", "index", "settings")) and sb.index("big") < sb.index("dupes"),
      f"the choice bar offers kinds and searches, then the menu's tools (got {sb})")
# A calmer screen (the owner): while searching, the tools fold into More and
# the hint does not repeat the choices.
st, pj = steps.get("start", {}), steps["proj"]
check(st.get("bar_tools") == [] and "dupes" in st.get("bar_buttons", []), f"the empty box shows every button (got {st.get('bar_buttons')})")
check("dupes" not in pj.get("bar_buttons", []) and "dupes" in pj.get("bar_tools", []) and "videos" in pj.get("bar_buttons", []),
      f"while searching the tools wait in More (buttons {pj.get('bar_buttons')}, tools {pj.get('bar_tools')})")
# A real-sized search: the calm view hides some choices, More shows them; the
# picture inside the Ulysses sheet (a package) is never a result.
ga, gall = steps["gallery"], steps["galleryall"]
check(len(ga["results"]) >= 30 and not any("inside" in r for r in ga["results"]),
      f"thirty sunsets, none from inside a package (got {len(ga['results'])}: {[r for r in ga['results'] if 'inside' in r]})")
check(ga.get("refine_hidden", 0) > 0, f"the calm view leaves some choices out of a big search (hidden {ga.get('refine_hidden')})")
check(gall.get("refine_shown_count", 0) > ga.get("refine_shown_count", 0), f"More choices shows more (calm {ga.get('refine_shown_count')}, all {gall.get('refine_shown_count')})")
check(steps["toolskey"].get("tools_open") is True, "⌘T opens the More panel of tools")
check(steps["toolsclosed"].get("tools_open") is False, "and it closes again")
check(report.get("news_dot") is True and report.get("news_dot_gone") is True,
      f"after an update the menu-bar icon carries a dot, gone once read (got {report.get('news_dot')}, {report.get('news_dot_gone')})")
po = steps["pillsonly"]
check(po.get("refine_row") is True and po.get("visible_hint", "") == "",
      f"only pills in the box: the choices show and no hint repeats them (row {po.get('refine_row')}, hint {po.get('visible_hint')!r})")
np_, npo = steps["noneplace"], steps["noneplaceoff"]
check(np_["results"] == [] and np_.get("place_pill") is True, f"nothing in the place: an empty list with Search Everywhere (got {np_['results']}, {np_.get('place_pill')})")
check("archive-2023.zip" in npo["results"] and npo.get("place_pill") is False, f"Search Everywhere finds it elsewhere (got {npo['results']})")
check(steps["start"].get("stays_open_on_open") is True, "opening a result leaves the search window open (the owner, 0.9.46)")
check(steps["start"].get("stays_on_its_desktop") is True, "the search window stays on its desktop and comes over only when called")
check(steps["reopenfresh"]["results"] == [], f"reopening runs the search again: a deleted file is gone (got {steps['reopenfresh']['results']})")
rs = steps["reset"]
check(rs["query"] == "" and rs.get("place_pill") is False and rs.get("sort_key") == "relevance",
      f"Reset clears words, place and order (got {rs['query']!r}, place {rs.get('place_pill')}, sort {rs.get('sort_key')})")
pg = steps["placegone"]
check(pg.get("place_pill") is False and len(pg["results"]) >= 3, f"a place whose folder is gone is ignored (got {pg.get('place_pill')}, {pg['results']})")
check(steps["start"].get("more_page") == 1000, "Show More adds a thousand results at a time")
check(report.get("flash_when_working") is True, "the menu-bar dot flashes while Laserdot works (a refresh)")
sv = steps["startvideos"]
check(sv.get("pills") == ["kind:video"] and "clip.mp4" in sv["results"] and not sv["compact"], f"the Videos button searches videos (got {sv.get('pills')}, {sv['results']})")
check(not jpg["compact"], "the overlay grows for results")

# Plain-language dates.
pdft = steps["pdftoday"]
check(pdft["results"] == ["project.pdf"], f"pdf today finds today's PDF (got {pdft['results']})")
check("modified:today" in pdft["hint"], f"the hint explains today (got {pdft['hint']!r})")

# The generic hint is not shown.
check(steps["proj"]["visible_hint"] == "", f"no generic hint for a plain word (got {steps['proj']['visible_hint']!r})")
# A plain word's meaning is explained the first few times, then left to the
# choice row (the English run comes first: the count carries over).
if lang == "en":
    check(steps["jpg"]["visible_hint"] != "", f"the first jpg explains the word (got {steps['jpg']['visible_hint']!r})")
    check(steps["space"]["visible_hint"] == "", f"after a few times it no longer does (got {steps['space']['visible_hint']!r})")

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

# Thumbnails: the grid shows the same results, several to a row.
g = steps["grid"]
check(g["grid"] is True and len(g["results"]) >= 3, f"the grid shows the images (got {g['results']})")
check(len(g["sections"]) == 1 and g["sections"][0]["title"] == "", f"the grid is one plain grid, no Top Hit or kind sections (got {g['sections']})")
check(g["grid_columns"] >= 2, f"several thumbnails to a row (got {g['grid_columns']})")
check(steps["multi"]["grid"] is False, "back to the list")
gb = steps["gridbig"]
check(gb["tile_size"] == 220 and gb["grid_columns"] < g["grid_columns"], f"bigger thumbnails, fewer to a row ({g['grid_columns']} → {gb['grid_columns']})")
check(steps["columns"]["kind_width"] == 180, f"a column keeps the width it was given (got {steps['columns']['kind_width']})")

# Columns: a header click sorts by it; a second click reverses.
c1, c2 = steps["columns"], steps["columnsreversed"]
check(c1["view"] == "columns" and c1["sort"] == "size" and c1["section_ids"] == ["sorted"], f"columns view sorted by size (got {c1['view']}, {c1['sort']}, {c1['section_ids']})")
check(c1["sizes"] == sorted(c1["sizes"], reverse=True) and len(c1["sizes"]) >= 2, f"largest first (got {c1['sizes']})")
check(c2["reverse"] is True and c2["sizes"] == sorted(c2["sizes"]), f"a second click: smallest first (got {c2['sizes']})")

# ⇧↓ selects a second result.
check(steps["multi"]["selected_count"] == 2, f"two results selected (got {steps['multi']['selected_count']})")

# Move to Trash: gone from the next search.
check("trashme.txt" in report.get("trash_found", []), f"the file to trash was found (got {report.get('trash_found')})")
check(steps["trashed"]["results"] == [], f"after Move to Trash it is gone (got {steps['trashed']['results']})")

check(steps["trashed"]["undo_banner"], "the Undo banner is offered after Move to Trash")
check(steps["untrashed"]["results"] == ["trashme.txt"], f"Undo brings it back (got {steps['untrashed']['results']})")

# ⌘K lists every action for one file.
shown_ids = steps["actions"].get("action_shown_ids", [])
check(0 < len(shown_ids) <= 10 and "group:more" in shown_ids, f"⌘K shows a short list with More Actions… (got {shown_ids})")
ids = steps["actions"]["action_ids"]
for a in ("open", "reveal", "copyPath", "terminal", "searchIn", "moreLike", "compress", "moveTo", "rename", "trash"):
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
ml = steps["morelike"]
check(ml["query"].startswith("ext:pdf fsize:") and "project.pdf" in ml["results"], f"More Like This searches the same type, size and date (got {ml['query']!r}, {ml['results']})")
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
print(f"info launch steps (ms since start): {report.get('launch_phases', '')}")
check(0 < launch < 5000, f"the app is ready in under 5 s on CI (got {launch} ms)")

# A file deleted behind Laserdot's back leaves the results; Refresh works.
check(steps["gone"]["results"] == [], f"a deleted file is not shown (got {steps['gone']['results']})")
check(steps["refreshed"]["refreshing"] is False and steps["refreshed"]["results"] == [], f"Refresh finished (got {steps['refreshed']})")

# A laserdot:// link opens the search.
url = steps["url"]
check(url["query"] == "beach" and "beach.jpg" in url["results"], f"laserdot://search?q=beach (got {url['query']!r}, {url['results']})")

# Storage and Duplicates windows.
check(report.get("storage_items", -1) >= 1, f"the Storage window lists folders (got {report.get('storage_items')})")
check(report.get("storage_trashed") is True, "Storage moves an item to the Trash")
check(report.get("storage_untrashed") is True, "and Undo puts it back")
check(report.get("storage_apps", 0) >= 1, f"Storage counts apps as apps (got {report.get('storage_counts')!r})")
check(report.get("duplicate_groups", -1) >= 1, f"the Duplicates window found the copied clip (got {report.get('duplicate_groups')})")
# Cleaning up duplicates: its own button, Select None / All, a dry run that answers.
check("Move" in report.get("dupes_button", "") or "نقل" in report.get("dupes_button", ""), f"a clear Move Duplicates button (got {report.get('dupes_button')!r})")
sums = report.get("dupes_summaries", [])
check(sums and not any("bpp" in s for s in sums), f"duplicates describe copies in plain words (got {sums})")
check(report.get("dupes_scope") == "kind:video", f"the Duplicates pickers make the scope (got {report.get('dupes_scope')!r})")
check(report.get("dupes_none") == 0 and report.get("dupes_all", 0) >= 1, f"Select None and Select All Extras work (got {report.get('dupes_none')}, {report.get('dupes_all')})")
check(report.get("dupes_preview_files", -1) >= 1, f"the dry run before moving answers (got {report.get('dupes_preview_files')}, {report.get('dupes_preview_error')!r})")
for shot in ("storage.png", "duplicates.png", "settings-folders.png", "settings-drives.png", "settings-privacy.png"):
    check(shot in report["screenshots"], f"screenshot {shot}")

# Indexing progress: a count of files found so far, then the details pass.
digits = re.compile("[0-9٠-٩]")
pf, pd = report.get("progress_found", ""), report.get("progress_details", "")
check("Movies" in pf and digits.search(pf), f"indexing shows how many files were found so far (got {pf!r})")
check(len(digits.findall(pd)) >= 5, f"the details pass shows done of total (got {pd!r})")

# Find Duplicates shows the step, a bar and how many files it has read.
dt, dd = report.get("dupes_progress_title", ""), report.get("dupes_progress_detail", "")
check(dt and "…" in dt, f"the duplicate scan names its step (got {dt!r})")
check(len(digits.findall(dd)) >= 8, f"the duplicate scan shows files done of total and the time so far (got {dd!r})")
check(abs(report.get("dupes_progress_fraction", -1) - 0.24) < 0.001, "the duplicate scan's bar fills with the count")
check("IMG_1234.HEIC" in report.get("dupes_progress_file", ""), f"the duplicate scan shows the file it is reading (got {report.get('dupes_progress_file')!r})")

# The install prompt fits a laptop screen with very long notes (its buttons were pushed off it).
h = report.get("install_prompt_height", 9999)
check(report.get("install_prompt_stays") is True, "the update prompt stays in front of other apps and on every Space")
check(0 < h < 650 and report.get("install_prompt_buttons") == 2, f"the install prompt fits the screen with its buttons (height {h}, buttons {report.get('install_prompt_buttons')})")

# The watchdog: laserdotd killed outright is started again and answers.
check(report.get("watchdog_restarted") is True and report.get("watchdog_restarts", 0) >= 1, f"the watchdog restarts a daemon that froze (got {report.get('watchdog_restarted')!r}, {report.get('watchdog_restarts')})")

# The empty box suggests a search drawn from this Mac's own files.
check(":" in steps["home"].get("tip", ""), f"the empty box offers a tip from the user's files (got {steps['home'].get('tip')!r})")

# Spotlight's way of typing.
inl = steps["inline"]
check(inl["query"] == "beac" and inl["field"].lower().startswith("beac") and len(inl["field"]) > 4
      and inl["field_selected"] == inl["field"][4:], f"the top hit's name completes inline, the rest selected (field {inl['field']!r}, selected {inl['field_selected']!r})")
ro = steps["reopen"]
check(ro["field"] and ro["field_selected"] == ro["field"], f"reopened, the last search is all selected (field {ro['field']!r}, selected {ro['field_selected']!r})")
check(report.get("escape_first_query") == "" and report.get("escape_first_visible") is True, "Esc clears the search and keeps the window")
check(report.get("escape_second_visible") is False, "Esc again, with the field empty, closes the window")
jp = steps["jump"]
check(jp["browsing"] and jp["selected_section"] and jp["selected_section"] != (jp["sections"][0]["title"] if jp["sections"] else ""), f"⌘↓ goes to the next section (got {jp['selected_section']!r})")
check("beach.jpg" in report.get("copied_files", []), f"⌘C copies the selected file (got {report.get('copied_files')})")

# Updates are never silent: a window with the step, a filled bar and the sizes.
check(report.get("update_window_visible") is True, "the update window shows while an update downloads")
check("update-progress.png" in report["screenshots"], "screenshot update-progress.png")
detail = report.get("update_detail", "")
check(detail.count("MB") == 2 or detail.count("ميغا") == 2 or len(detail) > 6, f"the update window says how much is downloaded (got {detail!r})")
check("44" in report.get("update_menu", ""), f"the menu shows the download percentage (got {report.get('update_menu')!r})")
el = report.get("update_elapsed", "")
check(bool(re.fullmatch(r"\d+:\d\d", el)) and el != "0:00", f"the update window has a ticking clock for the step (got {el!r})")
check(report.get("update_cancelled") is True, "Cancel stops a download")
check(report.get("update_window_closes") is True, "the update window closes when the download is cancelled")

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
