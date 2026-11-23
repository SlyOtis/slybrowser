package slyb

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type SpaceTie struct {
	Folders []string `json:"folders"`
	Domains []string `json:"domains,omitempty"`
}

type SpaceTies struct {
	Spaces map[string]SpaceTie `json:"spaces"`
}

func GetSpaceTies() (SpaceTies, error) {
	path, err := homeFile("spaces.json")
	if err != nil {
		return SpaceTies{}, err
	}
	ties := SpaceTies{Spaces: map[string]SpaceTie{}}
	if err := readJSONFile(path, &ties); err != nil && !os.IsNotExist(err) {
		return SpaceTies{}, err
	}
	if ties.Spaces == nil {
		ties.Spaces = map[string]SpaceTie{}
	}
	return ties, nil
}

func PutSpaceTies(ties SpaceTies) error {
	path, err := homeFile("spaces.json")
	if err != nil {
		return err
	}
	return writeJSONFile(path, ties)
}

func insideFolder(path, folder string) bool {
	return path == folder || strings.HasPrefix(path, strings.TrimSuffix(folder, "/")+"/")
}

func projectSpace(ctx context.Context, project Project, ties SpaceTies, rules RuleStore, groups GroupStore) string {
	best, length := "", 0
	for space, tie := range ties.Spaces {
		for _, folder := range tie.Folders {
			if insideFolder(project.Path, folder) && len(folder) > length {
				best, length = space, len(folder)
			}
		}
	}
	if best != "" {
		return best
	}
	for space, tie := range ties.Spaces {
		for _, domain := range tie.Domains {
			if relatesTo(hostOf(project.Remote), domain) || strings.HasSuffix(project.Email, "@"+domain) {
				return space
			}
		}
	}
	input := RuleInput{URL: project.Remote, Title: project.Name, Folder: project.Path, Remote: project.Remote, Email: project.Email}
	for _, rule := range rules.Rules {
		if rule.Scope != ScopeSpace {
			continue
		}
		if matched, err := runRule(ctx, rule, input); err == nil && matched {
			return rule.Space
		}
	}
	held := map[string]string{}
	for _, group := range groups.Groups {
		for _, section := range group.Sections {
			for _, page := range section.Pages {
				if parsed, err := url.Parse(page.URL); err == nil {
					held[parsed.Host] = group.Space
				}
			}
		}
	}
	votes := map[string]int{}
	for _, page := range project.ClaudePages {
		if parsed, err := url.Parse(page.URL); err == nil && held[parsed.Host] != "" {
			votes[held[parsed.Host]] += page.Sessions
		}
	}
	top := 0
	for space, count := range votes {
		if count > top {
			best, top = space, count
		}
	}
	return best
}

func NewSpaceTieCommand(kind string) *cobra.Command {
	about := map[string]string{
		"folder": "The folders whose projects belong to a workspace; a project in none goes by the workspace rules, then by its pages",
		"domain": "The domains of a workspace: its icon, and pages opened from outside that belong to the domain or carry its name open there",
	}
	tie := &cobra.Command{Use: kind, Short: about[kind]}
	tie.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "List the " + kind + "s tied to each workspace",
		Example: "slyb workspace " + kind + " get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ties, err := GetSpaceTies()
			if err != nil {
				return err
			}
			return printValue(cmd, ties.Spaces)
		},
	})
	change := func(add bool) func(cmd *cobra.Command, args []string) error {
		return func(cmd *cobra.Command, args []string) error {
			path := normalDomain(args[1])
			if kind == "folder" {
				var err error
				if path, err = filepath.Abs(os.ExpandEnv(strings.Replace(args[1], "~", "$HOME", 1))); err != nil {
					return err
				}
			}
			if path == "" {
				return cli.Rejected("name a "+kind, "")
			}
			ties, err := GetSpaceTies()
			if err != nil {
				return err
			}
			entry := ties.Spaces[args[0]]
			list := &entry.Folders
			if kind == "domain" {
				list = &entry.Domains
			}
			kept := []string{}
			for _, item := range *list {
				if item != path {
					kept = append(kept, item)
				}
			}
			if add {
				kept = append(kept, path)
			} else if len(kept) == len(*list) {
				return cli.Rejected(path+" is not tied to "+args[0], "read them with `slyb workspace "+kind+" get`")
			}
			*list = kept
			ties.Spaces[args[0]] = entry
			if err := PutSpaceTies(ties); err != nil {
				return err
			}
			if _, err := PostProjectSync(cmd.Context()); err != nil {
				return err
			}
			if _, err := Call(cmd.Context(), "project.reload", nil); err != nil && !notRunning(err) {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "tied", "space": args[0], kind + "s": kept})
		}
	}
	tie.AddCommand(
		&cobra.Command{Use: "create <workspace_id> <" + kind + ">", Short: "Tie a " + kind + " to a workspace", Example: map[string]string{"folder": "slyb workspace folder create {uuid} ~/Projects/acme", "domain": "slyb workspace domain create {uuid} example.com"}[kind], Args: cobra.ExactArgs(2), RunE: change(true)},
		&cobra.Command{Use: "delete <workspace_id> <" + kind + ">", Short: "Untie a " + kind + " from a workspace", Example: map[string]string{"folder": "slyb workspace folder delete {uuid} ~/Projects/acme", "domain": "slyb workspace domain delete {uuid} example.com"}[kind], Args: cobra.ExactArgs(2), RunE: change(false)},
	)
	return tie
}

func normalDomain(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if host := hostOf(value); host != "" {
		value = host
	}
	return strings.TrimPrefix(strings.Trim(value, "/"), "www.")
}

func relatesTo(host, domain string) bool {
	if host == "" || domain == "" {
		return false
	}
	if host == domain || strings.HasSuffix(host, "."+domain) {
		return true
	}
	name, _, _ := strings.Cut(domain, ".")
	labels := strings.Split(host, ".")
	for _, label := range labels[:len(labels)-1] {
		if label == name {
			return true
		}
	}
	return false
}
