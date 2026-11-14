# Changelog

All notable changes to Tosk are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). Before 1.0, the configuration format may change in a minor version.

## [Unreleased]

### Added

- An Agent Skill that teaches coding agents to use Tosk: read the worktree's own port and names with `tosk var` before starting a server, write and debug `.tosk.yml`. `tosk skill` installs it in the repository (`.claude/skills/tosk/`, to commit) or with `--global` for all your repositories; it always matches the installed version of Tosk.
- A Claude Code plugin serving the same skill: `/plugin marketplace add leukalm/tosk`, then `/plugin install tosk@tosk`.

## [0.1.1] - 2026-11-14

### Fixed

- Release notes on the GitHub release page are taken from this changelog (they were missing for 0.1.0 and were added by hand).
- `tosk --version` shows `0.1.0`, not `v0.1.0`, when installed with `go install`.

### Added

- `scripts/release.sh` (`make release BUMP=patch|minor|major|X.Y.Z`) cuts a release: version bump, changelog, checks, commit, tag, push.

## [0.1.0] - 2026-11-14

First beta.

### Added

- `.tosk.yml` with variables, three phases (`create`, `sync`, `remove`) and profiles.
- Steps: `copy`, `link`, `template`, `replace`, `write`, `run`, and `skip` in profiles. Every step accepts a `when:` condition (`exists()`, `matches`, `==`, `!=`, `!`, `&&`, `||`).
- Built-in variables `name`, `slug`, `branch`, `path`, `main`, `index`, `profile`, and providers `port`, `counter` and scripts.
- Commands `init`, `sync`, `add`, `rm`, `ls`, `var`, `run` and `doctor`.
- `post-checkout` hook: plain `git worktree add`, `claude --worktree` and any tool using the git CLI prepare the new worktree. Registered through Husky or Lefthook when the repository uses them.
- `tosk-post-create` and `tosk-pre-remove` events for git hooks.
- State stored in each worktree's own git config, and an OS lock so that worktrees created at the same time get distinct allocations.

### Security

- File steps cannot read or write outside the worktree, even through a symlink committed on the branch.
- Variables reach `run` commands through the environment, so a branch name cannot inject shell code. In `replace`, values match and are inserted literally.

### Known limitations

- Tested on macOS; Linux and Windows pass the test suite in CI but have not been used for real.
- `tosk watch` is not implemented.
- Homebrew and Scoop packages are not published: install with `go install` or from the release binaries.

[Unreleased]: https://github.com/leukalm/tosk/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/leukalm/tosk/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/leukalm/tosk/releases/tag/v0.1.0
