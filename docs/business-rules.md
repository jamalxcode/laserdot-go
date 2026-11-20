# Business rules

What Laserdot promises, and what it will never do. These are product rules, not preferences: a change that breaks
one needs the owner's say-so, and is recorded in [`design-conversation.md`](design-conversation.md) and the spec.
The engineering pillars behind them are in [`../CLAUDE.md`](../CLAUDE.md).

## 1. Privacy

1. **Nothing leaves the Mac.** `laserdotd`, which reads every file, asks macOS to deny it the network before it
   does anything else. The app has no network code at all (CI fails the build if any appears).
2. **One exception, granted by the owner:** the **Laserdot Updater** helper may reach github.com, and only to find
   and download a release, once a day or when asked. It sends nothing about the user or their files.
3. **No AI, no telemetry, no analytics, no crash reporter, no accounts.** Bugs are logged to a file on the Mac
   (`laserdotd-crash.log`); the user decides whether to share it, through Save Diagnostics (every path removed).
4. **The user can see and erase everything:** Settings ▸ Privacy lists what is kept and its size; Erase All Laserdot
   Data removes it all, remembered passphrases and the signing key included.
5. **Drives are confidential.** A drive keeps its own index; an index kept on this Mac for a read-only drive is
   deleted as soon as the drive is gone. A drive's index can be encrypted with a passphrase that is never written
   down (unless the user ticks *Remember in my Keychain*). Any drive's index is treated as untrusted input.
6. **A drive's index lives only on that drive**, its progress too: a first index that is cut short keeps its
   checkpoints inside the drive's own index, never on this Mac (0.9.19). What searches are about is used to order the
   work and kept in memory for 10 minutes only.

## 1b. The Mac comes first

1. **Laserdot's work never makes the Mac slow.** The engine runs as a background process (lower priority,
   throttled disk access, like Spotlight's indexer) and opens only a few files at a time (0.9.20).
2. **Indexing pauses for an install** and for battery or heat, and carries on afterwards.
3. **Searching never waits for indexing**: a drive is searchable while it is indexed, and quitting, ejecting or
   unplugging stops indexing at once, keeping what was read.
4. **Laserdot never stands in the way of ejecting a drive** (0.9.21): between jobs it holds nothing open on a drive,
   and when macOS is about to eject one it lets go first, then approves.

## 2. Files are never put at risk

1. **Laserdot never deletes a file.** Cleanups move files to the Trash or a folder the user picks.
2. **A dry run comes first**, then a confirmation that says what will move, how much space it frees, and that
   nothing is deleted.
3. **One copy of every file always stays.** The last kept copy of a group cannot be marked; a group whose kept copy
   changed since the scan is skipped.
4. **File actions can be undone** (⌥⌘Z, 20 steps, for a week, even after quitting).
5. **Safe start never touches files:** a fresh index moves the old index aside; the user's files are not read
   differently, moved or changed.

## 3. Duplicates

1. Identical copies, similar pictures and re-encoded videos are found; **the best copy is recommended** (quality,
   then the tie-breakers in the spec).
2. A scan **can be stopped at any point** and shows what it found; the next scan **continues** rather than starting
   over. What has been read is saved every 30 seconds.
3. One bad or enormous file never holds a scan up: a picture gets a minute, a video two, then it is skipped (and
   not retried until it changes).
4. Fingerprints are kept only in this Mac's index, never in a drive's.

## 4. Updates

1. **Every update carries proof** that Laserdot's own release workflow on GitHub built it (a keyless Sigstore
   attestation). Without valid proof, nothing is installed. No signing key exists anywhere to be stolen.
2. Each Mac then signs the update with **its own key**, so macOS keeps the user's "Allow" answers.
3. Updates install on quit (instantly: prepared in the background) or from the prompt; the previous version is kept
   for **Go Back**.
4. An update is never silent: checking, downloading and installing show their progress and each step's elapsed
   time, and a download can be cancelled.
5. **No step can hang:** every tool an install runs has a time limit; Save Diagnostics shows how long each step took.

## 5. Releases

1. A release is made by adding a version to `CHANGELOG.md` and merging to `main`; GitHub builds, checks and
   publishes it.
2. **A commit is tested once** (the owner's hard rule): on its branch; `main` is fast-forwarded to that exact
   commit and reuses the passing run. Nothing reaches `main` before its branch run is green.
3. Release notes are plain words; a version marked `<!-- quiet -->` gets no What's New message.
4. Only the owner can push or publish.
5. **The build is lean and heals itself:** documents-only commits use no Mac, every job has a time limit, and a job
   GitHub could not give a machine is started again by itself; a job that ran and failed is never retried silently.

## 6. Engagement: useful return, not time spent

1. Laserdot's recurring jobs are finding files (on demand) and keeping the disk tidy. **Only tidying earns a
   reminder**, and reminders are **off until the user turns them on**.
2. A reminder is never more frequent than chosen, and **using the feature resets its clock**: someone who scanned
   last week is not reminded.
3. Notifications are made on the Mac; permission is asked only when a reminder is turned on.
4. **Outcomes, not badges:** the space a cleanup moved and a lifetime total are shown; no points, streaks, levels,
   or counters that reset.
5. **Clear stopping points:** results end at *Show More*; nothing autoplays or scrolls forever.
6. Suggestions come from the user's own files and lead to a real search.

## 7. Scope

Files, media and disks. Personal data (contacts, calendar, mail, messages, notes, photos library, browser history),
System Settings search, a calculator, a dictionary, clipboard history and snippets are out of scope.
