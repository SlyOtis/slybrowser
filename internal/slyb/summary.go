package slyb

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Summary struct {
	ID          string         `json:"id"`
	Space       string         `json:"space"`
	Scope       string         `json:"scope"`
	Target      string         `json:"target"`
	Title       string         `json:"title"`
	Overview    string         `json:"overview"`
	Points      []JourneyPoint `json:"points"`
	Suggestions Suggestions    `json:"suggestions"`
	CreatedAt   string         `json:"created_at"`
}

type SummaryStore struct {
	Summaries []Summary `json:"summaries"`
}

type pageStats struct {
	URL       string   `json:"url"`
	Title     string   `json:"title"`
	Visits    int      `json:"visits"`
	LastVisit *string  `json:"last_visit"`
	Recent    []string `json:"recent"`
}

type summaryReply struct {
	Title       string         `json:"title"`
	Overview    string         `json:"overview"`
	Points      []JourneyPoint `json:"points"`
	Suggestions Suggestions    `json:"suggestions"`
}

const summarySchema = `{"type":"object","properties":{"title":{"type":"string"},"overview":{"type":"string"},"points":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"url":{"type":"string"},"title":{"type":"string"}},"required":["text","url","title"],"additionalProperties":false}},` + suggestionsSchema + `},"required":["title","overview","points","suggestions"],"additionalProperties":false}`

func GetSummaries() (SummaryStore, error) {
	path, err := homeFile("summaries.json")
	if err != nil {
		return SummaryStore{}, err
	}
	store := SummaryStore{Summaries: []Summary{}}
	if err := readJSONFile(path, &store); err != nil && !os.IsNotExist(err) {
		return SummaryStore{}, err
	}
	return store, nil
}

func PutSummaries(cmd *cobra.Command, store SummaryStore) error {
	path, err := homeFile("summaries.json")
	if err != nil {
		return err
	}
	if err := writeJSONFile(path, store); err != nil {
		return err
	}
	if _, err := Call(cmd.Context(), "groups.reload", nil); err != nil && !notRunning(err) {
		return err
	}
	return nil
}

