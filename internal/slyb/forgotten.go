package slyb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Forgotten struct {
	URLs     map[string]bool `json:"urls"`
	Searches map[string]bool `json:"searches"`
	Projects map[string]bool `json:"projects"`
}

func GetForgotten() (Forgotten, error) {
	path, err := homeFile("forgotten.json")
	if err != nil {
		return Forgotten{}, err
	}
	forgotten := Forgotten{}
	if err := readJSONFile(path, &forgotten); err != nil && !os.IsNotExist(err) {
		return Forgotten{}, err
	}
	if forgotten.URLs == nil {
		forgotten.URLs = map[string]bool{}
	}
	if forgotten.Searches == nil {
		forgotten.Searches = map[string]bool{}
	}
	if forgotten.Projects == nil {
		forgotten.Projects = map[string]bool{}
	}
	return forgotten, nil
}

func PutForgotten(forgotten Forgotten) error {
	path, err := homeFile("forgotten.json")
	if err != nil {
		return err
	}
	return writeJSONFile(path, forgotten)
}

func GetProjectIndex() (ProjectIndex, error) {
	home, err := Home()
	if err != nil {
		return ProjectIndex{}, err
	}
	index := ProjectIndex{}
	if err := readJSONFile(filepath.Join(home, "projects.json"), &index); err != nil && !os.IsNotExist(err) {
		return ProjectIndex{}, err
	}
	return index, nil
}

func NewForgottenCommand() *cobra.Command {
	forgotten := &cobra.Command{Use: "forgotten", Short: "Pages and searches removed from what the Claude Code sessions taught the browser"}
	forgotten.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "List the forgotten pages and searches",
		Example: "slyb forgotten get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := GetForgotten()
			if err != nil {
				return err
			}
			return printValue(cmd, value)
		},
	})
	var searches []string
	var space, groupID, section, projectID, from string
	var history bool
	create := &cobra.Command{
		Use:   "create [url...]",
		Short: "Remove pages from Claude: from every project, the frequent list and the pages Claude placed in groups",
		Long: "Forget the urls given, the searches given with --search, or every page of a space, a group, a section or a\n" +
			"project. A forgotten page never comes back from the Claude Code sessions: project sync skips it, and the\n" +
			"unverified pages Claude placed in groups with it are removed. Pages you verified stay where they are.",
		Example:     "slyb forgotten create --group 3f9a2c1b7d4e --section Docs",
		Annotations: map[string]string{cli.AnnotationMCP: "destructive"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			groups, err := GetGroups()
			if err != nil {
				return err
			}
			urls := map[string]bool{}
			for _, raw := range args {
				urls[normalURL(raw)] = true
			}
			queries := map[string]bool{}
			for _, query := range searches {
				queries[strings.ToLower(strings.TrimSpace(query))] = true
			}
			for _, group := range groups.Groups {
				if !(groupID != "" && group.ID == groupID) && !(space != "" && groupID == "" && group.Space == space) {
					continue
				}
				for _, entry := range group.Sections {
					if groupID != "" && !inSection(entry.Label, section) {
						continue
					}
					for _, page := range entry.Pages {
						urls[normalURL(page.URL)] = true
					}
				}
			}
			if from != "claude" && from != "sidebar" && from != "both" {
				return cli.Rejected("--from takes claude, sidebar or both", "")
			}
			if from != "claude" && projectID == "" {
				return cli.Rejected("--from sidebar and both need --project", "")
			}
			var prefixes []string
			if projectID != "" {
				index, err := GetProjectIndex()
				if err != nil {
					return err
				}
				found := false
				for _, project := range index.Projects {
					if project.ID != projectID {
						continue
					}
					found = true
					prefixes = append(prefixes, project.Prefixes...)
					for _, page := range project.ClaudePages {
						urls[normalURL(page.URL)] = true
					}
					for _, search := range project.ClaudeSearches {
						queries[strings.ToLower(search.Query)] = true
					}
				}
				if !found {
					return cli.Rejected("no project "+projectID, "read them with `slyb project get`")
				}
			}
			delete(urls, "")
			delete(queries, "")
			if from == "sidebar" {
				urls, queries = map[string]bool{}, map[string]bool{}
			}
			if len(urls) == 0 && len(queries) == 0 && from == "claude" {
				return cli.Rejected("nothing to forget", "pass urls, --search, --space, --group or --project")
			}
			value, err := GetForgotten()
			if err != nil {
				return err
			}
			for url := range urls {
				value.URLs[url] = true
			}
			for query := range queries {
				value.Searches[query] = true
			}
			if from != "claude" {
				value.Projects[projectID] = true
			}
			if err := PutForgotten(value); err != nil {
				return err
			}
			removed := 0
			for i := range groups.Groups {
				for j := range groups.Groups[i].Sections {
					kept := groups.Groups[i].Sections[j].Pages[:0]
					for _, page := range groups.Groups[i].Sections[j].Pages {
						if urls[normalURL(page.URL)] && page.Source == SourceClaude && !page.Verified {
							removed++
							continue
						}
						kept = append(kept, page)
					}
					groups.Groups[i].Sections[j].Pages = kept
				}
			}
			if err := PutGroups(ctx, groups); err != nil {
				return err
			}
			if _, err := PostProjectSync(ctx); err != nil {
				return err
			}
			if _, err := Call(ctx, "project.reload", nil); err != nil && !notRunning(err) {
				return err
			}
			summary := fmt.Sprintf("Forgot %d pages and %d searches, %d placed pages removed", len(urls), len(queries), removed)
			if from != "claude" {
				summary = "Removed the project from the sidebar. " + summary
			}
			if history {
				list := make([]string, 0, len(urls))
				for url := range urls {
					list = append(list, url)
				}
				var deleted struct {
					Removed int `json:"removed"`
				}
				if err := CallInto(ctx, "history.delete", map[string]any{"urls": list, "prefixes": prefixes}, &deleted); err != nil {
					return err
				}
				summary += fmt.Sprintf(", %d history entries deleted", deleted.Removed)
			}
			return printValue(cmd, map[string]any{"summary": summary})
		},
	}
	create.Flags().StringArrayVar(&searches, "search", nil, "A search to forget, repeatable")
	create.Flags().StringVar(&space, "workspace", "", "Forget every page of the groups of this workspace")
	create.Flags().StringVar(&groupID, "group", "", "Forget every page of this group")
	create.Flags().StringVar(&section, "section", "", "With --group, only this section path")
	create.Flags().StringVar(&projectID, "project", "", "Forget every page and search Claude used in this project")
	create.Flags().StringVar(&from, "from", "claude", "claude forgets the pages Claude taught, sidebar hides the project, both does both")
	create.Flags().BoolVar(&history, "history", false, "Also delete the browsing history of these pages and, with --project, of every page under the project's addresses")
	remove := &cobra.Command{
		Use:     "delete <url>",
		Short:   "Let Claude teach the browser a forgotten page again",
		Example: "slyb forgotten delete https://claude.com/claude-code",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := GetForgotten()
			if err != nil {
				return err
			}
			key := normalURL(args[0])
			if !value.URLs[key] {
				return cli.Rejected(key+" is not forgotten", "read them with `slyb forgotten get`")
			}
			delete(value.URLs, key)
			if err := PutForgotten(value); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "remembered", "url": key})
		},
	}
	forgotten.AddCommand(create, remove)
	return forgotten
}
