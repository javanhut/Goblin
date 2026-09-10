# Goblin

Goblin gives every project an isolated package environment, the way Nix does,
but driven by the package managers you already use. You pick which managers a
project needs (Cargo, Go modules, npm, pnpm, Yarn, Bun, Deno, uv, pip, Poetry,
Zig, Bundler, Composer, Maven, Gradle, .NET, Mix, Swift PM), Goblin writes a
`goblin.toml` next to the code, and from then on:

- every manager's global caches, registries, toolchains and installed binaries
  live under the project's `.goblin/` directory instead of your home directory;
- `goblin build` rebuilds the whole environment from `goblin.toml` on any machine;
- build outputs and dependency folders (`target/`, `node_modules/`, `.venv/`, …)
  are kept out of git or ivaldi automatically, and are untracked if they were
  ever committed.

## Install

```sh
go build -o goblin .
install -m 755 goblin ~/.local/bin/
```

## Workflow

```sh
goblin init                       # interactive: name, VCS, managers, paths
goblin build                      # fetch dependencies + build inside .goblin/
goblin sync                       # refresh .gitignore / .ivaldiignore, untrack artifacts
goblin exclude target/ dist/      # add more paths, synced immediately
goblin shell                      # a shell with the environment applied
goblin run -- cargo test          # one command inside the environment
goblin status                     # what is configured, built and excluded
goblin clean                      # delete excluded artifacts (asks first)
```

Non-interactive init for scripts and CI:

```sh
goblin init -y                              # accept detected managers and VCS
goblin init --with cargo,npm=web,uv=py --vcs ivaldi --name shop --build
```

## goblin.toml

```toml
[goblin]
name = "shop"
version = 1
vcs = "git"          # git | ivaldi | none
root = ".goblin"     # where isolated caches and env scripts live

[[manager]]
kind = "cargo"
path = "."

[[manager]]
kind = "npm"
path = "web"
build = ["npm run build"]          # override the default steps
env = { NODE_ENV = "production" }  # extra variables for this manager

[exclude]
paths = ["target/", "web/node_modules/", "web/dist/"]

[env]
RUST_LOG = "info"                  # applied to every command in the env
```

`install`, `build` and `artifacts` on a manager override the catalog defaults.
Commands and variables may use `{root}`, `{path}`, `{bin}` and `{name}`.

## How isolation works

Goblin does not sandbox processes. It sets the environment variables each
manager honours (`CARGO_HOME`, `GOMODCACHE`, `npm_config_cache`, `UV_CACHE_DIR`,
`GRADLE_USER_HOME`, …) so that everything they download or install lands in
`.goblin/`. Binaries built or installed by any manager end up in `.goblin/bin`,
which is first on `PATH` inside `goblin shell`. Delete `.goblin/` and the host
machine is untouched; run `goblin build` and it is back.

To apply the environment to your current shell instead of spawning one:

```sh
eval "$(goblin env)"          # bash, zsh, sh
goblin env --fish | source    # fish
```

## Version control

`goblin sync` owns one block in `.gitignore` or `.ivaldiignore`, marked with
`goblin managed` comments; everything outside the block is yours. It always
contains `.goblin/` plus `[exclude].paths`. After writing the file goblin
removes matching tracked files from the git index (`git rm --cached`), or lets
ivaldi drop them at the next seal. `goblin sync --clean` and `goblin clean`
also delete them from disk.

## Development

```sh
go test ./...
go vet ./...
```
