---
name: slyb
description: Controls slybrowser, the user's own build of the Zen browser, through the slyb cli and its mcp server, and keeps its spaces, groups, projects and pages organized the way the user wants. Use when asked to list, open, close, switch or read tabs, organize tabs into groups or by a prompt, build a group, find similar pages, place pages opened from outside with rules, tie folders and projects to spaces, forget pages Claude taught the browser, delete history, change the welcome page and its journey, change spaces, the sidebar or the top apps, search history, when a page of the user's own Zen is to be driven, scraped or tested with a browser automation tool, or when the user says how they want the browser organized.
---

# slyb

slybrowser is the official Zen app with an unsigned privileged experiment extension, `slybrowser@slytech.no`, in its profile. The extension runs in the parent process, serves a unix socket at `~/.slyb/browser.sock`, and replaces Zen's tab list with cards. Zen.app is never modified, so Zen updates itself and keeps the extension. Output is a table on a terminal and json in a pipe or with `--json`.

## What the user sees

1. Top bar: the sidebar button, then home (the welcome page), the Groups / Projects view toggle, New space, Sync projects and Reload. When the sidebar is narrow they move into the "…" menu under headings, Reload first; back and forward stay.
2. Top apps: a grid of the most used sites above the address area.
3. The address area shows the current page in the colors of its group, with a copy button and a clear site data button that warns first.
4. Ungrouped: open pages no group holds, at the top of the space.
5. A space: its header, with its icon, expand all, the settings menu and the chevron. The space scrolls as one unit; groups and sections never scroll on their own.
6. Groups view: groups are cards on shelves, painted from their favicon colors, with text in whatever color reads best on them. A card's header sticks while its pages scroll. Inside, sections nest. Unverified pages sit in their own section.
7. Projects view: one card per project of this space, by shelf, with its open tabs, the pages Claude used in it and the pages you visited. No space header. The first assistant action on a project makes it a group linked to it.
8. Every link has a "…" button and a right-click menu: sort it into a group, remove it from history, from Claude or both, isolate it in a new group with a login of its own, either with the site's current cookies copied in (Isolate with this session…) or signed out (Isolate in its own group…), or clear the history of its whole domain. Removals are instant and finish in the background.
9. A group's "…" menu holds Remove group… (it and its pages leave the sidebar, its open tabs stay as loose tabs) and Delete group… (its open tabs close too), both behind a warning.
10. cmd or ctrl plus w unloads a tab that belongs to a group and keeps it in the group's list; it closes a pinned tab that belongs to no group.
11. Every AI action animates the element it started from; nothing pops up for it.
12. The welcome page: the greeting and tagline, then the journey since the day's start as a tree grown from the roots, with times on the trunk, each point on a branch with icon actions, its subpages and followed links as leaves, and all-at-once actions at the roots.

The interface never names Claude in its labels for the assistant; Claude only appears where the user's data is about Claude.

## Workspaces and spaces

A workspace is Zen's workspace: it has exactly one profile (its container, named after it, which keeps its logins), a domain, and the icon and theme that come from the domain's favicon. Its information sits in the bottom bar: the workspace icons, its name and its theme. A space is something else entirely: a named part of the sidebar that holds groups. A workspace can hold several spaces, each with its own header, name, icon and menu (Rename…, Icon…, New space…, Remove space…, which moves its groups to another space of the workspace and shows only when there is one, and Delete space…, which deletes it with its groups and their pages and closes their open tabs), never the workspace's. The hierarchy is workspace > space > group: deleting a workspace deletes its spaces and their groups. Each space keeps a history of what was sorted into it and what was discarded. Folders and domains are tied to workspaces (`space folder`, `space domain`), and each project lands in the workspace of the deepest tied folder holding it, then by its domain, then by the rules, then by the hosts its Claude sessions used.

## Commands

