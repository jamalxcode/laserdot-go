# Lessons learned

Things building Laserdot taught us, mostly the hard way. Each one is a bug that reached the owner, or nearly did,
and the rule it left behind. (The conversations behind them: [`design-conversation.md`](design-conversation.md).)

## Long work

1. **A time limit is a bug waiting for a bigger disk.** Duplicate scans gave up after 10 minutes ("cannot reach
   laserdotd: timed out") on a 16 TB drive, and so could indexing a drive or Refresh. Long jobs now have no time
   limit and their own connection; they end when the engine ends, not when a clock says so.
2. **Never resend a long request.** The shared connection retried any request that failed, so a drive index that
   timed out after 10 minutes quietly started again from scratch. Long jobs are sent once.
3. **Save as you go.** A picture scan stalled after 1 h 39 min and lost everything, because results were saved only
   at the end. Now every 30 seconds; a stop, a crash or an update costs at most that.
4. **One file must not hold up a hundred thousand.** Eight 200-megapixel pictures decoded at once can take
   gigabytes and stall the Mac. Big pictures now go one at a time, and each file has a time limit.
5. **A request can be bigger than you think.** Cleaning up 27,670 duplicates sent a request over the engine's 1 MB
   limit and got "Broken pipe". The limit is 64 MB now, and cleanups go in batches of 500 groups.

## Silence is a bug report waiting to happen

6. "Is it indexing or stuck?", "the computer pauses with no indication", "I'm not sure if something is happening":
   three requests, one lesson. Anything slow shows its step, a count and the time; see
   [`../now/interface-rules.md`](../now/interface-rules.md) §2.
7. **A clock that waits for the engine looks frozen when the engine is busy.** The elapsed time now ticks on its own.

5b. **Hold a lock for the change, not for the looking.** Re-checking an 8 TB drive held the index lock while it
    walked the whole drive, so every search waited and Quit hung until Force Quit. Now the disk is read with no
    lock, and the lock is held only for the moment the in-memory index changes.
5c. **One slow request must not queue the rest behind it.** Searches shared a line with slow writes; now they have
    their own, and quitting never waits on a stuck request.

5d. **Make the index its own checkpoint.** A drive's first index used to be resumable only by walking the whole
    drive again and diffing. Now each folder and its "done" mark are saved together, so what is left after an unplug
    or a crash is exactly the folders not marked: nothing lost, nothing twice, and no separate state to get out of
    step.
5e. **Stopping must not wait for the slowest reader.** A stuck folder can take 30 seconds to give up; ejecting
    stops the index between batches and leaves such reads behind instead of waiting for them.

5f. **Background work must act like it.** A fast indexer at normal priority made the whole Mac beachball and an
    update crawl. Indexing now asks macOS for throttled disk access and a lower priority, and pauses for an install:
    the user's own work always comes first.
5g. **Every outside tool needs a time limit and a clock.** The update waited on `codesign`, `hdiutil` and `ditto` with
    no limit and no elapsed time, so "slow" and "stuck" looked the same.

5h. **Hold a file only while you use it.** A drive's index stayed open for as long as the drive was attached, so
    macOS refused to eject it. Open for the job, close after it, and let go when the system asks.

5i. **Test the case the user hit, not a neighbour of it.** The self-test chose `ext:pdf`, which has no typing
    suggestions; the owner chose `res:1440p`, which does, and the suggestions hid the choices. The test now checks
    the row is still shown after a click, whatever the filter.

5j. **Measure before you migrate.** Streaming this Mac's own first index looked like the fix for the million-file
    slowdown; measured, it was slower and heavier on a fast disk. The real cause was the engine's memory ceiling, a
    one-line change. The streaming work still paid off: testing it found two real bugs in the drive indexer.
5k. **Tests can disturb each other.** A new test that ejects a disk ran just before the self-test, and macOS finishing
    the eject stalled the self-test's start. Order tests so one's aftermath can't reach the next.

## Crashes and recovery

8. **Kill it on purpose.** A test that kills a real Laserdot process at random moments found, on its first runs, a
   bug no ordinary test had: a folder whose first index was interrupted was remembered as indexed but stayed empty
   forever. Unfinished folders are now re-indexed by themselves.
9. **Catching a panic can turn a crash into a hang.** If the bug left a lock held, everything after it waits forever.
   After catching a bug, the engine checks it can still take its lock, and restarts itself if not.
10. **Test the case only your fix can fix.** The watchdog test killed the engine, but the next search restarted it
    first, so the watchdog never acted. Freezing the engine (it neither answers nor exits) tests the watchdog.
11. **"Empty" and "didn't answer" are different answers.** A folder on a stuck drive must not be read as empty, or its
    files would leave the index. A listing that times out is an error.

## Measuring

12. **Measure what the user sees.** Resident memory counted index pages macOS can drop for free ("1 GB!"); the
    physical footprint, Activity Monitor's Memory column, said 65 MB after a million files.
13. **Profile before optimizing.** Search was already fast; time went where the profile pointed (opening media files
    for headers: 46 of 54 seconds on a million files).
14. **Time each step.** Launch looked slow; the marks showed Laserdot's own code took 3 ms and macOS's window setup
    the rest.

## The screen

15. **A dialog that grows with its text will one day leave the screen.** Release notes for several versions pushed
    the Install button below a 16-inch MacBook's screen. Long text scrolls in a fixed box now, and the self-test
    builds the prompt with 300 lines to check it fits.
16. **Obvious beats complete.** Duplicate cleanup worked, but its button said "Review N Files…" in a corner, and the
    owner couldn't find how to delete. One big button that names the action won.
17b. **Look at the screenshots before reviewing the UI.** The self-test's own screenshots showed what a description
     would have missed: a chip labelled with a whole system path, buttons clipped at the bottom of the preview, and
     query syntax (`-ext:pdf`) shown where people expected words.
17c. **Keep one source of truth when making things friendlier.** Pills in the search box are only a view of the
     query string, so history, pins, links and the command line kept working unchanged; choices go in front of the
     typed words so the end of the query is always what is being typed (the hint had been explaining `-ext:pdf`).
17f. **Never rebuild the view that holds the keyboard.** The Spotlight-style start first drew the search field in
     one view and the full window's field in another; typing the first letter switched views, and the new field
     never saw that letter ("pg" for "jpg"). The self-test caught it before release. The field is now one view in
     one place, and only what is around it changes.
17g. **Nothing slow on the main thread, not even "just opening a PDF".** PDFKit read the whole file on the main
     thread; a large or iCloud PDF froze the window. And when a freeze is reported, know whose it was: the app now
     writes down every time its window stops answering.
17h. **Things that appear on hover must not move anything.** The "−" grew the chip, the row reflowed, and the
     button slid away from the pointer that summoned it.
17i. **A thing on its way out looks like a thing that is new.** A drive being unmounted could not be read for a
     moment and so looked unindexed; offering only what has just appeared, and asking again a moment later, fixed it.
17d. **A menu-bar app's dialogs need to be told to stay.** Dialog panels hide when their app is not in front and
     open on one desktop; for an app that is never in front, that meant an update prompt lost behind the browser.
17e. **Read only what people would look for.** Reading every file's words found an Obsidian plugin's bundled code
     before the user's PDF; `.DS_Store` files filled results. Hidden folders are no longer read for words and system
     bookkeeping files are not indexed at all.
17. **Copy the habits people already have.** Spotlight's typing (inline completion, Esc twice, last search selected)
    made the window feel familiar overnight.

## Process

18. **Test a commit once.** CI ran every commit twice (branch, then `main`) while Mac machines were scarce; reusing the
    branch's result cut the wait for a release by about 25 minutes.
18b. **CI capacity is a failure mode too.** A release job can wait for a Mac runner and be cancelled without running
     a step; it is not a code failure. The `retry` workflow now starts such jobs again by itself (at most twice), and
     leaves jobs that ran and failed alone.
18e. **Time it before trimming it.** Guessing would have trimmed the steps; the timings showed 105 s per language
     spent waiting for a preview that never reported closed, and 2.5 minutes of the Arabic run waiting for a reader
     that only runs in English (an empty variable read as "set"). Two one-line fixes halved the job.
18d. **Don't spend a Mac on a comma.** Commits that change only documents skip the Mac jobs; a newer push to a branch
     cancels the run it replaces; every job has a time limit.
18c. **Code only CI can compile needs extra care.** The Swift app compiles only on CI's Macs; a test that referred to
     another test class's helper cost a round trip. Keep new tests self-contained.
19. **Simulated key presses can get lost on CI machines.** A flaky check is still worth keeping, but it should retry
    the key before failing, and a failure on a documents-only commit is a hint.
20. **Wait for the tests before saying "done".** A commit pushed while a test failed (its output piped through
    `tail`, which hid the exit code) had to be fixed by the next one.
20b. **A one-connection pool deadlocks on a held connection.** The schema migration took the pool's only connection
     for a write lock and then asked the pool for another; two processes opening a new index at once also raced on
     SQLite's first locks. Release the connection before using the pool, and retry the transient errors.
20c. **Test fixtures must be big enough to mean something.** A 64×48 test picture lost its fingerprint in HEIC
     compression (18 bits apart); an 800×600 one kept it. And a file written just before indexing is "still being
     written" and is not compared, so tests age their files.
20d. **Never touch a disk while holding the index lock.** The drive list stat'ed every /Volumes entry under the read
     lock; one hung network share would have held it, a waiting writer then blocks every new reader, and searches
     stop. Copy what you need under the lock, do the I/O without it, and take the lock again to build the answer.
20e. **A watchdog must know the difference between starting and stuck.** The app waited 10 s for the daemon, then the
     next request killed it and started another: a big index that needed longer to load would never come up. Give a
     starting process its time, and open the socket before slow, optional work.
20f. **`URLSessionTask.progress` is not bytes until the size is known.** Before the response headers it counts a
     placeholder 100 units, which the update window showed as "5 bytes of 100 bytes" and looked stuck. Use
     `countOfBytesReceived`/`countOfBytesExpectedToReceive`, and retry a download that breaks off.
20g. **Every way in must keep the signature.** Updates were re-signed with this Mac's key, but a version installed by
     hand from the disk image was ad hoc again, so macOS asked for Full Disk Access again on a new Mac. And a
     signing failure was swallowed with no trace. Re-sign at launch too, and keep the last failure for diagnostics.
20h. **Finder doesn't go into packages.** `activateFileViewerSelecting` on a file inside a .ulysses sheet opened an
     outer folder. `selectFile(_:inFileViewerRootedAtPath:)` with the file's own folder reaches it.
20i. **Write each fact once.** Every change was being copied into six documents, which was slow and let them drift.
     The map in `docs/README.md` gives each kind of fact one home; most changes need two or three.
20j. **Package lists grow with the owner's apps.** Apps were packages from the start, but document packages (a
     Ulysses sheet) were not, and their insides flooded searches. When a result looks like noise, ask whether macOS
     treats its folder as one document.
