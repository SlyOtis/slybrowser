package slyb

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type sortCandidate struct {
	ID      string
	Title   string
	URL     string
	Project string
	Tab     bool
	At      string
	Login   string
}

type Discarded struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
	Kept   string `json:"kept,omitempty"`
	Tab    string `json:"tab,omitempty"`
}

type Sorted struct {
	Summary   string      `json:"summary"`
	Plan      bool        `json:"plan"`
	Placed    []Placed    `json:"placed"`
	Discarded []Discarded `json:"discarded"`
	Spaces    []string    `json:"new_spaces"`
	Renamed   []string    `json:"renamed_spaces"`
	Frequent  []string    `json:"frequent"`
}

type sortPlacement struct {
	ID      string `json:"id"`
	Space   string `json:"space"`
	Group   string `json:"group"`
	Section string `json:"section"`
}

type sortReply struct {
	Placements []sortPlacement `json:"placements"`
	Renames    []struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"renames"`
}

const sortSchema = `{"type":"object","properties":{"placements":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"space":{"type":"string"},"group":{"type":"string"},"section":{"type":"string"}},"required":["id","space","group","section"],"additionalProperties":false}},"renames":{"type":"array","items":{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"}},"required":["from","to"],"additionalProperties":false}}},"required":["placements","renames"],"additionalProperties":false}`

var (
	errorTitle  = regexp.MustCompile(`(?i)^(404|page not found|not found|error|this page isn.t available)\b|\b404\b`)
	authSignal  = regexp.MustCompile(`(?i)(^|[/._-])(login|log-in|signin|sign-in|sign_in|oauth2?|authorize|callback|sso|saml|auth)([/._?-]|$)|^(sign in|log in|logg inn)\b`)
	noiseParams = regexp.MustCompile(`^(utm_|fbclid$|gclid$|mc_|ref$|ref_src$)`)
)

func sortKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		if noiseParams.MatchString(key) {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Host = strings.TrimPrefix(strings.ToLower(parsed.Host), "www.")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return parsed.String()
}

func pathSegments(raw string) (string, []string) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", nil
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Host), "www."), strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' })
}

func discardPages(candidates []sortCandidate) ([]sortCandidate, []Discarded) {
	var kept []sortCandidate
	var discarded []Discarded
	seen := map[string]sortCandidate{}
	drop := func(item sortCandidate, reason, keep string) {
		entry := Discarded{URL: item.URL, Title: item.Title, Reason: reason, Kept: keep}
		if item.Tab {
			entry.Tab = item.ID
		}
		discarded = append(discarded, entry)
	}
	for _, item := range candidates {
		key := sortKey(item.URL)
		if first, ok := seen[key]; ok {
			drop(item, "duplicate", first.URL)
			continue
		}
		seen[key] = item
		if errorTitle.MatchString(item.Title) {
			drop(item, "error page", "")
			continue
		}
		kept = append(kept, item)
	}
	bySite := map[string][]sortCandidate{}
	for _, item := range kept {
		host, _ := pathSegments(item.URL)
		labels := strings.Split(host, ".")
		site := strings.Join(labels[max(0, len(labels)-2):], ".")
		bySite[site] = append(bySite[site], item)
	}
	gone := map[string]string{}
	for _, items := range bySite {
		for _, item := range items {
			host, segments := pathSegments(item.URL)
			for _, other := range items {
				if other.ID == item.ID {
					continue
				}
				otherHost, deeper := pathSegments(other.URL)
				if otherHost == host && len(deeper) > len(segments) && strings.HasPrefix(strings.Join(deeper, "/")+"/", strings.Join(segments, "/")+"/") {
					gone[item.ID] = "a step on the way to a deeper page of " + host + "|" + other.URL
					break
				}
				if signIn(item) {
					if !signIn(other) {
						gone[item.ID] = "a sign-in that led on|" + other.URL
						break
					}
				}
			}
		}
	}
	var final []sortCandidate
	for _, item := range kept {
		if why, ok := gone[item.ID]; ok {
			reason, keep, _ := strings.Cut(why, "|")
			drop(item, reason, keep)
			continue
		}
		final = append(final, item)
	}
	return final, discarded
}

