package slyb

import (
	"context"
	"strconv"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type SuggestedGroup struct {
	Point   int    `json:"point"`
	Title   string `json:"title"`
	Shelf   string `json:"shelf"`
	Reason  string `json:"reason"`
	Pages   []Page `json:"pages"`
	Applied bool   `json:"applied,omitempty"`
}

type SuggestedSpace struct {
	Point   int    `json:"point"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	Reason  string `json:"reason"`
	Applied bool   `json:"applied,omitempty"`
}

type SuggestedRule struct {
	Point   int    `json:"point"`
	Name    string `json:"name"`
	Space   string `json:"space"`
	Reason  string `json:"reason"`
	Script  string `json:"script"`
	Applied bool   `json:"applied,omitempty"`
}

type Suggestions struct {
	Groups []SuggestedGroup `json:"groups"`
	Spaces []SuggestedSpace `json:"spaces"`
	Rules  []SuggestedRule  `json:"rules"`
}

const suggestionsSchema = `"suggestions":{"type":"object","properties":{` +
	`"groups":{"type":"array","items":{"type":"object","properties":{"point":{"type":"integer"},"title":{"type":"string"},"shelf":{"type":"string"},"reason":{"type":"string"},"pages":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string"},"title":{"type":"string"}},"required":["url","title"],"additionalProperties":false}}},"required":["point","title","shelf","reason","pages"],"additionalProperties":false}},` +
	`"spaces":{"type":"array","items":{"type":"object","properties":{"point":{"type":"integer"},"name":{"type":"string"},"color":{"type":"string","enum":["red","purple","blue","green","turquoise","yellow","orange","pink"]},"reason":{"type":"string"}},"required":["point","name","color","reason"],"additionalProperties":false}},` +
	`"rules":{"type":"array","items":{"type":"object","properties":{"point":{"type":"integer"},"name":{"type":"string"},"space":{"type":"string"},"reason":{"type":"string"},"script":{"type":"string"}},"required":["point","name","space","reason","script"],"additionalProperties":false}}` +
	`},"required":["groups","spaces","rules"],"additionalProperties":false}`

const suggestionsBrief = "Then suggest what would organize this better: groups worth making from these pages, with their pages; " +
	"spaces worth adding, each with a color; and space rules that catch the pages opened from outside the browser that " +
	"belong in a space, each naming that space. " + ruleLanguage + ". Suggest only what the history clearly supports, and leave a list empty when nothing fits. Give each suggestion the point: the index, from 0, of the journey point or summary point it grows out of, or -1 when it belongs to none."

func spaceNames(cmd *cobra.Command) ([]string, error) {
	var spaces []struct {
		Name string `json:"name"`
	}
	if err := CallInto(cmd.Context(), "workspace.get", nil, &spaces); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(spaces))
	for _, space := range spaces {
		names = append(names, space.Name)
	}
	return names, nil
}

func suggestionsPrompt(names, groups []string) string {
	return suggestionsBrief + " Give every point one group: the group its page belongs in, with the point's page among its pages, using the exact title of an existing group when one fits, or a new title. Points may share a group. The groups that exist are: " + strings.Join(groups, ", ") + ". The spaces that exist are: " + strings.Join(names, ", ") + ". A rule names only one of those spaces."
}

func validSuggestions(suggestions Suggestions, known map[string]bool, names []string) Suggestions {
	result := Suggestions{Groups: []SuggestedGroup{}, Spaces: suggestions.Spaces, Rules: []SuggestedRule{}}
	if result.Spaces == nil {
		result.Spaces = []SuggestedSpace{}
	}
	for _, group := range suggestions.Groups {
		var pages []Page
		for _, page := range group.Pages {
			if known[page.URL] {
				pages = append(pages, page)
			}
		}
		if len(pages) > 0 {
			group.Pages = pages
			result.Groups = append(result.Groups, group)
		}
	}
	spaces := map[string]bool{}
	for _, name := range names {
		spaces[name] = true
	}
	for _, rule := range suggestions.Rules {
		if !spaces[rule.Space] {
			continue
		}
		if _, err := runRule(context.Background(), Rule{Name: rule.Name, Script: rule.Script}, RuleInput{URL: "https://example.org/"}); err == nil {
			result.Rules = append(result.Rules, rule)
		}
	}
	return result
}

func NewSuggestionCommand() *cobra.Command {
	suggestion := &cobra.Command{Use: "suggestion", Short: "Act on what a summary or the welcome page suggests"}
	var pageURL string
	apply := &cobra.Command{
		Use:     "apply <welcome|summary_id> <group|space|rule> <index>",
		Short:   "Make the suggested group, space or rule, and mark it applied; with --page, also put that page in the group",
		Example: "slyb suggestion apply welcome rule 0",
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := strconv.Atoi(args[2])
			if err != nil || index < 0 {
				return cli.Rejected("the index is a number from 0", "")
			}
			ctx := cmd.Context()
			var suggestions *Suggestions
			var save func() error
			space := ""
			if args[0] == "welcome" {
				path, err := homeFile("welcome.json")
				if err != nil {
					return err
				}
				welcome := Welcome{}
				if err := readJSONFile(path, &welcome); err != nil {
					return cli.Rejected("the welcome page has no suggestions yet", "slyb welcome refresh")
				}
				suggestions = &welcome.Suggestions
				save = func() error { return writeJSONFile(path, welcome) }
			} else {
				store, err := GetSummaries()
				if err != nil {
					return err
				}
				for i := range store.Summaries {
					if store.Summaries[i].ID == args[0] {
						suggestions = &store.Summaries[i].Suggestions
						space = store.Summaries[i].Space
					}
				}
				if suggestions == nil {
					return cli.Rejected("no summary with id "+args[0], "read the ids with `slyb summary get`")
				}
				save = func() error { return PutSummaries(cmd, store) }
			}
			if space == "" {
				if space, err = activeSpace(ctx, ""); err != nil {
					return err
				}
			}
			summary := ""
			switch args[1] {
			case "group":
				if index >= len(suggestions.Groups) {
					return cli.Rejected("there is no suggested group "+args[2], "")
				}
				entry := &suggestions.Groups[index]
				store, err := GetGroups()
				if err != nil {
					return err
				}
				group := store.GroupTitled(space, entry.Title)
				if group == nil {
					group = store.PostGroup(space, entry.Title, true)
					group.Shelf = entry.Shelf
				}
				for _, page := range entry.Pages {
					group.Add("", KindSection, GroupPage{URL: page.URL, Title: page.Title, Source: SourceClaude, Verified: true})
				}
				if pageURL != "" {
					group.Add("", KindSection, GroupPage{URL: pageURL, Source: SourceYou, Verified: true})
				}
				if err := PutGroups(ctx, store); err != nil {
					return err
				}
				entry.Applied = true
				summary = "Made the group " + entry.Title
			case "space":
				if index >= len(suggestions.Spaces) {
					return cli.Rejected("there is no suggested space "+args[2], "")
				}
				entry := &suggestions.Spaces[index]
				if _, err := Call(ctx, "workspace.create", map[string]any{"name": entry.Name, "color": entry.Color}); err != nil {
					return err
				}
				entry.Applied = true
				summary = "Made the space " + entry.Name
			case "rule":
				if index >= len(suggestions.Rules) {
					return cli.Rejected("there is no suggested rule "+args[2], "")
				}
				entry := &suggestions.Rules[index]
				rules, err := GetRules()
				if err != nil {
					return err
				}
				var spaces []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				}
				if err := CallInto(ctx, "workspace.get", nil, &spaces); err != nil {
					return err
				}
				target := ""
				for _, known := range spaces {
					if strings.EqualFold(known.Name, entry.Space) {
						target = known.ID
					}
				}
				if target == "" {
					return cli.Rejected("there is no space named "+entry.Space, "make the space first")
				}
				rules.Rules = append(rules.Rules, Rule{ID: newID(), Name: entry.Name, Scope: ScopeSpace, Space: target, Script: entry.Script, CreatedAt: now()})
				if err := PutRules(rules); err != nil {
					return err
				}
				entry.Applied = true
				summary = "Added the rule " + entry.Name
			default:
				return cli.Rejected("the kind is group, space or rule", "")
			}
			if err := save(); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": summary, "kind": args[1], "index": index, "source": strings.TrimSpace(args[0])})
		},
	}
	apply.Flags().StringVar(&pageURL, "page", "", "With a group, also add this page to it")
	suggestion.AddCommand(apply)
	return suggestion
}

func groupTitles() ([]string, error) {
	store, err := GetGroups()
	if err != nil {
		return nil, err
	}
	titles := make([]string, 0, len(store.Groups))
	for _, group := range store.Groups {
		titles = append(titles, group.Title)
	}
	return titles, nil
}
