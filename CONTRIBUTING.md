# Contributing to Zubi

Check existing [issues](https://github.com/fa-krug/zubi/issues) and
[pull requests](https://github.com/fa-krug/zubi/pulls) first.

> **Don't hurry your code. Make sure it works well and is well designed. Don't worry about timing.**

## Non-negotiables

- **Nothing leaks by default.** Zubi hands personal data to other machines. A change must never expose
  a list, calendar or folder the user didn't tick, answer an `/api` route without the key, or widen
  the default bind beyond localhost.
- **No bloat.** Zubi stays small on purpose. A clean patch still gets declined if the feature isn't
  worth its weight, so open an issue and settle that before you build.
- **Never break the Non-negotiables** in [`AGENTS.md`](AGENTS.md).

## Setup

- Xcode 26; the app targets macOS 14+. `brew install xcodegen`. Do the one-time signing setup
  ([`docs/signing.md`](docs/signing.md) §1).
- `open Zubi.xcodeproj` → ⌘R. Debug builds are their own channel (`Zubi Dev.app`).
- After editing `project.yml`: `xcodegen generate`, commit the result.
- Details: [`docs/development.md`](docs/development.md). Start at [`docs/`](docs/README.md).

## Before submitting

- The Debug build compiles with no new warnings. The repository's one workflow releases each push to
  `main` and runs nothing on a PR, so **build locally**.
- You actually used the app, on your path and the ones next to it. For an API change, `curl` the
  endpoint with and without the key, and against a hidden item.
- Rebased on `main`, squashed into logical commits.
- Anything in [`docs/`](docs/) or the README your change makes wrong is fixed.
- **Read your own diff top to bottom before you open the PR.** Most review comments are things the
  author would have caught on a second pass.

## Pull requests

- Fill in the [PR template](.github/PULL_REQUEST_TEMPLATE.md): every section, or say why it doesn't
  apply.
- Visual change → before/after screenshots or a short recording.
- Non-visual → say what you tested, with the requests you ran.
- Flag anything surprising, and any tradeoff you made on purpose.

## Code style

Read [`AGENTS.md`](AGENTS.md) first. The layout and the Non-negotiables are there, and they apply to a
human contributor exactly as they do to an agent. Look before "fixing" something that looks wrong: it
may be deliberate. `.swift-format` at the repo root holds the formatter settings.

Two things that are only about contributing:

- Commit messages imperative: `Add due=tomorrow filter`.
- Behaviour you didn't mean to change, didn't change. Say so explicitly in the PR if it did.

## Bugs

macOS version, Zubi version, which source (Reminders, Calendar, Notes, the API itself or updates),
steps, expected vs actual. For an API bug, the exact request and the response.

## Security

Not in the issue tracker; see [`SECURITY.md`](SECURITY.md).

## License

[MIT](LICENSE). Contributions are licensed under the same terms.
