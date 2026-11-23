package slyb

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type PageRecord struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Site        string `json:"site,omitempty"`
	Type        string `json:"type,omitempty"`
	Canonical   string `json:"canonical,omitempty"`
	Keywords    string `json:"keywords,omitempty"`
	Heading     string `json:"heading,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
	Seen        int    `json:"seen"`
}

func GetPageRecords() (map[string]PageRecord, error) {
	path, err := homeFile("pages.json")
	if err != nil {
		return nil, err
	}
	records := map[string]PageRecord{}
	if err := readJSONFile(path, &records); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return records, nil
}

var codeHosts = map[string]bool{"github.com": true, "gitlab.com": true, "bitbucket.org": true}

func catalogPrefix(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	segments := strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
	if codeHosts[host] || strings.HasSuffix(host, ".ghe.com") {
		return strings.Join(append([]string{host}, segments[:min(2, len(segments))]...), "/")
	}
	return host
}

func underPrefix(raw, prefix string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	value := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.") + strings.TrimSuffix(parsed.Path, "/")
	prefix = strings.TrimSuffix(strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(prefix, "https://"), "http://")), "/")
	prefix = strings.TrimPrefix(prefix, "www.")
	return value == prefix || strings.HasPrefix(value, prefix+"/") || strings.HasSuffix(strings.SplitN(value, "/", 2)[0], "."+prefix)
}

type buildPage struct {
	URL    string
	Title  string
	Visits int
}

type buildGroup struct {
	Title    string   `json:"title"`
	Shelf    string   `json:"shelf"`
	Prefixes []string `json:"prefixes"`
	Repos    []string `json:"repos"`
	Sections []struct {
		Label    string   `json:"label"`
		Prefixes []string `json:"prefixes"`
	} `json:"sections"`
}

type buildPlan struct {
	Space  string       `json:"space"`
	Groups []buildGroup `json:"groups"`
}

const buildSchema = `{"type":"object","properties":{"space":{"type":"string"},"groups":{"type":"array","items":{"type":"object","properties":{"title":{"type":"string"},"shelf":{"type":"string"},"prefixes":{"type":"array","items":{"type":"string"}},"repos":{"type":"array","items":{"type":"string"}},"sections":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"prefixes":{"type":"array","items":{"type":"string"}}},"required":["label","prefixes"],"additionalProperties":false}}},"required":["title","shelf","prefixes","repos","sections"],"additionalProperties":false}}},"required":["space","groups"],"additionalProperties":false}`

type Built struct {
	Summary string       `json:"summary"`
	Plan    bool         `json:"plan"`
	Space   string       `json:"space"`
	Groups  []BuiltGroup `json:"groups"`
	Rules   []string     `json:"rules"`
	Request buildPlan    `json:"request"`
}

type BuiltGroup struct {
	Title    string         `json:"title"`
	Pages    int            `json:"pages"`
	Sections map[string]int `json:"sections"`
}

func NewPageCommand() *cobra.Command {
	page := &cobra.Command{Use: "page", Short: "The metadata the browser stores for every page it loads: title, description, site, type, heading, when and where it was seen"}
	var query, host string
	var limit int
	get := &cobra.Command{
		Use:     "get",
		Short:   "List stored pages, newest first, where every word of --query is in the url, title, description or site",
		Example: "slyb page get --host github.com --query acme",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			records, err := GetPageRecords()
			if err != nil {
				return err
			}
			words := strings.Fields(strings.ToLower(query))
			list := []PageRecord{}
			for _, record := range records {
				text := strings.ToLower(strings.Join([]string{record.URL, record.Title, record.Description, record.Site, record.Heading}, " "))
				match := host == "" || underPrefix(record.URL, host)
				for _, word := range words {
					match = match && strings.Contains(text, word)
				}
				if match {
					list = append(list, record)
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].LastSeen > list[j].LastSeen })
			return printValue(cmd, list[:min(len(list), limit)])
		},
	}
	get.Flags().StringVar(&query, "query", "", "Words that must all appear")
	get.Flags().StringVar(&host, "host", "", "Only pages of this host or address prefix")
	get.Flags().IntVar(&limit, "limit", 50, "Most rows to return")
	page.AddCommand(get)
	return page
}

func NewSpaceBuildCommand() *cobra.Command {
	var workspace, model string
	var plan bool
	cmd := &cobra.Command{
		Use:   "build <what the space should hold>",
		Short: "Build a space from a prompt, with groups and sections filled from the stored pages, history, Claude sessions and local repositories",
		Long: "Code gathers every page the browser stored, the browsing history, the pages the Claude sessions used and the local\n" +
			"repositories, and folds them into a compact catalog of sites and repositories. The assistant reads the catalog once\n" +
			"and answers with the space, its groups and sections as address prefixes and repository names. Code then fills each\n" +
			"group with every matching page, verified, and adds a rule per group so matching pages opened from outside land in it.",
		Example: "slyb space build \"a work space: one group for all the app listings per environment, one for github with all related repos\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			var err error
			if workspace, err = activeSpace(ctx, workspace); err != nil {
				return err
			}
			index, err := PostProjectSync(ctx)
			if err != nil {
				return err
			}
			records, err := GetPageRecords()
			if err != nil {
				return err
			}
			var history []Visit
			if err := CallInto(ctx, "history.get", map[string]any{"limit": 4000}, &history); err != nil {
				return err
			}
			pages := map[string]*buildPage{}
			add := func(raw, title string, visits int) {
				key := sortKey(raw)
				if key == "" || !strings.HasPrefix(key, "http") {
					return
				}
				entry := pages[key]
				if entry == nil {
					entry = &buildPage{URL: raw}
					pages[key] = entry
				}
				if entry.Title == "" {
					entry.Title = title
				}
				entry.Visits += visits
			}
			for _, record := range records {
				add(record.URL, record.Title, record.Seen)
			}
			for _, visit := range history {
				add(visit.URL, visit.Title, visit.Visits)
			}
			for _, project := range index.Projects {
				for _, page := range project.ClaudePages {
					add(page.URL, "", page.Sessions)
				}
				if project.Remote != "" {
					add(project.Remote, project.Name, 1)
				}
			}
			type site struct {
				prefix string
				pages  int
				visits int
				titles []string
			}
			sites := map[string]*site{}
			for _, page := range pages {
				prefix := catalogPrefix(page.URL)
				if prefix == "" {
					continue
				}
				entry := sites[prefix]
				if entry == nil {
					entry = &site{prefix: prefix}
					sites[prefix] = entry
				}
				entry.pages++
				entry.visits += page.Visits
				if len(entry.titles) < 3 && strings.TrimSpace(page.Title) != "" {
					entry.titles = append(entry.titles, strings.TrimSpace(page.Title))
				}
			}
			ranked := make([]*site, 0, len(sites))
			for _, entry := range sites {
				ranked = append(ranked, entry)
			}
			sort.Slice(ranked, func(i, j int) bool { return ranked[i].visits > ranked[j].visits })
			var lines []string
			for _, entry := range ranked[:min(len(ranked), 500)] {
				lines = append(lines, fmt.Sprintf("%s | %d pages | %d visits | %s", entry.prefix, entry.pages, entry.visits, strings.Join(entry.titles, "; ")))
			}
			var repos []string
			byName := map[string]Project{}
			for _, project := range index.Projects {
				byName[strings.ToLower(project.Name)] = project
				repos = append(repos, fmt.Sprintf("%s | %s | %s", project.Name, project.Remote, project.Path))
			}
			store, err := GetGroups()
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Plan one space of the user's browser sidebar. What they asked for: %s\n\n"+
				"The space holds groups, each group holds sections. Describe every group and section by the address prefixes it "+
				"covers, taken from the catalog, such as github.com/acme/widgets or app.tst.example.com, and list the "+
				"repositories that belong in a group by name. Include everything that belongs, leave out what does not. Use an "+
				"existing space name when one fits.\n\nThe sidebar now:\n%s\n\nSites, as prefix | pages | visits | sample titles:\n%s\n\n"+
				"Local repositories, as name | remote | folder:\n%s",
				args[0], sortOutline(store, workspace), strings.Join(lines, "\n"), strings.Join(repos, "\n"))
			var request buildPlan
			finish := step("build", "Planning the space")
			err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: buildSchema, Model: model}, &request)
			finish(err)
			if err != nil {
				return err
			}
			if strings.TrimSpace(request.Space) == "" || len(request.Groups) == 0 {
				return cli.Rejected("the plan has no space or no groups", "describe what the space should hold more concretely")
			}
			result := Built{Plan: plan, Space: request.Space, Groups: []BuiltGroup{}, Rules: []string{}, Request: request}
			space := store.SpaceNamed(workspace, request.Space)
			if space == nil {
				space = store.PostSpace(workspace, strings.TrimSpace(request.Space))
			}
			spaceID := space.ID
			rules, err := GetRules()
			if err != nil {
				return err
			}
			for _, planned := range request.Groups {
				var target *Group
				for i := range store.Groups {
					if store.Groups[i].SpaceID == spaceID && strings.EqualFold(store.Groups[i].Title, planned.Title) {
						target = &store.Groups[i]
					}
				}
				if target == nil {
					target = store.PostGroup(workspace, strings.TrimSpace(planned.Title), false)
					target.SpaceID = spaceID
					target.Shelf = planned.Shelf
				}
				prefixes := append([]string{}, planned.Prefixes...)
				for _, name := range planned.Repos {
					if project, ok := byName[strings.ToLower(name)]; ok && project.Remote != "" {
						prefixes = append(prefixes, project.Remote)
					}
				}
				var chosen []*buildPage
				for _, page := range pages {
					for _, prefix := range prefixes {
						if underPrefix(page.URL, prefix) {
							chosen = append(chosen, page)
							break
						}
					}
				}
				sort.Slice(chosen, func(i, j int) bool { return chosen[i].Visits > chosen[j].Visits })
				built := BuiltGroup{Title: target.Title, Sections: map[string]int{}}
				for _, page := range chosen {
					label := ""
					for _, section := range planned.Sections {
						for _, prefix := range section.Prefixes {
							if label == "" && underPrefix(page.URL, prefix) {
								label = section.Label
							}
						}
					}
					if target.Add(label, KindSection, GroupPage{URL: page.URL, Title: page.Title, Source: SourceClaude, Verified: true}) {
						built.Pages++
						built.Sections[label]++
					}
				}
				result.Groups = append(result.Groups, built)
				if len(prefixes) > 0 {
					quoted := make([]string, 0, len(prefixes))
					for _, prefix := range prefixes {
						clean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(prefix), "/"), "https://"), "http://")
						quoted = append(quoted, fmt.Sprintf("%q", strings.TrimPrefix(clean, "www.")))
					}
					script := "text := import(\"text\")\naddress := text.trim_prefix(text.trim_prefix(text.trim_prefix(url, \"https://\"), \"http://\"), \"www.\")\nfor prefix in [" + strings.Join(quoted, ", ") + "] {\n  if address == prefix || text.has_prefix(address, prefix + \"/\") || text.has_prefix(address, prefix + \"?\") { result = true }\n}"
					rule := Rule{ID: newID(), Name: target.Title + " pages", Scope: ScopeGroup, Space: workspace, Group: target.ID, Script: script, CreatedAt: now()}
					if _, err := runRule(ctx, rule, RuleInput{URL: "https://example.org/"}); err == nil {
						rules.Rules = append(rules.Rules, rule)
						result.Rules = append(result.Rules, rule.Name)
					}
				}
			}
			pagesTotal := 0
			for _, group := range result.Groups {
				pagesTotal += group.Pages
			}
			result.Summary = fmt.Sprintf("%s: %d groups, %d pages, %d rules", request.Space, len(result.Groups), pagesTotal, len(result.Rules))
			if plan {
				return printValue(cmd, result)
			}
			if err := PutGroups(ctx, store); err != nil {
				return err
			}
			if err := PutRules(rules); err != nil {
				return err
			}
			return printValue(cmd, result)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace id, the active workspace when empty")
	cmd.Flags().StringVar(&model, "model", "", "Claude model, the claude default when empty")
	cmd.Flags().BoolVar(&plan, "plan", false, "Print the space it would build and change nothing")
	return cmd
}
