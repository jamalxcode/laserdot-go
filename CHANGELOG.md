# Changelog

What's new in each version, in plain words. `app/scripts/bundle.sh` copies the section for the version it builds
into the app (`WhatsNew.txt`), which Laserdot shows when it updates.

## 0.9.28

- Choose where to search: the choice row now offers **Drive** (This Mac or each attached drive) and **Folder** (the
  folders your results are in, with how many in each). Click one to search only there; click it again to search
  everywhere.
- Leave a choice out: ⌥-click a choice (or right-click ▸ Leave Out) to drop it from the results, such as "not in
  Downloads" or "not PDF". It shows struck through; click it to bring it back.
- Searches find files inside zip archives: a word matching a file inside a zip finds the zip, marked "Contains …".
  Only the zip's list of contents is read; nothing is unpacked.
- More date words: "march 2024", "last summer", "summer 2023", "between jan and mar", and in Arabic "مارس 2024",
  "الصيف الماضي", "بين يناير ومارس".
- More Like This (⌘K on a result): searches for files of the same type, a similar size and a near date, as words you
  can change.
- Settings ▸ General ▸ Search in: Everywhere, This Mac, or one folder. The search window says where it is searching,
  and Search Everywhere widens one search.
- Fixed: the app and the command line opening a brand-new index at the same moment could fail. <!-- quiet -->

## 0.9.27

- iPhone photos (HEIC), AVIF and camera RAW pictures are now compared in Find Duplicates — a HEIC and a JPEG copy of
  the same photo are found as similar. macOS reads them for Laserdot; nothing leaves your Mac.
- The Storage window can clean up: right-click a folder or file to show it in Finder or move it to the Trash (with a
  clear confirmation of what moves and how much space it frees; nothing is deleted, and Undo puts it back).
- Searches on very big indexes are about twice as fast (plain words and wildcards like report*).
- Less background work: on Mac drives the check for missed changes runs every 30 minutes instead of every 5, and goes
  back to 5 if it ever finds something the system's change events missed.

## 0.9.26

- Drive indexing can be stopped and continued: Drives ▸ your drive ▸ Stop Indexing keeps what was read, and the
  drive stays searchable; Continue Indexing goes on from there.
- Ejecting a drive works even during a duplicate scan: the scan skips that drive's files instead of holding it.
- A calmer search window: refresh, sort, pin and the filter list are in one "…" menu (⌘R, ⌘D and ⌘/ still work); the
  thumbnail size slider shows only with thumbnails.
- The choices under the search wrap onto more lines instead of running off the edge.
- The hint line explains a word like "jpg" the first few times, then leaves it to the choices.
- Faster first indexing of very big folders: Laserdot may use more memory while it builds the index (it hands it back
  afterwards), instead of slowing down near its old limit.
- Less disk writing: the saved copy of the index is written once things are quiet, not every minute while files
  change.

## 0.9.25

- The Storage window shows apps at their real size. Apps (and other packages, such as libraries) are one item in the
  search, so they used to count as nearly nothing — /Applications showed 25 KB. Laserdot now measures each one once
  and remembers it until it changes; while it measures, a size says "at least …" and fills in by itself.
- Apps are counted as apps: "36 apps · 4 files" instead of "4 files".
- Numbers are written properly ("173,959 files"), and an empty folder says "Empty" instead of "Zero KB".

## 0.9.24

- Fixed: after picking a choice (say 1440p), the other choices — sound, length, shape… — disappeared, leaving only a
  "1440p" suggestion. They now stay, so you can keep adding to your search. The suggestions for a filter you type by
  hand show above the choices instead of replacing them.

## 0.9.23

- Tidier choices: once you pick a resolution (or a type, a camera…), the other values of that kind go away and your
  pick moves to the front with a ✓ — click it to take it out. Every other kind of choice (location, date, camera,
  size…) stays, so you can keep narrowing.

## 0.9.22

- Choosing from your results now works like a shop's filters: after picking a resolution, every other choice stays
  (sound, shape, length, codec…) so you can keep adding. What you picked shows highlighted with a ✓ — click it again
  to take it out, or pick another value of the same kind to switch. The numbers always say how many results each
  choice gives with your other picks; a choice that would leave nothing is greyed out.

## 0.9.21

- Ejecting a drive always works while Laserdot runs. Laserdot no longer keeps a drive's index file open: it opens it
  only while it is indexing or catching up, and searching a drive needs nothing on the drive. If you eject in the
  middle of that work, Laserdot stops at once, keeps what it read, and lets the drive go. (Before, you had to quit
  Laserdot to eject a drive.)
