# slybrowser

The Zen browser with a card sidebar, an assistant that sorts your tabs, and a cli that drives all of it.

slybrowser is not a fork. It is the official [Zen](https://zen-browser.app) app plus one privileged extension in its profile, and `slyb`, a command line tool and MCP server that talks to that extension. Zen.app is never modified, so Zen keeps updating itself and keeps the extension.

## What you get

1. **A card sidebar.** Zen's tab list is replaced by groups shown as cards, painted from their favicon colors, with sections nested inside. A second view shows the same space as one card per project.
2. **Spaces inside workspaces.** A Zen workspace can hold several named spaces, each with its own groups, icon and menu.
3. **Sorting by prompt.** `slyb organize` places every loose tab in a space, a group and a section. `slyb space build` builds a whole space from one sentence, out of your stored pages, history and local repositories.
4. **Rules for pages opened from outside.** A rule is a short [Tengo](https://github.com/d5/tengo) script that decides which group a link from another app lands in.
5. **Projects.** `slyb project sync` reads your git repositories and Claude Code sessions and shows each project with its open tabs and the pages used in it.
6. **Live items.** A page can carry a count on its row; a GitHub repository page lists its open pull requests and issues as subpages.
7. **A welcome page.** The new tab page greets you and draws the day's browsing as a tree, with the word of the day under it.
8. **Full control from the terminal.** Tabs, groups, spaces, history, the sidebar and the top apps are all readable and writable through `slyb`, as a table on a terminal and as json in a pipe.

## Requirements

1. macOS with [Zen](https://zen-browser.app) installed and started once, so it has a profile.
2. Go 1.26 and [just](https://github.com/casey/just).
3. [Claude Code](https://claude.com/claude-code) on the PATH for everything that asks the assistant: organize, build, summaries, rules written from a prompt.
4. `gh` for live GitHub pages and `slyb work get`. Optional.

## Install

```sh
git clone https://github.com/SlyOtis/slybrowser.git
cd slybrowser
just install
slyb restart
slyb plugin install
```

`just install` builds `slyb`, puts it in `$(go env GOPATH)/bin` and runs `slyb install`, which writes the extension to `~/.slyb/browser` and registers it in the default Zen profile. Pass `slyb install --profile <dir>` for another profile. A changed extension loads after `just install` and `slyb restart`.

`slyb plugin install` adds the skill and the MCP server to the one host it finds on the machine and asks which when it finds several. A flag picks the host, and several flags can be combined: `--claude` for Claude Code, `--codex` for Codex CLI, `--chatgpt` for ChatGPT desktop, `--gemini` for Gemini CLI, `--opencode` for opencode, `--desktop` for Claude Desktop and `--antigravity` for Antigravity. Every other entry in the host's own config is kept, and `slyb plugin status` says whether each installed host matches the binary. `just install-cli-completion` adds shell completion.

Coming from Arc or Dia, `just import arc` and `just import dia` copy spaces, folders, pinned tabs, open tabs and history into Zen, once, with Zen running. Add `--plan` to see what would happen first.

## The cli

```sh
slyb status
slyb tab get
slyb tab create https://zen-browser.app --select
slyb group create Reading
slyb group create --ask "tst and prd pages, a section per environment"
slyb organize
slyb space build "a work space: one group per environment, one for github with all related repos"
slyb rule create --group 3f9a2c1b7d4e --ask "the tst hosts of the app"
slyb rule match https://git.example.com/platform/hooks
slyb project sync
slyb welcome refresh
```

`slyb --help` prints the whole command table, and every command has its own `--help` with an example. Add `--json` for json on a terminal.

## How it works

1. **The extension** lives in `browser/` and is embedded in the `slyb` binary. It is an unsigned experiment extension that runs in the browser's parent process, draws the sidebar, and serves a unix socket at `~/.slyb/browser.sock`.
2. **The cli** in `internal/slyb/` writes one `{"method","params"}` request per connection to that socket and reads the reply. Everything the sidebar's buttons do, they do by starting `slyb`.
3. **The assistant** is Claude Code run headless by the cli with a json schema for the answer. Code gathers the catalog and applies the result; the model only decides where things go.
4. **The MCP server** is `slyb mcp`, the same commands exposed as tools, so an agent can read and arrange the browser. `slyb plugin install` installs it together with the skill into Claude Code, Codex, ChatGPT desktop, Gemini CLI, opencode, Claude Desktop or Antigravity.
5. **State** is plain json under `~/.slyb`: groups, rules, spaces, projects, settings and the welcome page. Nothing is seeded; a fresh install starts empty.

`slyb install` turns off extension signature checks in the Zen profile it installs into, which is what lets an unsigned extension load. Use a profile you are comfortable doing that in.

## Development

```sh
just build        # dist/slyb
just test         # go vet and go test
just dev status   # run without installing
```

## Build on it

Take it apart, lift the pieces, make it yours. The socket protocol, the rule engine and the sidebar are separate enough to reuse on their own. Issues and pull requests are welcome, and if you build something from it, a link back is appreciated.

## License and credit

[MIT](LICENSE), copyright Martin Ottersland. Use it and copy it freely; keep the copyright notice.

Zen is its own project under its own license and is not distributed here.
