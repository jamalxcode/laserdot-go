# Interface rules

How Laserdot looks and behaves. Each rule came from using the app (see
[`../history/design-conversation.md`](../history/design-conversation.md)); the self-test checks most of them on the real screen, in
English and Arabic, on every commit.

## 1. Behave like Spotlight, then add

People already know Spotlight. Laserdot matches it wherever it can, and adds its extras where Spotlight has nothing.

| Spotlight does | Laserdot does |
|---|---|
| ⌥Space-style shortcut opens a floating box | ⌥Space (changeable), a slim bar that grows when there are results |
| completes the top hit's name as you type | the same: the rest shown selected; → or Tab takes it, Delete drops it |
| Return opens the top hit, ⌘Return shows it in Finder | the same, and it reaches files inside packages (a Ulysses sheet, an app), where Finder alone stops outside |
| Esc clears, Esc again closes | the same |
| reopens with the last search selected | the same |
| ⌘↑ / ⌘↓ jump between categories | the same |
| ⌘C copies the selected file | the same |
| remembers where you dragged it | the same |
| Top Hit, then categories | the same, five per category with *Show all* |
| a rounded bar with round buttons beside it | the same start: Recently Opened, Videos, Images, Music, Documents, Apps, then Duplicates, Storage, Drives, Index a Folder, Settings; the rest appears on typing, ↓ or a button |
| — | filters (`kind:video res:4k`), a hint line explaining clever words, Space for Quick Look, ⌘K actions, `>` commands, recent files on ↓ |

The inline completion stays out of the way of Laserdot's own language: it is not offered for queries with filters,
for `>` commands, after a trailing space, or while moving through the results.

## 2. Never silent

1. **Anything that can take more than a couple of seconds shows what it is doing**: its step, a bar (moving when
   the total is unknown, filling when it is known), a count ("131,312 of 136,896 files") and the time so far.
2. Updates, indexing (folders and drives), duplicate scans and cleanups all follow this; the update window shows
   each step's elapsed time.
3. Background work never makes the Mac slow: the engine runs at low priority with throttled disk access, and pauses
   while an update installs.
4. A clock that depends on the engine answering is a bug: elapsed time ticks by itself.
5. Show the item being worked on (the file a scan is reading) so a stall can be diagnosed from a screenshot.

## 3. Long work can be left

1. Long jobs never freeze the window: they run off the main thread, on their own connection to the engine, with
   no time limit.
2. A long job can be **stopped**, keeping what it found, and **continued** later without starting over.
3. Closing a window stops its job the same way.
4. **Quit always works**, within seconds, whatever the engine is doing.
5. A question never waits on a long count: the drive prompt counts for 5 seconds, then says "more than N files".

## 4. One primary action per screen

1. Each screen has one obvious main button (the Duplicates window: *Move N Duplicates to Trash (size)…*, red,
   large, with the count and size in its title).
2. Secondary choices are smaller (Select All Extras / Select None, the destination).
3. After a job ends, offer **one or two** sensible next steps (open the Trash, continue the scan, see what takes
   space) — never a chain of prompts.
4. Empty results and errors always say what to try next (suggested searches, *see what takes space*).

## 5. Confirmations

1. Anything that moves files is confirmed, in plain words: how many, how much space, where to, and that nothing is
   deleted.
2. The safe choice (Cancel) answers Escape.
3. **A dialog always fits the screen** with its buttons visible: long text (such as release notes) scrolls in a box
   of fixed height. (A 16-inch MacBook once lost the Install button.)
4. **A dialog stays in front.** It does not hide when another app is used, and shows on every desktop and over
   full-screen apps. (The update prompt once kept vanishing behind the owner's browser.)

## 5b. Choose, don't type

1. After a search, what the results have to choose from is offered under the hint line, by attribute, with counts
   ("Folder Trips 5 · Kind Videos 12 · Size … · Resolution 4K 3 · 1080p 12 · Sound With sound 14").
   **Top down, large to granular** (the owner): where (drive, folder), what (kind, type), size and date, picture
   (resolution, shape, HDR, codec), length and sound, photo (camera, place, kind of picture), music. Each of those
   families is a band of its own with its own soft shade, so related choices read as one.
   **Calm by default** (0.9.42): choices that would leave nothing are hidden, a group shows its four biggest values,
   and four bands show; **More choices (N)** shows everything (greyed ones included), **Fewer choices** goes back,
   and the choice is remembered. Band colours are the colour-blind-safe Okabe–Ito set, each band with its own icon:
   colour is never the only cue.
2. Only choices that narrow the results are shown.
2a. **While searching, the kinds stay and the tools fold into More** (0.9.44); the empty box shows every button.
    The hint explains what is being typed, never what the choices already show.
2b. **Every choice can be reached:** a long group (cameras, folders) wraps onto the next lines; nothing runs off
    the edge of the window.
3. **What is chosen is a pill in the search box** ("4K ✕", "not PDF ✕", "in 2023 ✕"), before the typed words: ✕ or
   Delete in an empty box removes it. Its group leaves the row until the pill is removed; every other group stays,
   each count saying what that choice gives with the rest; a choice that would leave nothing is greyed out.
4. **Leaving out is as easy as choosing:** the "−" shown on hover, ⌥-click, or right-click ▸ Leave Out.
5. Where to search is a menu on the magnifying glass (Everywhere, This Mac, recent folders, a folder), and its name
   shows beside it while it narrows the search.
6. Common searches are one click on the empty box (Recent Downloads, Photos This Month, Videos…), and a scope can
   be picked rather than typed (Duplicates' Look at and folder pickers; the query is under Advanced).

## 6. Words

1. Plain language. Say what happens and what to do, not internal terms. The query language never has to be read:
   filters show as pills with words, tips are sentences ("See your 4K videos"), quality is "high quality", not
   "0.11 bpp", and folders read "Home › Trips › 2023". It can still be typed.
2. Every string exists in **English and Arabic**; Arabic windows are laid out right to left.
3. Numbers are formatted for the language ("27,670"), sizes with the system's formatter ("37.31 GB").
4. Errors name the problem and the fix ("only 400 MB free on the disk… free up some space, then try again").

## 7. Keyboard first

1. Everything in the search window works without the mouse: ↑↓, Return, ⌘1–⌘9, Space, ⌘K, Tab, ⌘↑/⌘↓, ⌘C, Esc,
   ⌥⌘Z, ⌘J, ⌘R, ⌘D, ⌘/.
2. Typing never waits for the engine: completions and searches are asked for in the background, and a stale answer
   never replaces a newer one.

## 8. Respect the user's time

1. No onboarding walls: apps and the home folder are indexed without asking; macOS's permission prompts come
   together, once.
2. Reminders are optional and rare (see [`business-rules.md`](business-rules.md) §6).
3. Results stop at *Show More*; nothing scrolls forever.
4. The window remembers the user's choices: size, position, view, sort, tile size, where to search, Duplicates'
   scope and options.
5b. **Opening a result closes the search window**, as Spotlight does; its right-click menu offers what Finder's
    would (Rename, Move to Trash) besides Laserdot's own (More Like This).
5. Short lists first: ⌘K shows the common actions, with Open With… and More Actions… opening in place.
6. No clutter: system files (`.DS_Store`, `._` copies, `Thumbs.db`…) are never results, and hidden app folders are
   never read for words.

## 9. Proving it

Every visible change extends the self-test (`app/Sources/Laserdot/SelfTest.swift`, judged by
`app/scripts/check-selftest.py`), which drives the real bundled app with key events in CI and prints screenshots
into the job log.