- Choose from what your results have. Search for videos and Laserdot offers the resolutions among them (4K, 1080p…),
  with sound or without, portrait or landscape, length and codec, each with how many results have it; pictures offer
  shape, camera, location, screenshots and Live Photos; music offers artist, album and genre; documents offer their
  types; everything offers date and size. Click one to narrow the search (it is added to the search, so you can see
  and change it).
- Typing feels lighter: a search that a newer keystroke has replaced is skipped instead of run.

## 0.9.20

- Indexing never slows your Mac down any more. Laserdot's engine now runs as a background task, like Spotlight's: your
  apps use the disk first, and indexing waits while they are busy. (Indexing an 8 TB drive could make the whole Mac
  slow and show the spinning beachball.)
- Updating while a drive is being indexed works smoothly: indexing pauses during the install and carries on
  afterwards.
- The update window shows how long the current step has taken, and no step can hang: a tool that stops answering is
  stopped after a few minutes. Save Diagnostics now lists each update step and how long it took.
- A drive whose first index was cut short by an older version now starts over with the new, low-memory indexer,
  instead of being checked all at once with the whole drive held in memory.
- Indexing opens only a few files at a time, whatever the size of the drive.

## 0.9.19

- A big external drive is searchable from its first seconds of indexing, not after hours: results appear as its
  folders are read, top folders and recently changed ones first. Searching in a folder of a drive still being
  indexed moves that folder to the front.
- Unplugging, ejecting or quitting in the middle of a drive's first index stops it at once and keeps what was read.
  Next time the drive is plugged in, it carries on from where it stopped, in the background.
- Indexing a drive uses little memory however big the drive is, and adjusts how many folders it reads at once to the
  drive (spinning disks like few).
- A drive coming back is checked quickly: on Mac-formatted drives (APFS, Mac OS Extended) only folders that changed
  are read again, with a full check once a week. Other drives (exFAT, FAT, NTFS) are read whole, as before.
- Very broad searches on huge drives (such as re:.) use far less memory.

## 0.9.18

- Fixed: Laserdot could stop responding (even to Quit) while it looked at a big external drive, such as an 8 TB disk.
  Searching, opening results and quitting now carry on normally however long a drive takes.
- Searches have a line of their own to Laserdot's engine, so nothing slow can hold up typing.
- Quitting never hangs: if the engine is busy, Laserdot stops it without waiting.
- The "index this drive?" question comes within seconds, even for a huge drive ("more than N files").

## 0.9.17

- Optional reminders (Settings ▸ Reminders, off unless you turn them on): check for duplicates every month or
  every 3 months, and a heads-up when your disk is nearly full. A scan resets the clock, so you are only reminded
  when you haven't looked. Notifications are made on your Mac; nothing is sent anywhere.
- After a cleanup Laserdot says what it achieved — the space moved out and the total it has cleared for you — and
  offers the sensible next step: open the Trash, continue the scan, or see what else takes space.
- No duplicates found? It suggests looking at what takes the most space instead.
- The empty search box suggests a search based on your own files ("812 of your videos are 4K: res:4k"), a different
  one each time; one click runs it.
- Results stop at a clear point: "Show More (100 of 2,431)" instead of loading endlessly as you scroll.
- Duplicates remembers what you looked in and whether you chose identical copies only.

## 0.9.16

- Typing in the ⌥Space window works like Spotlight:
  - The top result's name completes as you type, the rest shown selected: keep typing over it, press → or Tab to
    take it, Delete to drop it, Return to open the top result.
  - Esc clears the search; Esc again closes the window.
  - Reopening shows your last search, all selected: type to replace it, or press Return or ↓ to use it again.
  - ⌘↓ and ⌘↑ jump to the next or previous group of results.
  - ⌘C copies the selected file, to paste in Finder or Mail.
  - Drag the window anywhere: it opens there next time.
- Laserdot's own extras stay: filters such as kind:video, the hint line, Space for Quick Look, ⌘K actions, > commands
  and recent files when the box is empty.

## 0.9.15

- Steadier: a bug in Laserdot's background engine is caught, written to a log on your Mac, and the engine keeps
  running instead of stopping.
- If the engine stops or stops answering, Laserdot notices within a minute and restarts it by itself.
- Fixed: if Laserdot was quit or crashed during the first index of a folder, that folder could stay empty. It is now
  indexed again by itself the next time Laserdot starts.
- A drive or network share that stops answering no longer freezes indexing: its folder is skipped after 30 seconds
  and listed.
