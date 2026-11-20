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
   [`interface-rules.md`](interface-rules.md) §2.
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
21. **The user's screenshots are the best bug reports.** Most of the fixes above started as one.