func NewSummaryCommand() *cobra.Command {
	summary := &cobra.Command{Use: "summary", Short: "Summaries of how the pages of a space, group or section were visited"}
	summary.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "List the summaries, newest first",
		Example: "slyb summary get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetSummaries()
			if err != nil {
				return err
			}
			return printValue(cmd, store.Summaries)
		},
	})
	var space, groupID, section, projectID, model string
	create := &cobra.Command{
		Use:   "create",
		Short: "Summarize the visiting history of a space, a group or a section, shown in the space's AI area",
		Long: "Collect the pages of the space, the group or the section with how often and when each was visited, then let\n" +
			"claude write a short title, an overview and points that link to the pages behind them. The summary shows as a\n" +
			"loose item in the AI area of the space. The Summarize item in a space, group or section menu runs this.",
		Example: "slyb summary create --group 3f9a2c1b7d4e --section Docs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			groups, err := GetGroups()
			if err != nil {
				return err
			}
			urls := map[string]bool{}
			scope, target := "space", ""
			if projectID != "" {
				index, err := GetProjectIndex()
				if err != nil {
					return err
				}
				for _, project := range index.Projects {
					if project.ID != projectID {
						continue
					}
					scope, target, space = "project", project.Name, project.Space
					for _, page := range project.ClaudePages {
						urls[page.URL] = true
					}
				}
				if target == "" {
					return cli.Rejected("no project "+projectID, "read them with `slyb project get`")
				}
				if space, err = activeSpace(ctx, space); err != nil {
					return err
				}
				var tabs []Tab
				if err := CallInto(ctx, "tab.get", nil, &tabs); err != nil {
					return err
				}
				for _, tab := range tabs {
					if tab.Project != nil && *tab.Project == projectID {
						urls[tab.URL] = true
					}
				}
			} else if groupID != "" {
				group, err := groups.Group(groupID)
				if err != nil {
					return err
				}
				space, scope, target = group.Space, "group", group.Title
				if section != "" {
					scope, target = "section", group.Title+" / "+section
				}
				for _, entry := range group.Sections {
					if inSection(entry.Label, section) {
						for _, page := range entry.Pages {
							urls[page.URL] = true
						}
					}
				}
			} else {
				if space, err = activeSpace(ctx, space); err != nil {
					return err
				}
				var spaces []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				if err := CallInto(ctx, "workspace.get", nil, &spaces); err != nil {
					return err
				}
				for _, entry := range spaces {
					if entry.ID == space {
						target = entry.Name
					}
				}
				for _, group := range groups.Groups {
					if group.Space != space {
						continue
					}
					for _, entry := range group.Sections {
						for _, page := range entry.Pages {
							urls[page.URL] = true
						}
					}
				}
				var tabs []Tab
				if err := CallInto(ctx, "tab.get", map[string]any{"space": space}, &tabs); err != nil {
					return err
				}
				for _, tab := range tabs {
					if strings.HasPrefix(tab.URL, "http") {
						urls[tab.URL] = true
					}
				}
			}
			if len(urls) == 0 {
				return cli.Rejected("there are no pages to summarize in "+target, "")
			}
			list := make([]string, 0, len(urls))
			for url := range urls {
				list = append(list, url)
			}
			var stats []pageStats
			if err := CallInto(ctx, "history.stats", map[string]any{"urls": list}, &stats); err != nil {
				return err
			}
			names, err := spaceNames(cmd)
			if err != nil {
				return err
			}
			titles, err := groupTitles()
			if err != nil {
				return err
			}
			statsJSON, _ := json.Marshal(stats)
			prompt := fmt.Sprintf("Summarize how the user visited the pages of %q in their browser, from this visiting history: %s\n\n"+
				"Give a short title, a two or three sentence overview of what they spent time on and when, and four to eight "+
				"points, each one short sentence with the url and title of the page it is about, most telling first. Use only "+
				"urls from the history.\n\n%s", target, statsJSON, suggestionsPrompt(names, titles))
			var reply summaryReply
			finish := step("summary", "Summarizing "+target)
			err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: summarySchema, Model: model}, &reply)
			finish(err)
			if err != nil {
				return err
			}
			points := []JourneyPoint{}
			for _, point := range reply.Points {
				if urls[point.URL] {
					points = append(points, point)
				}
			}
			store, err := GetSummaries()
			if err != nil {
				return err
			}
			made := Summary{ID: newID(), Space: space, Scope: scope, Target: target, Title: reply.Title, Overview: reply.Overview, Points: points, Suggestions: validSuggestions(reply.Suggestions, urls, names), CreatedAt: now()}
			store.Summaries = append([]Summary{made}, store.Summaries...)
			if err := PutSummaries(cmd, store); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": made.Title, "item": made})
		},
	}
	create.Flags().StringVar(&space, "workspace", "", "Workspace id, the active workspace when neither --workspace nor --group is given")
	create.Flags().StringVar(&groupID, "group", "", "Summarize this group")
	create.Flags().StringVar(&section, "section", "", "With --group, summarize only this section path")
	create.Flags().StringVar(&projectID, "project", "", "Summarize the pages of this project")
	create.Flags().StringVar(&model, "model", "", "Claude model, the claude default when empty")
	remove := &cobra.Command{
		Use:     "delete <summary_id>",
		Short:   "Remove a summary from the AI area",
		Example: "slyb summary delete 3f9a2c1b7d4e",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetSummaries()
			if err != nil {
				return err
			}
			kept := store.Summaries[:0]
			found := false
			for _, entry := range store.Summaries {
				if entry.ID == args[0] {
					found = true
					continue
				}
				kept = append(kept, entry)
			}
			if !found {
				return cli.Rejected("no summary with id "+args[0], "read the ids with `slyb summary get`")
			}
			store.Summaries = kept
			if err := PutSummaries(cmd, store); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "removed", "id": args[0]})
		},
	}
	summary.AddCommand(create, remove)
	return summary
}