20k. **Moving Swift code to another file can break `private(set)`.** A setter that is private is private to its
     file; an extension in a new file cannot use it. When splitting a type, list every property the moved code
     assigns (including `&+=` and tuple assignments) and loosen those, or keep the code where it was.
20l. **A macOS `Menu` flattens its label.** A custom VStack label became a line of text. For a button that looks
     like its neighbours, use a `Button` that opens a popover.
20m. **New test files can collide with old test steps.** A "Beach" folder took `beach.jpg`'s place in a step that
     searched "beach". Name new fixtures with words no step searches for.
20n. **Rank by time as people remember it.** Recency in coarse steps (today, this week…) changes the order people
     expect without reshuffling results by seconds; the match itself still decides first.
20o. **A SwiftUI shape takes all the height it is offered.** A band's stripe as a Capsule in an HStack made
     Storage's band fill the window; draw it as a background of the content instead, so it is as tall as the content.
20p. **Sweep for dead code with tools, not memory.** `deadcode` and `staticcheck -checks U1000` (run for darwin too,
     so build-tagged code counts) found Go functions nothing called; Swift has no such tool on Linux, so a script
     counts each declared name's uses, and another lists interface strings no code asks for (0.9.47 removed the
     app's old sandbox helper, an uncalled launch prompt and 22 unused strings).
20q. **macOS previews only some videos.** Quick Look and AVFoundation read MP4/MOV but not MKV, WebM or AVI; the
     ffmpeg already bundled for media details reads them all. And a feature tried on the internal disk can fail on
     an external drive: give slow reads time, and fetch ahead.
20r. **Reading a new kind of file changes who waits.** 0.9.49 started reading PDFs' details, so on a first index
     PDFs joined the "waiting to be read" files — and the duplicate finder, which skips waiting files, stopped
     finding a fresh PDF copy (caught by the daemon and Swift tests, not the Go core tests, which age their files).
     A file waits only where the wait matters: media for its header; documents are read at once and compared by
     content whatever their state. Test with files written a moment ago, not only aged ones.
21. **The user's screenshots are the best bug reports.** Most of the fixes above started as one.
