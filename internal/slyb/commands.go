package slyb

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/nimling/nimbody/stream"
	"github.com/spf13/cobra"
)

var flagJSON bool

func AddFlags(root *cobra.Command) {
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "Print json on a terminal")
}

func printRaw(cmd *cobra.Command, payload json.RawMessage) error {
	return cli.Print(cmd.OutOrStdout(), payload, cli.FormatFor(cmd.OutOrStdout(), flagJSON))
}

func printValue(cmd *cobra.Command, value any) error {
	return cli.PrintValue(cmd.OutOrStdout(), value, cli.FormatFor(cmd.OutOrStdout(), flagJSON))
}

func run(cmd *cobra.Command, method string, params any) error {
	payload, err := Call(cmd.Context(), method, params)
	if err != nil {
		return err
	}
	return printRaw(cmd, payload)
}

func onOff(cmd *cobra.Command, name string) (*bool, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	value, _ := cmd.Flags().GetString(name)
	switch value {
	case "on":
		return new(true), nil
	case "off":
		return new(false), nil
	default:
		return nil, cli.Rejected("--"+name+" takes on or off", "")
	}
}

func NewStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Show whether slybrowser is running, its Zen version, windows, tabs and the active space",
		Example: "slyb status",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "status", nil)
		},
	}
}

func NewTabCommand() *cobra.Command {
	tab := &cobra.Command{Use: "tab", Short: "Read, open, change, close, read the text of and sort tabs"}

	var space string
	get := &cobra.Command{
		Use:     "get",
		Short:   "List open tabs in every window with id, title, url, space, folder, pinned and loaded state",
		Example: "slyb tab get --workspace <workspace_id>",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "tab.get", map[string]any{"space": space})
		},
	}
	get.Flags().StringVar(&space, "workspace", "", "Only tabs in this workspace id")

	var createSpace, createGroup string
	var createSelect, createClaude bool
	create := &cobra.Command{
		Use:     "create [url]",
		Short:   "Open a url in a new tab, in a workspace, or inside a group with its workspace and login",
		Example: "slyb tab create https://zen-browser.app --select\nslyb tab create https://github.com/acme --group 3f9a2c1b7d4e\nslyb tab create --group 3f9a2c1b7d4e",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			url := strings.Join(args, "")
			if createGroup != "" {
				if createSpace != "" {
					return cli.Rejected("--group opens in the group's own workspace, drop --workspace", "")
				}
				return run(cmd, "group.open", map[string]any{"id": createGroup, "url": url})
			}
			if url == "" {
				return cli.Rejected("name the url, or pass --group to open a new tab in a group", "slyb tab create https://zen-browser.app")
			}
			var tab Tab
			if err := CallInto(cmd.Context(), "tab.create", map[string]any{"url": url, "space": createSpace, "select": createSelect}, &tab); err != nil {
				return err
			}
			if createClaude && tab.Space != nil {
				store, err := GetGroups()
				if err != nil {
					return err
				}
				group := store.GroupTitled(*tab.Space, claudeGroupTitle)
				if group == nil {
					group = store.PostGroup(*tab.Space, claudeGroupTitle, true)
				}
				if group.Add("Opened from your sessions", KindSection, GroupPage{URL: tab.URL, Title: tab.Title, Source: SourceClaude, Verified: true}) {
					if err := PutGroups(cmd.Context(), store); err != nil {
						return err
					}
				}
			}
			return printValue(cmd, tab)
		},
	}
	create.Flags().StringVar(&createSpace, "workspace", "", "Open the tab in this workspace id")
	create.Flags().StringVar(&createGroup, "group", "", "Open the tab inside this group id: in its workspace and login, tied to the group's page when the url is one, a new tab in the group without a url")
	create.Flags().BoolVar(&createSelect, "select", false, "Switch to the new tab")
	create.Flags().BoolVar(&createClaude, "claude", stream.Machine(), "Add the page to the space's Claude Code group, on by default when Claude Code opens it")

	var updateSpace, updateURL string
	var updateSelect bool
	update := &cobra.Command{
		Use:     "update <tab_id>",
		Short:   "Select, navigate, pin or unpin a tab, or move it to another workspace",
		Example: "slyb tab update 12 --select --pin on",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pin, err := onOff(cmd, "pin")
			if err != nil {
				return err
			}
			return run(cmd, "tab.update", map[string]any{"id": args[0], "select": updateSelect, "space": updateSpace, "url": updateURL, "pin": pin})
		},
	}
	update.Flags().BoolVar(&updateSelect, "select", false, "Switch to the tab and its workspace")
	update.Flags().StringVar(&updateSpace, "workspace", "", "Move the tab to this workspace id")
	update.Flags().StringVar(&updateURL, "url", "", "Load this url in the tab")
	update.Flags().String("pin", "", "on pins the tab, off unpins it")

	remove := &cobra.Command{
		Use:     "delete <tab_id>...",
		Short:   "Close tabs",
		Example: "slyb tab delete 12 14",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "tab.delete", map[string]any{"ids": args})
		},
	}

	content := &cobra.Command{Use: "content", Short: "The readable text of a loaded tab"}
	content.AddCommand(&cobra.Command{
		Use:     "get <tab_id>",
		Short:   "Read the text of a loaded tab, the way reader view extracts it",
		Example: "slyb tab content get 12",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "tab.content", map[string]any{"id": args[0]})
		},
	})

	tab.AddCommand(get, create, update, remove, content)
	return tab
}