func sortOutline(store GroupStore, workspace string) string {
	var lines []string
	for _, space := range store.Spaces {
		if space.Workspace != workspace {
			continue
		}
		lines = append(lines, "space: "+space.Name)
		for _, group := range store.Groups {
			if group.SpaceID != space.ID || group.Title == claudeGroupTitle {
				continue
			}
			var sections []string
			for _, section := range group.Sections {
				if section.Label != "" {
					sections = append(sections, section.Label)
				}
			}
			line := "  group: " + group.Title
			if len(sections) > 0 {
				line += " (sections: " + strings.Join(sections, ", ") + ")"
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func trimURL(raw string) string {
	value := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	value = strings.TrimPrefix(value, "www.")
	if len(value) > 90 {
		value = value[:90]
	}
	return value
}

func postSortSpace(cmd *cobra.Command, index ProjectIndex, workspace, instruction, pageURL, model string, plan, withHistory bool) error {
	ctx := cmd.Context()
	var tabs []Tab
	if err := CallInto(ctx, "tab.get", map[string]any{"space": workspace}, &tabs); err != nil {
		return err
	}
	store, err := GetGroups()
	if err != nil {
		return err
	}
	if strings.TrimSpace(instruction) != "" && pageURL == "" {
		if _, err := postArrangeSpace(cmd, &store, workspace, instruction, model); err != nil {
			return err
		}
	}
	organization, err := GetOrganization(60)
	if err != nil {
		return err
	}
	findings, err := GetFindings()
	if err != nil {
		return err
	}
	var candidates []sortCandidate
	taken := map[string]bool{}
	for _, tab := range tabs {
		if tab.Essential || tab.Pinned || !strings.HasPrefix(tab.URL, "http") || store.Contains(tab.URL) || (pageURL != "" && tab.URL != pageURL) {
			continue
		}
		taken[tab.URL] = true
		item := sortCandidate{ID: tab.ID, Title: tab.Title, URL: tab.URL, Tab: true, At: tab.LastAccessed, Login: tab.Login}
		if tab.Project != nil {
			item.Project = projectName(index, *tab.Project)
		}
		candidates = append(candidates, item)
	}
	if pageURL != "" && len(candidates) == 0 && !store.Contains(pageURL) {
		candidates = append(candidates, sortCandidate{ID: "p0", URL: pageURL})
	}
	if withHistory && pageURL == "" {
		visited, err := historyCandidates(cmd, store, taken)
		if err != nil {
			return err
		}
		for i, page := range visited {
			candidates = append(candidates, sortCandidate{ID: fmt.Sprintf("h%d", i), Title: page.Title, URL: page.URL})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].At > candidates[j].At })
	kept, discarded := discardPages(candidates)
	result := Sorted{Plan: plan, Placed: []Placed{}, Discarded: discarded, Spaces: []string{}, Renamed: []string{}, Frequent: []string{}}
	if pageURL != "" {
		kept, result.Discarded = candidates, []Discarded{}
	}
	var unsorted []sortCandidate
	for _, item := range kept {
		if item.Login == "" {
			unsorted = append(unsorted, item)
			continue
		}
		var target *Group
		for i := range store.Groups {
			if store.Groups[i].Space == workspace && store.Groups[i].Login == item.Login {
				target = &store.Groups[i]
			}
		}
		isNew := target == nil
		if isNew {
			target = store.PostGroup(workspace, item.Login, false)
			target.Login = item.Login
		}
		target.Add("", KindSection, GroupPage{URL: item.URL, Title: item.Title, Source: SourceClaude, Verified: false})
		result.Placed = append(result.Placed, Placed{Group: target.ID, Title: target.Title, URL: item.URL, New: isNew})
		if record := store.SpaceOf(target.SpaceID); record != nil {
			record.History = append(record.History, SortRecord{At: now(), URL: item.URL, Title: item.Title, Group: target.Title, Reason: "same login"})
		}
	}
	kept = unsorted
	byID := map[string]sortCandidate{}
	if len(kept) > 0 {
		var lines []string
		for _, item := range kept {
			byID[item.ID] = item
			line := fmt.Sprintf("%s | %s | %s", item.ID, strings.TrimSpace(item.Title), trimURL(item.URL))
			if item.Project != "" {
				line += " | project " + item.Project
			}
			lines = append(lines, line)
		}
		learned := make([]string, 0, len(organization.Learned))
		for _, entry := range organization.Learned {
			learned = append(learned, fmt.Sprintf("%s %s → %s / %s", entry.Action, trimURL(entry.URL), entry.Group, entry.Section))
		}
		prompt := fmt.Sprintf("Sort browser pages into the sidebar. It has spaces, each space holds groups, each group holds sections.\n\n"+
			"%sRules:\n%s\n\nFacts about the user's sites:\n%s\n\nHow the user sorted before, newest last:\n%s\n\nThe sidebar now:\n%s\n\n"+
			"Pages, one per line as id | title | address | project:\n%s\n\n"+
			"Place every page. For each give its id, then the space, the group and the section it belongs in, in that order of "+
			"importance: first the most meaningful space, then the group, then a short section label such as an environment, or an "+
			"empty section. Use the exact name of an existing space or group when one fits; otherwise name a new one in one to three "+
			"words. A new space only for a clearly separate area of life or work. Spaces still called Space get a real name: list "+
			"them in renames with the name their groups suggest.",
			instructionText(instruction), organization.Rules, findings, strings.Join(learned, "\n"), sortOutline(store, workspace), strings.Join(lines, "\n"))
		var reply sortReply
		finish := step("organize", fmt.Sprintf("Sorting %d pages", len(kept)))
		err := PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: sortSchema, Model: model}, &reply)
		finish(err)
		if err != nil {
			return err
		}
		renamed := map[string]string{}
		for _, rename := range reply.Renames {
			if space := store.SpaceNamed(workspace, rename.From); space != nil && strings.TrimSpace(rename.To) != "" && store.SpaceNamed(workspace, rename.To) == nil {
				space.Name = strings.TrimSpace(rename.To)
				renamed[strings.ToLower(rename.From)] = space.Name
				result.Renamed = append(result.Renamed, rename.From+" → "+space.Name)
			}
		}
		for _, entry := range reply.Placements {
			item, ok := byID[entry.ID]
			if !ok || strings.TrimSpace(entry.Space) == "" || strings.TrimSpace(entry.Group) == "" {
				continue
			}
			delete(byID, entry.ID)
			if to, ok := renamed[strings.ToLower(strings.TrimSpace(entry.Space))]; ok {
				entry.Space = to
			}
			space := store.SpaceNamed(workspace, entry.Space)
			if space == nil {
				space = store.PostSpace(workspace, strings.TrimSpace(entry.Space))
				result.Spaces = append(result.Spaces, space.Name)
			}
			spaceID := space.ID
			var target *Group
			for i := range store.Groups {
				if store.Groups[i].Space == workspace && store.Groups[i].SpaceID == spaceID && strings.EqualFold(store.Groups[i].Title, entry.Group) {
					target = &store.Groups[i]
				}
			}
			isNew := target == nil
			if isNew {
				target = store.PostGroup(workspace, strings.TrimSpace(entry.Group), true)
				target.SpaceID = spaceID
			}
			target.Add(entry.Section, KindSection, GroupPage{URL: item.URL, Title: item.Title, Source: SourceClaude, Verified: false})
			result.Placed = append(result.Placed, Placed{Group: target.ID, Title: target.Title, Section: entry.Section, URL: item.URL, New: isNew})
			record := store.SpaceOf(spaceID)
			record.History = append(record.History, SortRecord{At: now(), URL: item.URL, Title: item.Title, Group: target.Title, Section: entry.Section})
		}
	}
	for _, entry := range result.Discarded {
		home := store.DefaultSpace(workspace)
		for _, placed := range result.Placed {
			if placed.URL == entry.Kept {
				if group, err := store.Group(placed.Group); err == nil {
					home = store.SpaceOf(group.SpaceID)
				}
			}
		}
		home.Discarded = append(home.Discarded, SortRecord{At: now(), URL: entry.URL, Title: entry.Title, Reason: entry.Reason})
	}
	open := map[string]bool{}
	for _, tab := range tabs {
		open[normalURL(tab.URL)] = true
	}
	claude := store.GroupTitled(workspace, claudeGroupTitle)
	for _, entry := range index.Frequent {
		if pageURL != "" || open[entry.URL] || store.Contains(entry.URL) {
			continue
		}
		if claude == nil {
			claude = store.PostGroup(workspace, claudeGroupTitle, true)
		}
		if claude.Add("Frequent", KindSection, GroupPage{URL: entry.URL, Source: SourceClaude, Verified: false}) {
			result.Frequent = append(result.Frequent, entry.URL)
		}
	}
	result.Summary = fmt.Sprintf("%d pages placed, %d discarded, %d new spaces", len(result.Placed), len(result.Discarded), len(result.Spaces))
	if plan {
		return printValue(cmd, result)
	}
	if err := PutGroups(ctx, store); err != nil {
		return err
	}
	var close []string
	for _, entry := range result.Discarded {
		if entry.Tab != "" {
			close = append(close, entry.Tab)
		}
	}
	if len(close) > 0 {
		if _, err := Call(ctx, "tab.delete", map[string]any{"ids": close}); err != nil {
			return err
		}
	}
	if pageURL == "" {
		unplaced := []any{}
		for _, item := range byID {
			if item.Tab {
				unplaced = append(unplaced, map[string]any{"url": item.URL, "title": item.Title})
			}
		}
		if _, err := PostTrigger(ctx, TriggerCall{Trigger: TriggerOrganizeDone, Data: map[string]any{"workspace": workspace, "summary": result.Summary, "placed": len(result.Placed), "discarded": len(result.Discarded), "unplaced": unplaced}}); err != nil {
			return err
		}
	}
	return printValue(cmd, result)
}

func signIn(item sortCandidate) bool {
	parsed, err := url.Parse(item.URL)
	if err != nil {
		return false
	}
	return authSignal.MatchString(parsed.RequestURI()) || authSignal.MatchString(item.Title)
}