- Indexing a drive, Refresh and content search no longer give up after ten minutes or block searching meanwhile;
  indexing a drive shows how many files it has found.
- With less than 512 MB free, indexing does not start and says why, instead of failing halfway.
- If Laserdot fails to start twice in a row, it offers to start with a fresh index (your files are never touched).
- Save Diagnostics includes any bugs the engine caught and how often it was restarted.

## 0.9.14

- Fixed: when several versions' notes were shown, the update prompt grew taller than the screen and hid its
  Install button. The notes now scroll in a box of their own and the buttons always show.

## 0.9.13

- Fixed: a duplicate scan of many large pictures could slow the Mac to a crawl, stall and end with nothing to show.
  Very large pictures are now looked at one at a time, and a picture or video that takes over a minute or two is
  skipped instead of holding the scan up.
- A duplicate scan saves what it has read every 30 seconds: if it is quit, updated over or stops, the next scan
  goes on from there instead of starting again.
- The Duplicates window shows the file being read, and its clock keeps ticking.

## 0.9.12

- A long duplicate scan can be stopped: "Stop and Show What's Found" lists the duplicates found so far, ready to
  clean up. Then "Continue Scan" goes on where it stopped: files already looked at are not read again.
- Closing the Duplicates window stops a scan too (what it read is kept).

## 0.9.11

- Cleaning up duplicates is obvious now: one big button, "Move 27,670 Duplicates to Trash (37.31 GB)…", then a
  clear confirmation (what is freed, that one copy of each file stays, that nothing is deleted).
- Select All Extras and Select None, and a count of what is chosen.
- Moving thousands of duplicates shows its progress and no longer fails with "Broken pipe". The list updates at
  once afterwards, without scanning your drives again.

## 0.9.10

- Find Duplicates shows what it is doing: each step (choosing files, comparing files of the same size, reading files
  that look the same, looking at pictures and videos), a progress bar, how many files are done, and the time so far.
- Fixed: on big drives Find Duplicates stopped with "cannot reach laserdotd: timed out" after ten minutes. A scan
  now takes as long as it needs.

## 0.9.9

- You can see indexing move: the menu and the window say how many files have been found so far, then how far the
  saving has got, then how many photo, video and music details have been read.
- Updates are checked before they are installed: each release now comes with GitHub's proof that Laserdot's own
  release build made it, and Laserdot installs nothing without that proof. No signing key is stored anywhere.
- A Cancel button while an update downloads.
- Quitting with an update ready no longer waits: the update is prepared in the background as soon as it has
  downloaded, so installing it is instant.
- The thumbnail view is one plain grid, as in Finder (no Top Hit or kind headings).
- A file added, deleted or renamed while Laserdot missed the change now shows up within 5 minutes instead of an
  hour.

## 0.9.8

- Updates are never silent any more: a small window shows each step (checking, downloading with a progress bar
  and how many MB are done, checking the download, preparing it for this Mac, installing, restarting). You can hide
  it while it downloads; the menu then shows the percentage, and clicking that brings the window back.
- Laserdot no longer freezes while it installs an update, including when it installs one as you quit.

## 0.9.7

