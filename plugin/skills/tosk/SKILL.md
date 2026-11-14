---
name: tosk
description: Prepare and use git worktrees with Tosk. Use when a repository has a .tosk.yml or the user mentions Tosk; when writing or fixing a .tosk.yml; when a new worktree lacks gitignored files (.env, local config), dependencies, or its own ports and database names; and before starting a dev server, database or container inside a git worktree, so it uses that worktree's own port and names.
---

# Tosk

Tosk prepares git worktrees. A fresh worktree only has tracked files; Tosk copies what git ignores, renders local config, allocates what must be unique per worktree (ports, database names), runs setup commands, and tears it down on removal. The configuration is `.tosk.yml` at the root of the main worktree, committed. A `post-checkout` hook runs Tosk on every `git worktree add`, including `claude --worktree`.

Check it is installed with `tosk --version`. If it is not, it installs with `go install github.com/leukalm/tosk@latest` or from https://github.com/leukalm/tosk/releases. Ask before installing.

## Working inside a worktree

Before starting anything that listens on a port or owns external state (dev server, database, docker compose), read this worktree's values instead of the project defaults. Other worktrees, and other agents, run at the same time with their own.

```sh
tosk var            # all variables of the current worktree
tosk var port       # one value
```

Use them: `PORT=$(tosk var port) npm run dev`, `docker compose -p "$(tosk var slug)" up`. If `tosk var` says a variable "is not allocated yet", run `tosk sync`.

If the worktree looks unprepared (missing `.env`, no `node_modules`), run `tosk sync`: it is safe to run any time and only does what is missing. `tosk ls` shows every worktree with its status (`ready`, `pending`), profile and variables.

## Writing a .tosk.yml

1. Find what a fresh checkout lacks. Compare the main worktree with what git tracks: `git status --ignored --short` lists ignored files (`.env*`, `*.local.*`, IDE settings, certificates). Look for hard-coded ports and database names in config files and scripts, and for slow dependency installs.
2. Run `tosk init` if there is no `.tosk.yml`. It also registers the hook, through Husky or Lefthook when the repository uses them.
3. Write the config. Keep each step idempotent: `sync` may run at any time.
4. Try it on a throwaway branch, then clean up:
   ```sh
   tosk add tosk-try          # creates ../<repo>-tosk-try and prepares it
   tosk ls
   tosk rm tosk-try
   git branch -D tosk-try
   ```
5. Run `tosk doctor` and fix what it reports.

### Format

```yaml
vars:
  port: { provider: port, base: 8100, range: 100 }   # free port, stable per worktree
  db:   "myapp_{{slug}}"                             # plain string with {{…}}

profile: { agent: "branch matches '^worktree-'" }    # rules choosing a profile

create:                                   # once, right after git worktree add
  - copy: .env                            # from the main worktree; skipped if present
  - copy: "config/*.local.properties"     # globs work
  - link: node_modules                    # symlink to the main worktree's copy
  - template: config/app.properties.tpl   # renders config/app.properties
  - replace: { file: .env, match: "^PORT=.*", with: "PORT={{port}}" }
  - write:   { file: .tosk-env, content: "DB={{db}}\n" }
  - run: "{{main}}/scripts/create-db.sh {{db}}"
  - run: { cmd: npm run seed, when: "exists('data/seed.sql')" }

sync:                                     # on every tosk sync
  - run: { cmd: "{{main}}/scripts/migrate.sh {{db}}", mode: always }

remove:                                   # on tosk rm, before the directory goes
  - run: "{{main}}/scripts/drop-db.sh {{db}}"

profiles:
  agent:
    create:
      - skip: [link:node_modules]         # drop a base step by name
      - run: npm ci --prefer-offline      # added after the base steps
```

- Built-in variables: `name` (directory), `slug` (branch, safe for identifiers), `branch`, `path`, `main` (main worktree path), `index` (0 for the main worktree, then 1, 2…), `profile`. In commands they are also `$TOSK_<NAME>`.
- Providers: `port` (`base`, `range`), `counter` (`start`), or a script (`provider: ./alloc.sh`, called as `<script> allocate <name>` and `<script> release <name> <value>`).
- A step's name is `<type>:<argument>` as written, before expansion: `link:node_modules`, `run:{{main}}/scripts/create-db.sh {{db}}`. `skip` and `tosk run` use these names.
- `run` steps run once by default, `mode: always` reruns them on every sync. A failed step is retried on the next sync; steps before it are not rerun.
- `when:` takes `exists('path')`, `<var> matches 'regex'`, `==`, `!=`, `!`, `&&`, `||` and parentheses.
- `remove` is for what lives outside the worktree (databases, containers). Links are removed automatically; files go with the directory.
- Files that `copy`, `template` and `write` create must be gitignored, otherwise `git worktree remove` refuses the "dirty" worktree.
- For Claude Code worktrees (`.claude/worktrees/<name>`, branch `worktree-<name>`), select a profile with `branch matches '^worktree-'`.

### Rules that keep it safe

- Commands run in the new worktree, so `./scripts/x.sh` runs that branch's copy. Prefer `{{main}}/scripts/x.sh` when worktrees may be created for untrusted branches.
- Do not quote `{{var}}` yourself in `run`: Tosk passes each value as a quoted reference to its environment variable, so values never become shell code.
- Paths in file steps are relative to the worktree and cannot leave it, even through a symlink. Do not try to write outside the worktree with them; use a `run` step if it is really needed.
- Do not put secrets in `.tosk.yml`, which is committed. Copy them from the main worktree with `copy`.

## Commands

| Command | Use |
|---|---|
| `tosk init` | Create `.tosk.yml` and register the hook |
| `tosk add <branch> [path] [--profile p]` | `git worktree add` (creates the branch if needed), then prepare |
| `tosk sync` | Prepare what is missing in every worktree, rerun `always` steps |
| `tosk rm <worktree> [--force]` | `remove` phase, then `git worktree remove`. Refuses dirty or locked worktrees before running anything |
| `tosk ls` | Worktrees with status, profile and variables |
| `tosk var [name]` | Variables of the current worktree |
| `tosk run <step>` | Rerun one step by name, ignoring `when` and whether it already ran |
| `tosk doctor` | Check the hook, git version and allocations |

## When something goes wrong

- Worktree `pending` in `tosk ls`: a step failed. Run `tosk sync` in it to see the error; the command's output is shown only on failure.
- One step to retry: `tosk run 'run:{{main}}/scripts/create-db.sh {{db}}'`.
- Nothing happens on `git worktree add`: run `tosk doctor`. Tosk must be on `PATH` for the hook to call it. With Husky, worktrees added from a linked worktree need `- copy: .husky/_` in `create`.
- `path escapes from parent`: a file step met a symlink leaving the worktree. Remove the symlink, or do not use that path.
- `tosk rm` refuses: the worktree has uncommitted changes, or is locked (Claude Code locks the worktrees it is using). Commit or discard, or use `--force` only if the user agrees.
