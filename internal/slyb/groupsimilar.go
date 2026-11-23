package slyb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type CachedPage struct {
	URL      string          `json:"url"`
	Title    string          `json:"title"`
	Section  string          `json:"section,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	ReadAt   string          `json:"read_at,omitempty"`
}

type GroupCache struct {
	Group string       `json:"group"`
	Title string       `json:"title"`
	Pages []CachedPage `json:"pages"`
}

type similarReply struct {
	Pages []similarPage `json:"pages"`
}

type similarPage struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type Similar struct {
	Summary string        `json:"summary"`
	Group   string        `json:"group"`
	Cache   string        `json:"cache"`
	Pages   []similarPage `json:"pages"`
}

type tabMetadata struct {
	Tab
	Metadata json.RawMessage `json:"metadata"`
}

const similarSchema = `{"type":"object","properties":{"pages":{"type":"array","items":{"type":"object","properties":{"url":{"type":"string"},"title":{"type":"string"},"reason":{"type":"string"}},"required":["url","title","reason"],"additionalProperties":false}}},"required":["pages"],"additionalProperties":false}`

func NewGroupSimilarCommand() *cobra.Command {
	var model, scope string
	var count int
	cmd := &cobra.Command{
		Use:   "similar <group_id>",
		Short: "Find pages like a group's among the open tabs and the pages visited more than once, and add them as unverified",
		Long: "Read the metadata of the group's open, loaded pages into ~/.slyb/cache/groups/<group_id>/pages.json, which keeps\n" +
			"what earlier runs read, then pick, from the open tabs and the history pages visited at least twice that no\n" +
			"group holds, the ones that belong with the group. Nothing is searched on the web. The picks land in the\n" +
			"group's Similar section as unverified pages. The Find similar button on a card runs this.",
		Example:     "slyb group similar 3f9a2c1b7d4e",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			store, err := GetGroups()
			if err != nil {
				return err
			}
			group, err := store.Group(args[0])
			if err != nil {
				return err
			}
			var tabs []Tab
			if err := CallInto(ctx, "tab.get", map[string]any{}, &tabs); err != nil {
				return err
			}
			loaded := map[string]Tab{}
			for _, tab := range tabs {
				if tab.Loaded {
					loaded[tab.URL] = tab
				}
			}
			home, err := Home()
			if err != nil {
				return err
			}
			dir := filepath.Join(home, "cache", "groups", group.ID)
			cachePath := filepath.Join(dir, "pages.json")
			cache := GroupCache{}
			if err := readJSONFile(cachePath, &cache); err != nil && !os.IsNotExist(err) {
				return err
			}
			known := map[string]CachedPage{}
			for _, page := range cache.Pages {
				known[page.URL] = page
			}
			finish := step("metadata", "Reading the pages of "+group.Title)
			var pages []CachedPage
			for _, section := range group.Sections {
				if !inSection(section.Label, scope) {
					continue
				}
				for _, page := range section.Pages {
					cached := known[page.URL]
					cached.URL, cached.Title, cached.Section = page.URL, page.Title, section.Label
					if tab, ok := loaded[page.URL]; ok {
						var read tabMetadata
						if CallInto(ctx, "tab.metadata", map[string]any{"id": tab.ID}, &read) == nil {
							cached.Metadata, cached.ReadAt = read.Metadata, time.Now().UTC().Format(time.RFC3339)
						}
					}
					pages = append(pages, cached)
				}
			}
			finish(nil)
			if err := writeJSONFile(cachePath, GroupCache{Group: group.ID, Title: group.Title, Pages: pages}); err != nil {
				return err
			}
			var history []Visit
			if err := CallInto(ctx, "history.get", map[string]any{"limit": 5000}, &history); err != nil {
				return err
			}
			candidates := map[string]Page{}
			for _, tab := range tabs {
				if strings.HasPrefix(tab.URL, "http") && !store.Contains(tab.URL) {
					candidates[tab.URL] = Page{URL: tab.URL, Title: tab.Title}
				}
			}
			for _, visit := range history {
				if visit.Visits >= 2 && !store.Contains(visit.URL) {
					candidates[visit.URL] = Page{URL: visit.URL, Title: visit.Title}
				}
			}
			list := make([]Page, 0, len(candidates))
			for _, page := range candidates {
				list = append(list, page)
			}
			pagesJSON, _ := json.Marshal(pages)
			listJSON, _ := json.Marshal(list)
			prompt := fmt.Sprintf("The user's browser group %q holds these pages, with their sections and page metadata: %s\n\n"+
				"Work out what ties them together, then pick up to %d pages from this list of the user's open tabs and the "+
				"pages they visited more than once, that belong in the same group: %s\n\n"+
				"Pick only urls from that list and give each a short reason.", group.Title, pagesJSON, count, listJSON)
			var reply similarReply
			finish = step("similar", "Finding pages like "+group.Title)
			err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: similarSchema, Model: model}, &reply)
			finish(err)
			if err != nil {
				return err
			}
			if err := writeJSONFile(filepath.Join(dir, "similar.json"), reply); err != nil {
				return err
			}
			added := []similarPage{}
			for _, page := range reply.Pages {
				if _, ok := candidates[page.URL]; !ok {
					continue
				}
				if group.Add(underSection("Similar", scope), KindSection, GroupPage{URL: page.URL, Title: page.Title, Source: SourceClaude, Verified: false}) {
					added = append(added, page)
				}
			}
			if err := PutGroups(ctx, store); err != nil {
				return err
			}
			return printValue(cmd, Similar{Summary: fmt.Sprintf("%d similar pages", len(added)), Group: group.ID, Cache: dir, Pages: added})
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Claude model, the claude default when empty")
	cmd.Flags().IntVar(&count, "count", 8, "Most pages to find")
	cmd.Flags().StringVar(&scope, "section", "", "Find pages like this section path only, and add them under it")
	return cmd
}
