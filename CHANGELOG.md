# Changelog

What's new in each version, in plain words. `app/scripts/bundle.sh` copies the section for the version it builds
into the app (`WhatsNew.txt`), which Laserdot shows when it updates.

## 0.9.9

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