- Much faster first indexing of big folders: files can be searched as soon as they are listed (a million files in
  about 10 seconds on GitHub's test Mac, instead of almost a minute); picture and video details follow a moment later.
- Uses less memory while indexing.

## 0.9.6

- Columns can be made wider or narrower: drag the line before a column's name. Laserdot remembers the widths.
- A Last Opened column: when you last opened the file from Laserdot (click it to sort).
- Thumbnail size: the slider next to the view buttons, or ⌘+ and ⌘−.
- Laserdot checks every hour that it has not missed a change, and catches up if it has; the menu-bar menu says
  whether it is watching for changes.
- What's New after an update lists everything since your last version, and stays quiet for fixes you would not
  notice.

## 0.9.5

- Columns, like Finder: the view switch at the top (or ⌘J) adds a list with Name, Date Modified, Size, Kind and Date
  Created; click a column to sort by it, click again to reverse.
- Sort by Kind (also in the sort menu).

## 0.9.4

- Files deleted in Finder no longer show up: every result is checked before it is shown, and anything gone is
  removed from the index by itself.
- ⌘R (the arrow next to the grid button, or `> refresh`) looks at every indexed folder again and removes whatever
  was deleted.

## 0.9.3

- Thumbnails: ⌘J (or the grid button next to the sort arrows) shows results as a grid of large previews; ↑↓ move by
  a row. Laserdot remembers your choice.
- Faster first indexing on the Mac: folders are read in bulk (one call for many files instead of one per file), and
  Finder tags are read just after, in the background.
- Uses less memory: after indexing, the search engine serves the index from its file (40 MB instead of about 70 MB
  for 100,000 files); scrolling the grid no longer looks up icons again and again.
- Cache files from `~/Library` that an older version indexed (wallpaper caches and the like) are removed by
  themselves.
- Fixed: Laserdot could stay on "Starting the index…" for good, waiting on a drive that didn't answer or on the
  welcome tour. Now it never waits more than a minute for macOS's questions, and the menu says what it is waiting for.
- Older copies of Laserdot left on your Mac (which macOS may start instead, and which cannot update) are found at
  launch; one click moves them to the Trash.

## 0.9.2

- macOS now keeps your "Allow" answers when Laserdot updates: Laserdot signs each update with a key it makes on
  your Mac (it never leaves it). The first update after this one asks once more; after that, updates don't ask.

## 0.9.1

- macOS's permission questions (Desktop, Documents, Downloads, iCloud Drive, drives) now come all at once, before
  Laserdot reads anything — then it indexes by itself.
- If you said no to a place, Laserdot tells you once which files it can't see, and how to allow them.

## 0.9.0

- Undo remembers more: press ⌥⌘Z again to undo the step before, even after quitting (up to 20 steps, for a week).
- ⌘K ▸ Tags…: add or remove Finder tags (Undo works here too).
- ↑ in the empty search box brings back your earlier searches.
- Settings ▸ Folders says what Laserdot couldn't read (for example, folders macOS didn't allow) and how to fix it.
- Faster indexing, and the window is ready sooner after launch.
- The passage of a big PDF or Word document appears faster the second time, and never holds up the preview.
- Save Diagnostics shows the launch time and how many files couldn't be read.

## 0.8.0

- Undo: after Move to Trash, Rename, Move to… or Compress, a bar offers **Undo** (also ⌥⌘Z and Edit ▸ Undo).
- Settings ▸ Privacy: what Laserdot keeps and how big it is, and **Erase All Laserdot Data**.
- Not happy with an update? Settings ▸ General ▸ **Go Back** to the version you had before.
- The welcome tour explains the macOS permission prompts before Laserdot starts reading your files.
- Documents found by their words show the passage that matched in the preview.
- ⌘K ▸ **Search in This Folder**, or drop a folder on the window.
- Encrypted drives: tick **Remember in my Keychain** and the drive unlocks by itself next time.
- Faster indexing (writes about twice as fast).

## 0.7.0

- Sort results by name, size or date (the arrows next to the pin).
- Library ▸ Storage: what fills your disk, folder by folder, and the largest files.
- Full media details for every format: ffprobe is now built in (without any network support).
- Resize the ⌥Space window from its edges; Laserdot remembers the size.
- Background work pauses on battery, when the Mac is hot, or in Low Power Mode.
- Content Search shows how much space it uses, and can skip source code.
- laserdot:// links for Shortcuts: laserdot://search?q=photos%20last%20week, laserdot://storage.
- ⌘K adds Open in Terminal, Copy as Link, Compress and Move to….
- ⌥1–⌥9 press the suggestion chips; ⌘→ shows all of a section (after ↓).
- A new menu-bar icon.

## 0.6.1

- Fixed: Laserdot quit when a video or song was selected (the preview's player).

## 0.6.0

- Settings ▸ Folders: see every indexed folder, add or remove one, and pick folders that are never indexed.
- Clear History: forget what you searched for and opened (Settings ▸ General).
- Laserdot opens at login by default, so the index stays current after a restart.
- This list: what's new, shown when Laserdot updates.
- Space twice previews the highlighted result without pressing ↓ first.
- Scroll to the end of the results to load more.
- Short typos are forgiven (pfd finds pdf), and you are told when only similar names were found.
- Words inside files are searched in your whole home folder.
- The Duplicates window shows the copies side by side, with thumbnails.
- Settings ▸ Drives: every drive, where its index is, and its actions.
- A short welcome tour on first launch (Help ▸ Welcome Tour).
- Help ▸ Save Diagnostics: a file to send with a bug report, without your file names.

## 0.5.2

- The index of a read-only drive, kept on this Mac, is deleted as soon as the drive is gone.

## 0.5.1

- Space opens Quick Look for the selected result (after ↑/↓ or a click).
- Settings opens in front; Settings shows where the index is kept.

## 0.5.0

- Cleaner results: short paths, friendly dates, bold matches, five per section with Show all.
- Plain words: today, last week, big, 4k, screenshots — and Arabic.
- ⌘K actions, > commands, several results at once, a key bar, playable previews.
