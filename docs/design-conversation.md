# Design conversation: from a working port to an app people use

After the Go port reached parity with the Rust build (Issue 002, phases G1–G4), the owner used the app every day
and asked for changes. This page records that conversation — what was asked, what was decided, and why — so the
reasons stay with the code. The binding rules that came out of it are also in the spec (version noted per item) and
summarized in Issue 002's design log. Releases are on the
[Releases page](https://github.com/jamalxcode/laserdot-go/releases).

The owner's guiding requests throughout: **make it work like Spotlight, ask nothing of the user, keep it private.**

---

## 1. Making the app usable (0.2.0 – 0.2.2)

**Asked:** an app, not a command line: the daemon started by the app, a Dock icon and a menu-bar icon, an update
button, packaged as a disk image.

**Decided:**
- Laserdot is both a Dock app (main window) and a menu-bar app (⌥Space overlay). It starts `laserdotd` and stops
  it when it quits.
- The app crashed at launch because SwiftPM looks for its resources beside the `.app`; it now looks in
  `Contents/Resources` first, and CI launches the bundled app to catch this class of bug.
- Indexing failures were silently ignored; they now show an alert, and the status line shows progress.
- Releases are disk images published by the **Release** workflow.

**Update button — the one network exception.** Laserdot's promise is that it never touches the network. The owner
granted an exception for updates only: a separate helper, **Laserdot Updater**, may contact GitHub to find and
download a release (spec §7.4). Laserdot and `laserdotd` stay network-blocked; CI fails if network code appears
anywhere else in the app.

**Opening files in Preview or VLC.** Results would not open ("no permission") because the app shell applied the
macOS sandbox to itself, and a sandboxed app may only hand over files it could read itself. The owner decided:
*"remove the sandbox, Laserdot will self-discipline and just not access the network because it is coded that way."*
The app shell is no longer OS-sandboxed; it has no network code (CI-enforced); `laserdotd`, which reads every file,
keeps its OS network block (spec §7.1, v1.17).

## 2. Updates without tokens (0.2.3, 0.2.8)

**Asked:** no tokens, no steps — *"I don't want any input or manipulation from my side"* — and private.

**Options weighed:** a private release repository read with an embedded SSH deploy key (works, but complex and a
key ships inside the app) versus making the repository public.

**Decided:** the owner chose public code — *"code can be public, but no one can touch it except with my
permission."* On GitHub only the owner can push or publish releases; outside changes arrive as pull requests the
owner must merge, and CI runs those with a read-only token and no secrets. The updater downloads public releases
with no credentials.

**Then (0.2.8):** checks failed with HTTP 403 — GitHub's anonymous API allows 60 requests an hour per network
address, often used up on shared networks. The updater now reads the newest version from where
`github.com/<repo>/releases/latest` redirects and downloads the disk image by its fixed name; the API is only a
fallback. CI runs the real updater against GitHub. Updates downloaded in the background install when the app quits
(0.4.0).

## 3. Search that behaves like Spotlight (0.2.4 – 0.3.0)

| Asked | Decided | Version |
|---|---|---|
| Search on every keystroke | Already the case; the visible problems were elsewhere (below) | — |
| `jpg` or `avi` should mean pictures or video | **Type words:** an extension or kind word means that type (`jpg` = .jpg/.jpeg/.jpe, `videos` = kind:video, Arabic too); quotes search names | 0.2.4 (spec v1.18) |
| Menu items all greyed out | A dialog had opened behind other windows; every dialog now comes to the front | 0.2.4 |
| Files first, not folders | Among equally good matches: files, then folders, then hidden items (`.obsidian`, `.git`) | 0.2.5 (v1.18) |
| Still wrong results after updating | An old `laserdotd` survived a force quit and the new app used it; the daemon now reports the app's version and a mismatched one is replaced | 0.2.5 |
| Still wrong rows for `jpeg` | The count was right but the list kept rows of the first keystroke (two identities per row in SwiftUI's lazy list); rows have one identity and the list is rebuilt per answer | 0.2.6 |
| `ppt` should list all PowerPoints | Format families (`ppt` = ppt/pptx/pps/ppsx, `doc`, `xls`; `powerpoint`, `excel`) | 0.2.6 |
| `jpg` in the ⌥Space window showed random files | SwiftUI's text field in the non-activating overlay passed on only the first keystroke; the query field is now an AppKit `NSTextField` | 0.2.9 |
| `claude.app` found nothing | Applications were never indexed, and an app would be thousands of entries: apps and other packages are one result (contents not indexed); the Applications folders are indexed once, automatically | 0.3.0 (v1.19) |

Each of these was diagnosed from the owner's screenshots; the last three led to the self-test (section 5), so the
app's real screen is now checked in CI rather than only the engine.

## 4. Apple's Human Interface Guidelines (0.2.7)

**Asked:** follow Apple's standards.

**Decided:** a Settings window (⌘,) instead of options scattered in menus; Edit ▸ Find (⌘F); a Help menu item for
the search syntax instead of an empty help book; standard window chrome in the main window (the floating card is
the overlay's look only); one status bar; the selected row in the accent colour with white text, grey when the window
is not active; VoiceOver no longer reads invisible shortcut buttons; destructive alerts use verb buttons and Cancel;
Open at Login. Later (0.5.1): Settings opened from the menu-bar icon comes to the front and shows where the index is.

## 5. The owner's critique of the app (0.4.0)

**Asked:** "fix all except 5" (5 was an Apple Developer ID signature, which needs a paid account).

| # | Critique | Decided |
|---|---|---|
| 1 | Only searches what you add | The home folder and apps are indexed by default, without asking; `~/Library` is skipped except iCloud Drive (spec §13.6a) |
| 2 | Doesn't learn | Opened results rank higher among equally good matches; the history stays on this Mac (spec §6.2) |
| 3 | No preview | A preview pane with Quick Look thumbnails; thumbnails in rows |
| 4 | One long list | A Top Hit, then sections by kind |
| 6 | The UI was untested | **The self-test:** CI launches the real app, types into the ⌥Space window with key events, checks what it shows and saves screenshots |
| 7 | Asks too much | No first-run prompt; background updates install on quit |
| 8 | Few shortcuts | ⌘1–⌘9, ⌘Y |
| 9 | Arabic layout unchecked | The self-test runs in Arabic too — and found the layout was secretly left-to-right; it is now right-to-left on every window |
| 10 | Hidden search language | Suggestion chips: completions, and the kinds a result list mixes |

## 6. The owner's critique of the interface (0.5.0, 0.5.1)

**Asked:** make it more intuitive, enjoyable, clever and time-saving — "do all of it".

- **Cleaner:** short paths (`~ › Documents › AI Code`, full path on hover); the hint only when it says something about
  what was typed; a lock icon instead of the network sentence; the empty box shows recently opened files, recent
  downloads and pinned searches instead of code-like examples; the overlay starts as a slim bar.
- **Clever:** plain-language words — `today`, `last week`, `big`, `4k`, `screenshots`, Arabic phrases (spec §2.7,
  v1.21); matching letters in bold; five results per section with **Show all**; friendly dates ("Today at 9:04",
  "3 days ago").
- **Time-saving:** ⌘K actions (Open with…, Copy file or path, Share, Rename, Move to Trash); a key bar showing the
  keys that work now; `>` commands; selecting several results; indexing progress; the preview plays video and audio
  and pages through PDFs.
- **Space for Quick Look (0.5.1):** as in Finder, once ↑/↓ or a click has moved into the results, Space opens Quick
  Look (pictures, videos, songs); while typing, Space types — so multi-word searches still work.

## 7. Drives and confidentiality (0.5.2)

**Asked:** where a drive's index is stored; and that the index of a read-only drive, kept on this Mac, be deleted
when the drive can no longer be seen — checked at start, on quit and periodically — *"to keep confidentiality safe."*

**Decided:**
- A writable drive keeps its index on itself (`/Volumes/<drive>/.laserdot/`), paths relative to the drive, so it
  travels between Macs. A read-only drive's index lives in `~/Library/Application Support/laserdot/drives/`.
- That on-Mac index is now deleted as soon as its drive is not mounted: at daemon start, every minute, whenever a
  drive leaves, and on quit (spec §9, v1.22). The trade-off, accepted: such a drive is indexed again when it returns.
  If the volume list comes back empty while drives are attached, nothing is deleted (a failed listing, not departed
  drives).

**Asked:** is there a quick way to tell whether a drive needs a full or partial rebuild?

**Answered (no change):** it already decides by itself. On mount, Laserdot lists the drive's names, sizes and dates
(about 100,000 files a second), compares them with the index, and reads only new or changed files; renames and moves
are recognized. A full rebuild is only for a damaged index. Skipping even the listing with the drive's change journal
was ruled out in the original design: FSEvents ids belong to one Mac, and drives move between Macs (Issue 001,
Design Decision Log).

## 8. The second critique (0.6.0)

**Asked:** "fix all except 15" (15 was, again, the Apple Developer ID signature).

| # | Critique | Decided |
|---|---|---|
| 1 | You can't see or change what's indexed | Settings ▸ Folders: every indexed folder (add, remove) and **Never Index** folders, skipped for good and removed from the index |
| 2 | No way to clear history | Settings ▸ General ▸ Clear History: recent searches and the opened-results history (pins stay) |
| 3 | The index goes stale when Laserdot is closed | Open at login on by default (once, for the copy in Applications) |
| 4 | No "What's new" | `CHANGELOG.md` is bundled; its notes show in the update prompt, after an update, and in Help |
| 5 | Space needs ↓ first | A second Space in a row previews the highlighted result |
| 6 | Only 100 results | Scrolling to the end loads more; an answer of one kind only (all pictures) is not capped at five |
| 7 | Short typos | Three-letter words forgive swapped letters (`pfd` → `pdf`) — not any edit, which would match almost everything; "showing similar names" when nothing matched exactly |
| 8 | Contents only in three folders | The whole home folder, by default |
| 9 | Old-looking Duplicates window | Copies side by side, with thumbnails, the recommended one marked |
| 10 | Drives only in menus | Settings ▸ Drives: each drive, where its index is and how big, its actions |
| 11 | No tour | A three-page welcome on first launch (Help ▸ Welcome Tour) |
| 12 | No bug reports | Help ▸ Save Diagnostics: a file you choose to share, with every path removed |
| 13 | VoiceOver untested | CI now reads the running app with the accessibility API, as VoiceOver does — GitHub's Mac allows it, and it reads the result rows |
| 14 | A flaky test | Mounting a disk image is retried, in the app's updater too |

Found on the way by the new checks: Quick Look sometimes stayed open when asked to close, and would not reopen
after being closed with Space; both fixed.

---

## How the work was done

- Every change: on a branch, `gofmt`/`vet`/tests locally, CI on Linux and macOS (the Swift app compiles only there),
  then fast-forward to `main` and a release.
- The owner's screenshots were the bug reports; since 0.4.0 the self-test's screenshots let the developer see the app
  too, in English and Arabic.
- The spec and Issue 002 were updated with every behavior change.
