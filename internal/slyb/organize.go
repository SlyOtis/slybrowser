package slyb

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/nimling/nimbody/respond"
	"github.com/nimling/nimbody/stream"
	"github.com/spf13/cobra"
)

const claudeGroupTitle = "Assistant"

type Tab struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	Space        *string `json:"space"`
	Folder       *string `json:"folder"`
	FolderLabel  *string `json:"folder_label"`
	Project      *string `json:"project"`
	Pinned       bool    `json:"pinned"`
	Essential    bool    `json:"essential"`
	Selected     bool    `json:"selected"`
	Loaded       bool    `json:"loaded"`
	LastAccessed string  `json:"last_accessed"`
	Login        string  `json:"login"`
}

type Folder struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Space *string  `json:"space"`
	Tabs  []string `json:"tabs"`
}

type Page struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type status struct {
	Space string `json:"space"`
}

type candidate struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Project string `json:"project,omitempty"`
}

type groupOutline struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Shelf    string   `json:"shelf"`
	Sections []string `json:"sections"`
	Sample   []string `json:"sample"`
}

type Placed struct {
	Group   string `json:"group"`
	Title   string `json:"title"`
	Section string `json:"section"`
	URL     string `json:"url"`
	New     bool   `json:"new"`
}

func readAll(cmd *cobra.Command) ([]byte, error) {
	return io.ReadAll(cmd.InOrStdin())
}

func step(id, label string) func(err error) {
	report := stream.Report(os.Stderr)
	started := time.Now()
	report(respond.Step{ID: id, Label: label, Status: "running", StartedAt: started})
	return func(err error) {
		ended := time.Now()
		done := respond.Step{ID: id, Label: label, Status: "done", StartedAt: started, EndedAt: &ended}
		if err != nil {
			done.Status = "failed"
			done.Error = err.Error()
		}
		report(done)
	}
}

func projectName(index ProjectIndex, id string) string {
	for _, project := range index.Projects {
		if project.ID == id {
			return project.Name
		}
	}
	return ""
}

func findingsPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "findings.md"), nil
}

func GetFindings() (string, error) {
	path, err := findingsPath()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(raw), err
}

func outlineGroups(store GroupStore, space string) []groupOutline {
	outlines := []groupOutline{}
	for _, group := range store.Groups {
		if group.Space != space || group.Title == claudeGroupTitle {
			continue
		}
		outline := groupOutline{ID: group.ID, Title: group.Title, Shelf: group.Shelf, Sections: []string{}, Sample: []string{}}
		for _, section := range group.Sections {
			if section.Label != "" {
				outline.Sections = append(outline.Sections, section.Label)
			}
			for _, page := range section.Pages {
				if len(outline.Sample) < 6 {
					outline.Sample = append(outline.Sample, page.Title)
				}
			}
		}
		outlines = append(outlines, outline)
	}
	return outlines
}

func NewOrganizeCommand() *cobra.Command {
	var space, model, groupID, section, instruction, pageURL, projectID string
	var withHistory bool
	var plan bool
	cmd := &cobra.Command{
		Use:   "organize",
		Short: "Sort the workspace's unsorted pages into spaces, groups and sections, discarding duplicates and steps on the way",
		Long: "First, in code: duplicates, error pages, pages that were only a step on the way to a deeper page of the same\n" +
			"site, and sign-ins that led on to a real page are discarded, recorded in the space and their tabs closed. Then the\n" +
			"rest go to claude as one line each with a compact outline of the spaces and groups, the rules, the findings and\n" +
			"how the user sorted before. Every page resolves into a space, then a group, then a section, as an unverified\n" +
			"page; new spaces and groups are made when nothing fits and spaces still called Space are named. Each placement is\n" +
			"recorded in its space's history. --plan prints all of it and changes nothing. With --group, claude rearranges that\n" +
			"one group into sections; with --page, only that page is sorted.",
		Example: "slyb organize --plan",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			index, err := PostProjectSync(cmd.Context())
			if err != nil {
				return err
			}
			if _, err := Call(ctx, "project.reload", nil); err != nil {
				return err
			}
			if space, err = activeSpace(ctx, space); err != nil {
				return err
			}
			if projectID != "" {
				if groupID, err = postProjectGroup(cmd, index, projectID, space); err != nil {
					return err
				}
			}
			if groupID != "" {
				return postOrganizeGroup(cmd, index, groupID, section, instruction, model, plan, withHistory)
			}
			return postSortSpace(cmd, index, space, instruction, pageURL, model, plan, withHistory)
		},
	}
	cmd.Flags().StringVar(&space, "workspace", "", "Workspace id, the active workspace when empty")
	cmd.Flags().StringVar(&groupID, "group", "", "Organize this one group: its sections and the loose tabs that belong in it")
	cmd.Flags().StringVar(&section, "section", "", "With --group, organize only this section path, such as Docs / Api")
	cmd.Flags().StringVar(&projectID, "project", "", "Organize this project as a group: the first time makes the group, with the pages Claude used in it")
	cmd.Flags().StringVar(&pageURL, "page", "", "Place only this page, by the sorting you did before, the rules, the findings and what you are working on now")
	cmd.Flags().StringVar(&instruction, "prompt", "", "How to sort or relate the pages or groups, such as \"by client, newest work first\"; it comes before the organization rules")
	cmd.Flags().BoolVar(&withHistory, "history", false, "Also place pages visited more than once that no group holds")
	cmd.Flags().StringVar(&model, "model", "", "Claude model, the claude default when empty")
	cmd.Flags().BoolVar(&plan, "plan", false, "Print the placements and change nothing")
	return cmd
}

