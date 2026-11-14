# Tosk

**Environment preparation for git worktrees.**

A fresh worktree is a bare checkout. Tosk turns it into a ready-to-run environment: it copies what git ignores, renders local config, allocates what must be unique (ports, database names), runs your setup commands, and tears everything down when the worktree goes away.

```
$ git worktree add ../app-feat-invoices feat/invoices
Preparing worktree (new branch 'feat/invoices')
HEAD is now at 3f2a9c1 Add invoice export
tosk: vars  port=8103 db=myapp_feat_invoices
tosk: copy  .env, config/*.local.properties (3 files)
tosk: link  node_modules -> ../app/node_modules
tosk: tmpl  config/application-local.properties
tosk: run   ./scripts/create-db.sh myapp_feat_invoices (4s)
tosk: run   mvn -q dependency:go-offline (12s)
tosk: ready in 17s
```

No wrapper command to remember. No `node_modules` required. Nothing to install in the worktree itself.

---

## Why

`git worktree` is the best way to work on several branches at once, and the natural unit of isolation for coding agents. But git only checks out tracked files. Everything that makes a checkout actually runnable is on you:

- `.env`, local properties, IDE settings, certificates: gitignored, so absent
- anything that must be unique per worktree: ports, database names, container project names
- dependencies that take minutes to rebuild and could just be shared
- setup commands, and the cleanup nobody runs because git has no removal hook

Hook managers like Husky and Lefthook are built around commits. Tosk is built around the lifecycle of a worktree.

## Install

```sh
brew install leukalm/tap/tosk                                    # macOS / Linux
scoop bucket add leukalm https://github.com/leukalm/scoop-bucket  # Windows
scoop install tosk
go install github.com/leukalm/tosk@latest
```

Then, once per repository:

```sh
tosk init
```

This writes a `.tosk.yml` you commit, and registers Tosk on the repository's `post-checkout` hook, through the hook manager you already use:

- **Lefthook**: a `tosk` command is added to the `post-checkout` hook in `lefthook.yml`, then `lefthook install` is run. If your config already has a `post-checkout` hook, or is TOML or JSON, Tosk leaves it untouched and prints the lines to add.
- **Husky**: Tosk is appended to `.husky/post-checkout`, which you commit. The starter `.tosk.yml` also copies `.husky/_` into each new worktree: Husky's generated hooks are gitignored, and without them git runs no hook for a worktree added *from* that worktree.
- **Plain hooks**: Tosk is appended to the existing `post-checkout` script, or creates it.

Entries in committed files call `tosk` through `PATH`, and do nothing for teammates who have not installed it. `tosk doctor` checks that the hook is registered and active.

## Configure

`.tosk.yml` lives at the root of the main worktree and is versioned. It has three parts: variables, phases, profiles.

```yaml
vars:
  port: { provider: port, base: 8100, range: 100 }
  db:   "myapp_{{slug}}"

create:
  - copy: .env
  - copy: "config/*.local.properties"
  - link: node_modules
  - template: config/application-local.properties.tpl
  - replace: { file: .env, match: "^PORT=.*", with: "PORT={{port}}" }
  - write:   { file: .tosk-env, content: "DB_NAME={{db}}\nPORT={{port}}\n" }
  - run: ./scripts/create-db.sh {{db}}
  - run: mvn -q dependency:go-offline
  - run: { cmd: ./scripts/seed.sh, when: "exists('data/seed.sql')" }

sync:
  - run: { cmd: "./scripts/migrate.sh {{db}}", mode: always }

remove:
  - run: ./scripts/drop-db.sh {{db}}
  - run: docker compose -p {{slug}} down --remove-orphans

profiles:
  agent:
    create:
      - skip: [link:node_modules]
      - run: npm ci --prefer-offline
```

### Variables

Built in, available in every step and in `tosk var`:

| Variable | Example |
|---|---|
| `name` | `app-feat-invoices` (directory name) |
| `slug` | `feat_invoices` (branch, safe for identifiers) |
| `branch` | `feat/invoices` |
| `path` | absolute path of the worktree |
| `main` | absolute path of the main worktree |
| `index` | 1, 2, 3… stable for the life of the worktree (0 for the main worktree) |
| `profile` | `agent`, or empty without a profile |

Your own variables are plain strings with `{{…}}` interpolation, or come from a **provider**. Providers allocate something unique and release it on removal. Built-in providers: `port` (a free port in `base`…`base+range-1`, stable across syncs) and `counter` (the smallest free number from `start`, default 1). Others are a script away (`provider: ./scripts/alloc-schema.sh`): Tosk calls `<script> allocate <name>` and keeps what it prints, then `<script> release <name> <value>` on `tosk rm`.

### Steps

| Step | Does | Idempotent |
|---|---|---|
| `copy` | Copy a file or glob from the main worktree. Skips if the target exists. | yes |
| `link` | Symlink to the main worktree (junction on Windows). | yes |
| `template` | Render `foo.tpl` to `foo` with the variables. | yes |
| `replace` | Regex substitution in a file. | yes |
| `write` | Write a file from inline content. | yes |
| `run` | `sh` command in the worktree, variables in env (`TOSK_PORT`, `TOSK_DB`…). Output is shown only on failure. | `once` (default) or `always` |
| `skip` | In a profile: drop a step from the base config. | |

In a `run` line, `{{name}}` is passed to the shell as a quoted reference to its environment variable (`"${TOSK_NAME}"`), never as text: a branch called `x;rm -rf ~` stays an argument. In `replace`, values match and are inserted literally. File steps cannot read or write outside the worktree, even through a symlink.

Every step accepts `when:` with a small expression language (`exists()`, `branch matches`, `profile ==`). Copied files must be gitignored, otherwise `git worktree remove` will refuse to delete a "dirty" worktree.

