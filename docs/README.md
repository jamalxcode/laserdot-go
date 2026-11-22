# Laserdot's documents

Everything written about Laserdot, in three folders, plus the changelog and the issue tracker. Nothing was thrown
away when they were arranged this way (0.9.44): every document, lesson and decision is here, only moved.

| Folder | What is in it | How it changes |
|---|---|---|
| [`now/`](now/) | **What is true today.** | Edited in place: when something changes, the old text is replaced. |
| [`history/`](history/) | **How it got here.** | Only added to, never rewritten. |
| [`reference/`](reference/) | **The spec**, the source of truth for behavior. | Amended where a behavior changes (marked with the version). |

## now/ — what is true today

- [`how-it-works.md`](now/how-it-works.md) — one page: how Laserdot works now. Start here.
- [`business-rules.md`](now/business-rules.md) — what Laserdot promises and will never do (privacy, files,
  duplicates, updates, releases, reminders, scope).
- [`interface-rules.md`](now/interface-rules.md) — how it looks and behaves, and how it copies Spotlight.
- [`design-ideas.md`](now/design-ideas.md) — the ideas behind it, and the ones still waiting their turn.

## history/ — how it got here

- [`design-conversation.md`](history/design-conversation.md) — what the owner asked for, release by release, what
  was decided and why, and how the work was done.
- [`lessons-learned.md`](history/lessons-learned.md) — what building it taught us, mostly the hard way.

## reference/

- [`spotlight-successor-spec.md`](reference/spotlight-successor-spec.md) — the full feature spec (§ numbers in code
  and issues refer to it).

## Elsewhere

- [`../CHANGELOG.md`](../CHANGELOG.md) — what's new in each version, in plain words (bundled into the app as What's New).
- [`../issues/001-spotlight-successor.md`](../issues/001-spotlight-successor.md) — the epic and the Design Decision Log.
- [`../issues/002-go-port.md`](../issues/002-go-port.md) — the Go port: Rust→Go mapping and the as-built notes
  (file and function names) for each version.
- [`../README.md`](../README.md) — the front page; [`../CLAUDE.md`](../CLAUDE.md) — how to work in this repository.

## Where a change is written (each fact once)

A change used to be written into six places. Now each kind of fact has one home:

| The change… | Goes in |
|---|---|
| is something a user will notice | `CHANGELOG.md` (plain words) |
| came from the owner (a request, a screenshot, a choice) | `history/design-conversation.md` (asked, decided, why) |
| changes how Laserdot works | `now/how-it-works.md` (edit the paragraph in place) |
| changes a promise, or how the screen behaves | `now/business-rules.md` or `now/interface-rules.md` |
| taught something the hard way | `history/lessons-learned.md` |
| needs the code's names (files, functions, settings) | `issues/002-go-port.md`, as-built notes |
| changes a behavior the spec describes | `reference/spotlight-successor-spec.md`, marked with the version |
| is visible on the front page | `README.md` |

Most changes need two or three of these, not all of them.