type askGroup struct {
	Title    string       `json:"title"`
	Shelf    string       `json:"shelf"`
	Sections []askSection `json:"sections"`
}

type askSection struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Pages []Page `json:"pages"`
}

const askGroupSchema = `{"type":"object","properties":{"title":{"type":"string"},"shelf":{"type":"string"},"sections":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"kind":{"type":"string","enum":["section","divider"]},"pages":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string"},"title":{"type":"string"}},"required":["url","title"],"additionalProperties":false}}},"required":["label","kind","pages"],"additionalProperties":false}}},"required":["title","sections"],"additionalProperties":false}`

func postGroupAsk(cmd *cobra.Command, space, ask, model string) error {
	ctx := cmd.Context()
	known := map[string]Page{}
	addKnown := func(url, title string) {
		if !strings.HasPrefix(url, "http") {
			return
		}
		if existing, ok := known[url]; !ok || existing.Title == "" {
			known[url] = Page{URL: url, Title: title}
		}
	}
	var tabs []Tab
	if err := CallInto(ctx, "tab.get", map[string]any{}, &tabs); err != nil {
		return err
	}
	for _, tab := range tabs {
		addKnown(tab.URL, tab.Title)
	}
	var history []Visit
	if err := CallInto(ctx, "history.get", map[string]any{"limit": 3000}, &history); err != nil {
		return err
	}
	for _, visit := range history {
		addKnown(visit.URL, visit.Title)
	}
	if index, err := GetProjectIndex(); err == nil {
		for _, project := range index.Projects {
			for _, page := range project.ClaudePages {
				addKnown(page.URL, "")
			}
		}
	}
	store, err := GetGroups()
	if err != nil {
		return err
	}
	for _, group := range store.Groups {
		for _, section := range group.Sections {
			for _, page := range section.Pages {
				addKnown(page.URL, page.Title)
			}
		}
	}
	findings, err := GetFindings()
	if err != nil {
		return err
	}
	pages := make([]Page, 0, len(known))
	for _, page := range known {
		pages = append(pages, page)
	}
	pagesJSON, _ := json.Marshal(pages)
	shelves := map[string]bool{}
	for _, group := range store.Groups {
		if group.Shelf != "" {
			shelves[group.Shelf] = true
		}
	}
	shelvesJSON, _ := json.Marshal(shelves)
	prompt := fmt.Sprintf("Build one browser group from the user's pages.\n\nWhat the group should hold: %s\n\nExisting shelves: %s\n\n"+
		"Findings about the user's pages:\n%s\n\nEvery page the user has open, visited, bookmarked or that Claude Code used: %s\n\n"+
		"Give the group a short title, a shelf when one of the existing shelves fits or the request names one, and split it into labeled sections, one per part the request names, such as one per "+
		"environment. Use kind divider only for a plain sub folder. Pick only urls from the list, each once, and give each a "+
		"short readable title.", ask, shelvesJSON, findings, pagesJSON)
	var built askGroup
	finish := step("claude", "Claude builds the group")
	err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: askGroupSchema, Model: model}, &built)
	finish(err)
	if err != nil {
		return err
	}
	target := store.GroupTitled(space, built.Title)
	if target == nil {
		target = store.PostGroup(space, built.Title, true)
		target.Shelf = built.Shelf
	}
	added := 0
	for _, section := range built.Sections {
		kind := section.Kind
		if kind != KindDivider {
			kind = KindSection
		}
		for _, page := range section.Pages {
			if _, ok := known[page.URL]; !ok {
				continue
			}
			if target.Add(section.Label, kind, GroupPage{URL: page.URL, Title: page.Title, Source: SourceClaude, Verified: true}) {
				added++
			}
		}
	}
	summary := summarizeGroup(*target)
	if err := PutGroups(ctx, store); err != nil {
		return err
	}
	return printValue(cmd, map[string]any{"summary": fmt.Sprintf("%s: %d pages in %d sections", target.Title, added, len(target.Sections)), "group": summary})
}