### Phases

- `create` runs once, right after `git worktree add`.
- `sync` runs on every `tosk sync` (hook, prompt, IDE startup). Use it for things that must track the main worktree, like migrations or refreshed credentials.
- `remove` runs before the directory is deleted. Links to the main worktree are always removed first; everything else Tosk created is gitignored and goes with the directory. Declare the phase for what lives outside it (databases, containers).

### Profiles

A profile overrides or extends the base config. Select it with `tosk add --profile agent`, the `TOSK_PROFILE` environment variable, or a rule in `.tosk.yml` (`profile: { agent: "branch matches '^agent/'" }`; with several rules, the first match in file order wins). Typical split: humans get symlinked dependencies and IDE files, agents get a self-contained install and a throwaway database.

## Commands

| Command | What it does |
|---|---|
| `tosk init` | Create `.tosk.yml` and install the hook |
| `tosk sync` | Reconcile every worktree with the config. Safe to run anytime. |
| `tosk add <branch> [path]` | `git worktree add`, then the `create` phase |
| `tosk rm <path>` | `remove` phase, release providers, then `git worktree remove`. Refuses a worktree with uncommitted changes before running anything; `--force` overrides. |
| `tosk ls` | Worktrees with their profile, variables and status |
| `tosk var [name]` | Print one or all variables of the current worktree |
| `tosk run <step>` | Re-run a single step by name, for debugging a config |
| `tosk doctor` | Check hook installation, git version, orphaned allocations |

You can keep using plain `git worktree add`: the hook prepares the new worktree for you. `tosk rm` exists because git has no hook on removal.

## How it works

Tosk has one core operation, `sync`, and several ways to trigger it.

**Sync is a reconciliation.** It compares `git worktree list --porcelain` with what has been provisioned, runs `create` for new worktrees, `sync` for existing ones, and releases what belonged to worktrees that are gone. Running it twice changes nothing. Missing an event is never fatal: the next sync catches up.

**State lives in git.** Each worktree's variables, profile and completed steps are stored in its own `config.worktree` (`git config --worktree tosk.var.port`). When git removes or prunes the worktree, the record goes with it. No sidecar database, no stale entries.

**Triggers.**

- `post-checkout`: git runs it on `git worktree add`. Tosk detects the null previous HEAD and prepares that worktree, and only that one. This covers `claude --worktree`, subagent isolation, IntelliJ, anything that goes through the `git` binary.
- `tosk rm`: the only way to run `remove` before the directory disappears.
- Planned, not implemented yet: `tosk watch`, a small daemon fed by git's trace2 event stream, which would also see `git worktree remove` and `prune` done by hand.
- Your shell prompt or IDE startup, if you like belt and braces: `tosk sync` takes about a tenth of a second when nothing changed.

## Extending

Tosk exposes its own hook events through git's config-based hooks (git 2.54+):

```ini
[hook "seed-db"]
  event = tosk-post-create
  command = ./scripts/seed-db.sh

[hook "drop-db"]
  event = tosk-pre-remove
  command = ./scripts/drop-db.sh
```

Hooks receive every variable as `TOSK_<NAME>` in the environment. Declare them in `.git/config`, `~/.gitconfig` or a shared `include`, and list them with `git hook list tosk-post-create`. This is how a team can plug its own preparation into a repository's `.tosk.yml` without editing it.

On older git versions, `run` steps do the same job without the plugin layer.

## Coding agents

Tosk needs no agent-specific configuration. Coding agents create worktrees with the `git` CLI, so the `post-checkout` path handles them; use a profile rule to give them their own preparation.

With Claude Code, `claude --worktree <name>` creates `.claude/worktrees/<name>` on a branch `worktree-<name>`, and Tosk prepares it before the session starts. Select a profile for these worktrees with:

```yaml
profile: { agent: "branch matches '^worktree-'" }
```

Claude Code locks its worktrees while a session runs: `tosk rm` refuses a locked worktree, and `tosk rm --force` removes it anyway. When Claude Code deletes a worktree itself at the end of a session, Tosk is not told (git has no removal hook), so the `remove` phase does not run: keep it for things you can clean up by hand, or remove agent worktrees with `tosk rm`.

We recommend **not** defining `WorktreeCreate` / `WorktreeRemove` hooks in Claude Code alongside Tosk: they replace the default worktree logic and currently disable the cleanup prompt at session exit.

## Trust

`.tosk.yml` is always read from the main worktree, never from the branch being checked out. But `run` steps and script providers execute in the new worktree, so a relative command such as `./scripts/create-db.sh` runs **that branch's copy** of the script, like `npm install` runs a branch's `package.json` scripts. With the hook installed this happens on every `git worktree add`, unattended.

If you create worktrees for branches you do not trust (fork pull requests, agent-chosen refs), point commands at the main worktree, `{{main}}/scripts/create-db.sh`, or keep the hook out of that repository and call `tosk sync` yourself.

## Requirements

- git 2.20+ (per-worktree config, where the state lives)
- git 2.54+ for the `tosk-*` plugin events
- macOS, Linux, Windows (Git Bash or PowerShell)

## Status

Early. The core (`sync`, `add`, `rm`, steps, providers, profiles) works and is tested on macOS. Windows builds but has not been tried yet. `tosk watch` is not implemented. Homebrew and Scoop packages are not published yet: use `go install` for now.

## Name

Ratatoskr is the squirrel of Norse myth who runs along the trunk of Yggdrasil carrying messages between the eagle at the top and the dragon at the roots. Tosk is the short, pronounceable half of that name, and the part that means "tooth". It gnaws through the boring parts of worktree setup.

## License

MIT