func NewWorkspaceCommand() *cobra.Command {
	workspace := &cobra.Command{Use: "workspace", Short: "Read, create, rename and switch Zen workspaces, each with its own profile, domain and theme"}

	get := &cobra.Command{
		Use:     "get",
		Short:   "List workspaces with id, name, icon, profile and which one is active",
		Example: "slyb workspace get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "workspace.get", nil)
		},
	}

	var icon, color string
	create := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a workspace with its own profile, the container of its logins named after it, and a dimmed color",
		Example: "slyb workspace create Work --color purple",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "workspace.create", map[string]any{"name": args[0], "icon": icon, "color": color})
		},
	}
	create.Flags().StringVar(&icon, "icon", "", "Emoji for the workspace")
	create.Flags().StringVar(&color, "color", "", "red, purple, blue, green, turquoise, yellow, orange or pink, dimmed")

	var name, updateIcon, updateColor string
	var selected bool
	update := &cobra.Command{
		Use:     "update <workspace_id>",
		Short:   "Rename a workspace and its profile, change its icon or color, or switch to it",
		Example: "slyb workspace update {uuid} --name Personal --color red",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "workspace.update", map[string]any{"id": args[0], "name": name, "icon": updateIcon, "color": updateColor, "select": selected})
		},
	}
	update.Flags().StringVar(&name, "name", "", "New name")
	update.Flags().StringVar(&updateIcon, "icon", "", "Emoji for the workspace")
	update.Flags().StringVar(&updateColor, "color", "", "red, purple, blue, green, turquoise, yellow, orange or pink, dimmed")
	update.Flags().BoolVar(&selected, "select", false, "Switch to the workspace")

	route := &cobra.Command{Use: "route", Short: "Rules that always open matching pages in a workspace, Zen's own space routing"}
	route.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "List the routing rules with the workspace each one opens pages in",
		Example: "slyb workspace route get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "route.get", nil)
		},
	})
	var match string
	routeCreate := &cobra.Command{
		Use:     "create <workspace_id> <reference>",
		Short:   "Always open pages whose url contains, equals or matches the reference in the workspace",
		Example: "slyb workspace route create {uuid} tst.example.com\nslyb workspace route create {uuid} '^https://git\\.example\\.com/' --match regex",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "route.create", map[string]any{"space": args[0], "reference": args[1], "match": match})
		},
	}
	routeCreate.Flags().StringVar(&match, "match", "contains", "contains, equal-to or regex")
	route.AddCommand(routeCreate, &cobra.Command{
		Use:     "delete <route_id>",
		Short:   "Remove a routing rule",
		Example: "slyb workspace route delete 3f2c9a1e-0d4b-4c1e-9a77-2f6c3d1b8e10",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "route.delete", map[string]any{"id": args[0]})
		},
	})
	workspace.AddCommand(get, create, update, route, NewSpaceTieCommand("folder"), NewSpaceTieCommand("domain"))
	return workspace
}

func NewSidebarCommand() *cobra.Command {
	sidebar := &cobra.Command{Use: "sidebar", Short: "Read and change the sidebar layout"}
	sidebar.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Show whether compact mode is on, the sidebar is expanded, and which side it is on",
		Example: "slyb sidebar get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "sidebar.get", nil)
		},
	})
	update := &cobra.Command{
		Use:     "update",
		Short:   "Turn compact mode on or off and expand or collapse the sidebar",
		Example: "slyb sidebar update --compact on --expanded off",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			compact, err := onOff(cmd, "compact")
			if err != nil {
				return err
			}
			expanded, err := onOff(cmd, "expanded")
			if err != nil {
				return err
			}
			return run(cmd, "sidebar.update", map[string]any{"compact": compact, "expanded": expanded})
		},
	}
	update.Flags().String("compact", "", "on or off")
	update.Flags().String("expanded", "", "on or off")
	sidebar.AddCommand(update)
	return sidebar
}

