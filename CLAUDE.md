# Tosk

Go CLI that prepares git worktrees (see README.md, which is the product spec).

## Commands

- `make build` builds `bin/tosk`; `make test` runs the tests; `make lint` runs vet + gofmt check.
- `go run . <command>` to try the CLI from source.
- Go is installed with Homebrew (`/opt/homebrew/bin/go`).

## Layout

- `main.go`: entry point (kept at the root so `go install github.com/leukalm/tosk@latest` works).
- `internal/cli`: cobra commands, one file per command. Thin: logic lives in `engine`.
- `internal/engine`: variable resolution, providers, steps. API: `engine.New(cfg, worktrees, Options)`, then `SyncAll` / `SyncOne` / `Remove` / `RunStep` / `Inspect` (read-only) / `Check` (doctor findings). A `Target` is one resolved worktree.
- `internal/state`: per-worktree record in `config.worktree` (`tosk.*` keys).
- `internal/config`: `.tosk.yml` schema and parsing.
- `internal/expr`: the `when:` expression language.
- `internal/git`: thin wrapper around the `git` binary.
- `internal/vars`: built-in variables, slug, `{{…}}` interpolation.
- `internal/hook`: `post-checkout` hook installation and chaining.
- `testdata/`: sample configs shared by tests.

## Conventions

- Tosk shells out to `git`; no libgit. Always go through `git.Run` / `git.CleanEnv`: inside a hook git sets `GIT_DIR`, which breaks `git -C <other worktree>`.
- State belongs in each worktree's `config.worktree`, never in a sidecar file.
- `git worktree add` copies the `config.worktree` of the worktree it runs from. `tosk.owner` marks whose record it is; a record owned by another worktree is ignored and wiped (`state.Load`, `State.own`). Do not remove that check.
- Every step must be idempotent and print only what it changed: `sync` may run at any time and is silent when there is nothing to do.
- Security model: a worktree's content and its branch name are untrusted, and Tosk runs unattended from a hook. File steps go through `os.Root` (`engine.roots`), never plain `os` calls on a joined path. Shell lines use `vars.ShellExpand` (values travel in the environment), never `Expand`. Regex and replacement text use `ExpandQuoted`. `internal/engine/security_test.go` guards all three.
- `state.Load` returns an error rather than an empty state when it cannot read: an empty state means "create this worktree again".
- Allocations happen under an OS file lock (`engine.lock`) in `engine.resolveAll`; read-only commands (`ls`, `var`) call `Engine.Inspect` and never write.
- `tosk watch` is not implemented (`errNotImplemented`).
- Every function that runs git or a shell command takes a `context.Context` first (`cli.Main` cancels it on Ctrl-C / SIGTERM). Use `exec.CommandContext`.
- The engine reads no environment: `TOSK_PROFILE` is read in `internal/cli` (`repo.engine`) and passed as `Options.Profile`.
- `profile:` rules in `.tosk.yml` are an ordered list (`config.ProfileRules`): the first matching rule in file order wins.
- Tests: packages that run git have a `TestMain` setting `GIT_CONFIG_GLOBAL=os.DevNull` and `GIT_CONFIG_NOSYSTEM=1`. CLI tests use `newRoot()` + `SetArgs` + `t.Chdir`.
- Lint: `make lint` (vet + gofmt) and `golangci-lint run` (`.golangci.yml`, run in CI).