func NewFindingCommand() *cobra.Command {
	finding := &cobra.Command{Use: "finding", Short: "The notes Claude keeps about the user's pages, read by every organize and group build"}
	finding.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Print the findings",
		Example: "slyb finding get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			findings, err := GetFindings()
			if err != nil {
				return err
			}
			path, _ := findingsPath()
			return printValue(cmd, map[string]string{"path": path, "findings": findings})
		},
	})
	finding.AddCommand(&cobra.Command{
		Use:     "create <text>",
		Short:   "Note a finding, such as which hosts are an environment of a service",
		Example: "slyb finding create \"tst runs on *.tst.example.com, prd on *.example.com\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return cli.Rejected("the finding is empty", "")
			}
			findings, err := GetFindings()
			if err != nil {
				return err
			}
			count := 0
			for _, line := range strings.Split(findings, "\n") {
				if line != "" && line[0] >= '0' && line[0] <= '9' {
					count++
				}
			}
			if findings == "" {
				findings = "# Findings\n\n"
			}
			findings = strings.TrimRight(findings, "\n") + fmt.Sprintf("\n%d. %s\n", count+1, strings.TrimSpace(args[0]))
			path, err := findingsPath()
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(findings), 0o600); err != nil {
				return err
			}
			return printValue(cmd, map[string]string{"path": path, "findings": findings})
		},
	})
	return finding
}

type groupArrangement struct {
	Sections []arrangedSection `json:"sections"`
	Add      []arrangedTab     `json:"add"`
}

type arrangedSection struct {
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	URLs  []string `json:"urls"`
}

type arrangedTab struct {
	TabID   string `json:"tab_id"`
	Section string `json:"section"`
}

const groupArrangementSchema = `{"type":"object","properties":{"sections":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"kind":{"type":"string","enum":["section","divider"]},"urls":{"type":"array","items":{"type":"string"}}},"required":["label","kind","urls"],"additionalProperties":false}},"add":{"type":"array","items":{"type":"object","properties":{"tab_id":{"type":"string"},"section":{"type":"string"}},"required":["tab_id","section"],"additionalProperties":false}}},"required":["sections","add"],"additionalProperties":false}`

func historyCandidates(cmd *cobra.Command, store GroupStore, taken map[string]bool) ([]Page, error) {
	var history []Visit
	if err := CallInto(cmd.Context(), "history.get", map[string]any{"limit": 5000}, &history); err != nil {
		return nil, err
	}
	var pages []Page
	for _, visit := range history {
		if visit.Visits >= 2 && strings.HasPrefix(visit.URL, "http") && !store.Contains(visit.URL) && !taken[visit.URL] {
			taken[visit.URL] = true
			pages = append(pages, Page{URL: visit.URL, Title: visit.Title})
		}
	}
	return pages, nil
}

func inSection(label, section string) bool {
	return section == "" || label == section || strings.HasPrefix(label, section+" / ")
}

func underSection(label, section string) string {
	switch {
	case section == "":
		return label
	case label == "":
		return section
	case inSection(label, section):
		return label
	default:
		return section + " / " + label
	}
}