| Command | Does |
|---|---|
| `slyb status` | Zen version, windows, tab count, active space |
| `slyb tab get` / `tab create <url>` / `tab update <id>` / `tab delete <id>...` / `tab content get <id>` | tabs |
| `slyb group get [id] [--detailed]` / `group create <title>` or `--ask "<what it holds>"` / `group update <id> [--space <id or name>]` / `group delete <id>` | groups |
| `slyb group page create\|update\|delete <group_id> <url>` | add, verify or remove a page |
| `slyb group similar <id>` | pages like the group's, added unverified |
| `slyb space build "<what it should hold>" [--workspace] [--plan]` | build a space from a prompt: code catalogs stored pages, history, Claude pages and local repos; the assistant returns groups and sections as address prefixes and repo names; code fills them and adds a rule per group |
| `slyb page get [--query] [--host]` | the metadata stored for every page loaded: title, description, site, type, heading, when and in which workspace |
| `slyb live get [url]` / `live create <url> [--every <min>]` / `live update <url> --every <min>` / `live delete <url>` / `live sync [url]` | live items: a page that listens for what changes on it and shows a count on its row; a GitHub repository page lists its open pull requests and issues as subpages, read with gh every `--every` minutes (5 by default, 0 for only when opened) and when it gets focus; any other page counts from its tab title, such as "(3) Inbox"; stored in `~/.slyb/live.json` |
| `slyb organize` | sort every unsorted page of the workspace into a space, a group and a section; duplicates, error pages, steps on the way and finished sign-ins are discarded, recorded and closed first |
| `slyb organize [--space] [--group [--section]] [--project] [--page <url>] [--prompt "<how>"] [--history] [--plan]` | place loose tabs, rearrange a group, sort one page, or arrange by a prompt |
| `slyb summary create [--space\|--group [--section]\|--project]` / `summary get` / `summary delete <id>` | summaries in the space's AI area |
| `slyb suggestion apply <welcome\|summary_id> <group\|space\|rule> <index> [--page <url>]` | make what a summary suggested |
| `slyb rule create --space\|--group [--section] (--script\|--file\|--ask "<what to catch>")` / `rule update <id> (--name\|--script\|--file\|--ask "<how it should change>")` / `rule get` / `rule delete <id>` / `rule match <url>` | Tengo rules for pages opened from outside the browser |
| `slyb space get` / `space create <name> [--color]` / `space update <id>` | spaces, each with its own profile |
| `slyb space folder get\|create\|delete <space_id> <path>` | folders whose projects belong to a space |
| `slyb space domain get\|create\|delete <space_id> <domain>` | a space's domain: its icon and theme, and the pages opened from outside that belong to it |
| `slyb space route get\|create\|delete` | Zen's own routing rules |
| `slyb project get` / `project sync` | projects from git repositories and Claude sessions |
| `slyb forgotten create [url...] [--search] [--space\|--group\|--project] [--from claude\|sidebar\|both] [--history]` / `forgotten get` / `forgotten delete <url>` | forget pages Claude taught, hide a project, delete its history |
| `slyb history get [--since]` / `history delete [url...] [--host] [--prefix]` | read and delete browsing history |
| `slyb work get [--since]` | per repository: commits with diff stats from git, pull requests from gh, Claude sessions and their searches linked to the change they led to |
| `slyb welcome get` / `welcome refresh [--since <time>] [--cached]` | the welcome page and its journey |
| `slyb profile get` / `profile update [--name] [--clock 24h\|12h] [--journey-start HH:MM]` | who it greets and when the day starts |
| `slyb finding get` / `finding create "<text>"` | facts about the user's sites that every organize reads |
| `slyb organization get` / `organization update` | the organization rules and the pages kept or dismissed |
| `slyb remote get` / `remote create` / `remote delete` | remote control: get says whether Zen is available, on which loopback port, and whether an automation session is held; create makes the running Zen available on 127.0.0.1 without a restart and reports the port when it already is; delete ends the held session and stops the agent create started |
| `slyb apps get\|update`, `slyb sidebar get\|update`, `slyb search`, `slyb eval`, `slyb restart` | top apps, sidebar, search, privileged JavaScript, restart |

The one time Arc and Dia import is a repo recipe, not a command: `just import dia|arc [--plan]`.

## Driving a page of Zen with an automation tool

Zen takes one automation session at a time. slyb finds the page and makes Zen available; the automation tool does the driving and knows nothing of slyb.

1. Find the tab with `slyb tab get`, and bring it forward with `slyb tab update <id> --select` when the user asks.
2. Run `slyb remote create`. It reports the port; already available is a success.
3. Attach the tool to the tab by its url, with nimon `nimon attach <url>`.
4. Never quit, relaunch or start a second Zen to get a port: a second Zen is not the user's Zen. When `remote create` is refused, say why and stop.
5. When the work is done and the user asks, run `slyb remote delete`; it leaves a Zen that was started with a remote port running and only ends its session.

## Rules for pages opened from outside

A rule belongs to a space, a group or a section. Its script reads `url`, `host`, `path` and `title`, and for a project `folder`, `remote` and `email`, and sets `result = true` when the page or project belongs. Section rules run first, then group rules, then space rules. A matched page opens in that space, lands among the group's unverified pages in the rule's section, and the sidebar scrolls to it. Space rules also place projects.

## The welcome journey

The journey also gets one point for each of the six most active repositories, built in code from `slyb work get`, and one for the rest. Points from browsing carry figures counted by the browser: visits, pages, minutes in front of you and with developer tools, from the focus record in `~/.slyb/focus/`.

A journey covers one period, from the day's start (`--journey-start`, 00:00 by default) or a time the user picks. Each period's journey is stored under `~/.slyb/journeys/`; the browser and the period picker reuse a stored one (`--cached`) and only "Grow a new journey" or a plain `welcome refresh` writes a new summary. Every point carries one group suggestion, and every link in it adds itself to that group.

## Keeping the organization evolving

`~/.slyb/organization.md` is the rule set every Organize follows and this skill owns it; `~/.slyb/findings.md` holds facts about the user's sites. The browser appends every page the user keeps, dismisses or drags to `~/.slyb/learned.jsonl`.

1. When the user says how something should be organized, write the rule into `organization.md` in their words, then run `slyb organize --plan` and show the plan.
2. When the user states a fact about their sites, run `slyb finding create`.
3. When the user asks for a group of a kind, run `slyb group create --ask` with their words.
4. When the user wants pages placed when opened from another app, run `slyb rule create --ask` with their words and the scope they name.

## Rules

1. Tab ids reset when Zen restarts; read them with `tab get` first.
2. Run `organize --plan` first when the user has not asked for the change outright.
3. `history delete`, `forgotten create --history` and `--from sidebar|both` delete browsing history; run them only when the user asked for that deletion.
4. `eval` runs an async function body with `gBrowser`, `gZenWorkspaces`, `Services`, `tabId(tab)`, `getTab(id)` and `folderTabs(folder)` in scope; reach for it only when no command covers the job.
5. Exit codes: 0 ok, 1 failure, 2 rejected input, 3 refused.