func NewHistoryCommand() *cobra.Command {
	history := &cobra.Command{Use: "history", Short: "Read and delete the local browsing history"}
	var query, since string
	var limit int
	get := &cobra.Command{
		Use:     "get",
		Short:   "List visited pages, newest first, where every word of the query is in the url or title; with --since, visits count from then",
		Example: "slyb history get --query \"zen folders\" --limit 20",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"query": query, "limit": limit}
			if since != "" {
				from, err := journeySince(Profile{}, since)
				if err != nil {
					return cli.Rejected(err.Error(), "")
				}
				params["since"] = from.UTC().Format(time.RFC3339)
			}
			return run(cmd, "history.get", params)
		},
	}
	get.Flags().StringVar(&query, "query", "", "Words that must all appear in the url or title")
	get.Flags().IntVar(&limit, "limit", 50, "Most rows to return")
	get.Flags().StringVar(&since, "since", "", "Only visits from this time, such as 2026-10-27T00:00")
	var hosts, prefixes []string
	remove := &cobra.Command{
		Use:     "delete [url...]",
		Short:   "Delete visited pages: the urls given, every page of a domain and its subdomains, or every page under a prefix",
		Example: "slyb history delete --host duckduckgo.com",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && len(hosts) == 0 && len(prefixes) == 0 {
				return cli.Rejected("pass urls, --host or --prefix", "")
			}
			return run(cmd, "history.delete", map[string]any{"urls": args, "hosts": hosts, "prefixes": prefixes})
		},
	}
	remove.Flags().StringArrayVar(&hosts, "host", nil, "Delete every page of this domain and its subdomains, repeatable")
	remove.Flags().StringArrayVar(&prefixes, "prefix", nil, "Delete every page whose url starts with this, repeatable")
	history.AddCommand(get, remove)
	return history
}

func NewEvalCommand() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "eval [code]",
		Short: "Run privileged JavaScript in the newest browser window and print what it returns",
		Long: "Run privileged JavaScript in the newest browser window and print what it returns. The code is the body of an\n" +
			"async function with window globals in scope (gBrowser, gZenWorkspaces, gZenFolders, gZenCompactModeManager,\n" +
			"Services), plus tabId(tab) and getTab(id). It can do anything the browser can.",
		Example:     "slyb eval 'return gBrowser.tabs.length'",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			code := strings.Join(args, "")
			if file != "" {
				var content []byte
				var err error
				if file == "-" {
					content, err = readAll(cmd)
				} else {
					content, err = os.ReadFile(file)
				}
				if err != nil {
					return err
				}
				code = string(content)
			}
			if code == "" {
				return cli.Rejected("pass the code or --file", "slyb eval 'return gBrowser.tabs.length'")
			}
			return run(cmd, "eval", map[string]any{"code": code})
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "Read the code from a file, - for stdin")
	return cmd
}

func NewRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "restart",
		Short:   "Restart Zen with fresh script caches, so a changed slybrowser module loads",
		Example: "slyb restart",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "restart", nil)
		},
	}
}

func NewRemoteCommand() *cobra.Command {
	remote := &cobra.Command{Use: "remote", Short: "Make the running Zen available for remote control by a browser automation tool"}
	remote.AddCommand(&cobra.Command{
		Use:         "get",
		Short:       "Show whether Zen is available for remote control, on which loopback port, and whether an automation session is held",
		Example:     "slyb remote get",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationMCP: "read"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "remote.get", nil)
		},
	}, &cobra.Command{
		Use:         "create",
		Short:       "Make the running Zen available for remote control on 127.0.0.1 without a restart; already available reports the port",
		Example:     "slyb remote create",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "remote.create", nil)
		},
	}, &cobra.Command{
		Use:         "delete",
		Short:       "End the held automation session and stop the remote agent this command started",
		Example:     "slyb remote delete",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationMCP: "destructive"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "remote.delete", nil)
		},
	})
	return remote
}

func NewInstallCommand(browser fs.FS) *cobra.Command {
	var profile string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the slybrowser extension into the Zen profile; Zen loads it on its next start",
		Long: "Write the extension to ~/.slyb/browser, point the profile's extensions folder at it with a proxy file, and set\n" +
			"the three prefs in user.js that let Zen load an unsigned privileged extension. Zen.app is never touched, so\n" +
			"its signature and its own updates stay intact. Quit Zen before installing.",
		Example:     "slyb install",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{cli.AnnotationMCP: cli.AccessNone},
		RunE: func(cmd *cobra.Command, args []string) error {
			if profile == "" {
				userHome, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				profile, err = GetProfile(filepath.Join(userHome, "Library", "Application Support", "zen"))
				if err != nil {
					return err
				}
			}
			installed, err := PostInstall(profile, browser)
			if err != nil {
				return err
			}
			return printValue(cmd, installed)
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "Zen profile directory, the default profile when empty")
	return cmd
}

func NewAppsCommand() *cobra.Command {
	apps := &cobra.Command{Use: "apps", Short: "The top apps grid above the address area"}
	apps.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Show how the top apps grid fits, its tile size and how many apps it shows",
		Example: "slyb apps get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, "apps.get", nil)
		},
	})
	var fit string
	var size float64
	update := &cobra.Command{
		Use:     "update",
		Short:   "Fit the grid by tile width or height, or set the tile size in em",
		Example: "slyb apps update --fit width --size 5",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			change := map[string]any{}
			if fit != "" {
				change["fit"] = fit
			}
			if size > 0 {
				change["size"] = size
			}
			return run(cmd, "apps.update", change)
		},
	}
	update.Flags().StringVar(&fit, "fit", "", "width keeps a minimum tile width and wraps, height keeps a tile height")
	update.Flags().Float64Var(&size, "size", 0, "Tile size in em")
	apps.AddCommand(update)
	return apps
}