func postOrganizeGroup(cmd *cobra.Command, index ProjectIndex, groupID, scope, instruction, model string, plan, withHistory bool) error {
	ctx := cmd.Context()
	store, err := GetGroups()
	if err != nil {
		return err
	}
	group, err := store.Group(groupID)
	if err != nil {
		return err
	}
	var tabs []Tab
	if err := CallInto(ctx, "tab.get", map[string]any{"space": group.Space}, &tabs); err != nil {
		return err
	}
	organization, err := GetOrganization(200)
	if err != nil {
		return err
	}
	findings, err := GetFindings()
	if err != nil {
		return err
	}
	pages := map[string]GroupPage{}
	type listed struct {
		URL     string `json:"url"`
		Title   string `json:"title"`
		Section string `json:"section"`
		Kind    string `json:"kind"`
	}
	var current []listed
	for _, section := range group.Sections {
		if !inSection(section.Label, scope) {
			continue
		}
		for _, page := range section.Pages {
			pages[page.URL] = page
			current = append(current, listed{URL: page.URL, Title: page.Title, Section: section.Label, Kind: section.Kind})
		}
	}
	byID := map[string]Page{}
	taken := map[string]bool{}
	var loose []candidate
	for _, tab := range tabs {
		if tab.Essential || !strings.HasPrefix(tab.URL, "http") || store.Contains(tab.URL) {
			continue
		}
		byID[tab.ID] = Page{URL: tab.URL, Title: tab.Title}
		taken[tab.URL] = true
		entry := candidate{ID: tab.ID, Title: tab.Title, URL: tab.URL}
		if tab.Project != nil {
			entry.Project = projectName(index, *tab.Project)
		}
		loose = append(loose, entry)
	}
	if withHistory {
		visited, err := historyCandidates(cmd, store, taken)
		if err != nil {
			return err
		}
		for i, page := range visited {
			id := fmt.Sprintf("h%d", i)
			byID[id] = page
			loose = append(loose, candidate{ID: id, Title: page.Title, URL: page.URL})
		}
	}
	title := group.Title
	if scope != "" {
		title = group.Title + " / " + scope
	}
	currentJSON, _ := json.Marshal(current)
	looseJSON, _ := json.Marshal(loose)
	learnedJSON, _ := json.Marshal(organization.Learned)
	prompt := fmt.Sprintf("Organize the user's browser group or section %q.\n\nOrganization rules:\n%s\n\nFindings about the user's pages:\n%s\n\n"+
		"Pages the user kept or dismissed, newest last:\n%s\n\nThe group's pages with their current section or divider: %s\n\n"+
		"Loose open tabs, and pages the user visited more than once, that no group holds: %s\n\n"+
		"%s"+
		"Arrange every page of the group into sections, with short labels, such as one per environment, project or kind "+
		"of page. Keep a label the user already uses when it still fits. Use kind divider for a plain sub folder and kind "+
		"section for a labeled part. List each url once, in the order it should show. Then give the loose tabs that belong "+
		"in this group, each with the section it goes in; leave out tabs that do not belong.",
		title, organization.Rules, findings, learnedJSON, currentJSON, looseJSON, instructionText(instruction))
	var arranged groupArrangement
	finish := step("organize", "Organizing "+title)
	err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: groupArrangementSchema, Model: model}, &arranged)
	finish(err)
	if err != nil {
		return err
	}
	placed := map[string]bool{}
	var sections []GroupSection
	for _, entry := range arranged.Sections {
		kind := entry.Kind
		if kind != KindDivider {
			kind = KindSection
		}
		section := GroupSection{ID: newID(), Label: underSection(entry.Label, scope), Kind: kind, Pages: []GroupPage{}}
		for _, url := range entry.URLs {
			page, ok := pages[url]
			if !ok || placed[url] {
				continue
			}
			placed[url] = true
			section.Pages = append(section.Pages, page)
		}
		sections = append(sections, section)
	}
	var outside []GroupSection
	for _, section := range group.Sections {
		if !inSection(section.Label, scope) {
			outside = append(outside, section)
			continue
		}
		var kept []GroupPage
		for _, page := range section.Pages {
			if !placed[page.URL] {
				kept = append(kept, page)
				placed[page.URL] = true
			}
		}
		if len(kept) > 0 {
			sections = append(sections, GroupSection{ID: section.ID, Label: section.Label, Kind: section.Kind, Pages: kept})
		}
	}
	group.Sections = append(outside, sections...)
	added := 0
	for _, entry := range arranged.Add {
		tab, ok := byID[entry.TabID]
		if !ok {
			continue
		}
		delete(byID, entry.TabID)
		if group.Add(underSection(entry.Section, scope), KindSection, GroupPage{URL: tab.URL, Title: tab.Title, Source: SourceClaude, Verified: false}) {
			added++
		}
	}
	result := map[string]any{
		"summary": fmt.Sprintf("%s: %d sections, %d pages added", title, len(sections), added),
		"plan":    plan,
		"group":   summarizeGroup(*group),
	}
	if plan {
		result["sections"] = group.Sections
		return printValue(cmd, result)
	}
	if err := PutGroups(ctx, store); err != nil {
		return err
	}
	return printValue(cmd, result)
}

