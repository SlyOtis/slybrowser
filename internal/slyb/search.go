package slyb

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Visit struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Visits    int    `json:"visits"`
	LastVisit string `json:"last_visit"`
}

type Found struct {
	Tabs    []Tab   `json:"tabs"`
	History []Visit `json:"history"`
}

type Match struct {
	Kind   string `json:"kind"`
	TabID  string `json:"tab_id,omitempty"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type askReply struct {
	Matches []Match `json:"matches"`
}

const askSchema = `{"type":"object","properties":{"matches":{"type":"array","items":{"type":"object","properties":{"kind":{"type":"string","enum":["tab","history"]},"tab_id":{"type":"string"},"url":{"type":"string"},"title":{"type":"string"},"reason":{"type":"string"}},"required":["kind","url","title","reason"],"additionalProperties":false}}},"required":["matches"],"additionalProperties":false}`

func NewSearchCommand() *cobra.Command {
	var limit int
	var ask bool
	var model string
	cmd := &cobra.Command{
		Use:   "search <words>...",
		Short: "Find open tabs and visited pages by words in their title or url, or ask Claude in plain language",
		Long: "Without --ask every word must appear in the title or url of an open tab or a visited page. With --ask the\n" +
			"words are a question in plain language: the open tabs and the newest --limit visited pages go to claude,\n" +
			"which returns the ones that answer it with a reason each.",
		Example:     "slyb search zen folders\nslyb search --ask \"the recipe with lentils I read last week\"",
		Args:        cobra.MinimumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "read"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			query := strings.Join(args, " ")
			var tabs []Tab
			if err := CallInto(ctx, "tab.get", map[string]any{}, &tabs); err != nil {
				return err
			}
			if ask {
				var history []Visit
				if err := CallInto(ctx, "history.get", map[string]any{"limit": limit}, &history); err != nil {
					return err
				}
				tabsJSON, _ := json.Marshal(tabs)
				historyJSON, _ := json.Marshal(history)
				prompt := fmt.Sprintf("Find what the user is looking for among their open tabs and browsing history.\n"+
					"Question: %s\nOpen tabs: %s\nHistory, newest first: %s\n"+
					"Return the best matches, most likely first, at most ten. For a tab give kind tab and its tab_id. "+
					"For a visited page give kind history. Copy url and title exactly. Give a short reason for each.", query, tabsJSON, historyJSON)
				var reply askReply
				finish := step("claude", "Claude searches tabs and history")
				err := PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: askSchema, Model: model}, &reply)
				finish(err)
				if err != nil {
					return err
				}
				return printValue(cmd, reply.Matches)
			}
			words := strings.Fields(strings.ToLower(query))
			found := Found{Tabs: []Tab{}}
			for _, tab := range tabs {
				text := strings.ToLower(tab.Title + " " + tab.URL)
				all := true
				for _, word := range words {
					if !strings.Contains(text, word) {
						all = false
						break
					}
				}
				if all {
					found.Tabs = append(found.Tabs, tab)
				}
			}
			if err := CallInto(ctx, "history.get", map[string]any{"query": query, "limit": limit}, &found.History); err != nil {
				return err
			}
			return printValue(cmd, found)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 300, "Most visited pages to search, or to send to Claude with --ask")
	cmd.Flags().BoolVar(&ask, "ask", false, "Ask Claude in plain language instead of matching words")
	cmd.Flags().StringVar(&model, "model", "", "Claude model for --ask, the claude default when empty")
	return cmd
}