const spaceArrangementSchema = `{"type":"object","properties":{"groups":{"type":"array","items":{"type":"object","properties":{"group_id":{"type":"string"},"shelf":{"type":"string"}},"required":["group_id","shelf"],"additionalProperties":false}}},"required":["groups"],"additionalProperties":false}`

type spaceArrangement struct {
	Groups []struct {
		GroupID string `json:"group_id"`
		Shelf   string `json:"shelf"`
	} `json:"groups"`
}

func instructionText(instruction string) string {
	if strings.TrimSpace(instruction) == "" {
		return ""
	}
	return fmt.Sprintf("The user asked for this, and it comes before the organization rules: %s\n\n", strings.TrimSpace(instruction))
}

func postArrangeSpace(cmd *cobra.Command, store *GroupStore, space, instruction, model string) (int, error) {
	outlines := outlineGroups(*store, space)
	if len(outlines) == 0 {
		return 0, nil
	}
	outlinesJSON, _ := json.Marshal(outlines)
	prompt := fmt.Sprintf("Arrange the groups of the user's browser space.\n\n%sGroups, with their shelf, section labels and a sample of their pages: %s\n\n"+
		"Give every group_id once, in the order the groups should show, each with the shelf it belongs on: a short label that "+
		"relates the groups on it, an existing shelf when it still fits, or empty for no shelf.", instructionText(instruction), outlinesJSON)
	var arranged spaceArrangement
	finish := step("organize", fmt.Sprintf("Arranging %d groups", len(outlines)))
	err := PostClaude(cmd.Context(), ClaudeCall{Prompt: prompt, Schema: spaceArrangementSchema, Model: model}, &arranged)
	finish(err)
	if err != nil {
		return 0, err
	}
	rank := map[string]int{}
	shelves := map[string]string{}
	for i, entry := range arranged.Groups {
		if _, seen := rank[entry.GroupID]; !seen {
			rank[entry.GroupID] = i
			shelves[entry.GroupID] = strings.TrimSpace(entry.Shelf)
		}
	}
	var slots []int
	var inSpace []Group
	for i, group := range store.Groups {
		if group.Space == space {
			slots = append(slots, i)
			inSpace = append(inSpace, group)
		}
	}
	position := func(group Group) int {
		if at, ok := rank[group.ID]; ok {
			return at
		}
		return len(arranged.Groups)
	}
	sort.SliceStable(inSpace, func(a, b int) bool { return position(inSpace[a]) < position(inSpace[b]) })
	for i, slot := range slots {
		group := inSpace[i]
		if shelf, ok := shelves[group.ID]; ok {
			group.Shelf = shelf
		}
		store.Groups[slot] = group
	}
	return len(rank), nil
}

func postProjectGroup(cmd *cobra.Command, index ProjectIndex, projectID, space string) (string, error) {
	store, err := GetGroups()
	if err != nil {
		return "", err
	}
	for _, group := range store.Groups {
		if group.Project == projectID {
			return group.ID, nil
		}
	}
	for _, project := range index.Projects {
		if project.ID != projectID {
			continue
		}
		if project.Space != "" {
			space = project.Space
		}
		group := store.PostGroup(space, project.Name, false)
		group.Project = project.ID
		for _, page := range project.ClaudePages {
			group.Add("", KindSection, GroupPage{URL: page.URL, Source: SourceClaude, Verified: true})
		}
		id := group.ID
		if err := PutGroups(cmd.Context(), store); err != nil {
			return "", err
		}
		return id, nil
	}
	return "", cli.Rejected("no project "+projectID, "read them with `slyb project get`")
}
