package slyb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

const (
	LiveGitHub = "github"
	LiveTitle  = "title"
	liveEvery  = 5
)

var githubRepo = regexp.MustCompile(`^https://github\.com/([^/?#]+)/([^/?#]+)/?$`)

type LiveChild struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Number int    `json:"number"`
	Draft  bool   `json:"draft,omitempty"`
}

type LiveItem struct {
	URL       string      `json:"url"`
	Source    string      `json:"source"`
	Every     int         `json:"every"`
	Count     int         `json:"count"`
	Children  []LiveChild `json:"children"`
	SyncedAt  string      `json:"synced_at,omitempty"`
	Error     string      `json:"error,omitempty"`
	CreatedAt string      `json:"created_at"`
}

type LiveStore struct {
	Items []LiveItem `json:"items"`
}

func livePath() (string, error) {
	return homeFile("live.json")
}

func GetLive() (LiveStore, error) {
	path, err := livePath()
	if err != nil {
		return LiveStore{}, err
	}
	store := LiveStore{Items: []LiveItem{}}
	if err := readJSONFile(path, &store); err != nil && !os.IsNotExist(err) {
		return LiveStore{}, err
	}
	if store.Items == nil {
		store.Items = []LiveItem{}
	}
	return store, nil
}

func PutLive(ctx context.Context, store LiveStore) error {
	path, err := livePath()
	if err != nil {
		return err
	}
	if err := writeJSONFile(path, store); err != nil {
		return err
	}
	if _, err := Call(ctx, "live.reload", nil); err != nil && !notRunning(err) {
		return err
	}
	return nil
}

func (store *LiveStore) Item(url string) (*LiveItem, error) {
	for i := range store.Items {
		if store.Items[i].URL == url {
			return &store.Items[i], nil
		}
	}
	return nil, cli.Rejected("no live item for "+url, "read them with `slyb live get`")
}

func getLiveSource(url string) string {
	if githubRepo.MatchString(url) {
		return LiveGitHub
	}
	return LiveTitle
}

func getGitHubItems(ctx context.Context, repo, kind string) ([]LiveChild, error) {
	fields := "number,title,url"
	if kind == "pr" {
		fields += ",isDraft"
	}
	out, err := toolOutput(ctx, "", 15*time.Second, "gh", kind, "list", "--repo", repo, "--state", "open", "--json", fields, "--limit", "50")
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			return nil, fmt.Errorf("gh %s list: %s", kind, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("gh %s list: %w", kind, err)
	}
	found := []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		IsDraft bool   `json:"isDraft"`
	}{}
	if err := json.Unmarshal(out, &found); err != nil {
		return nil, err
	}
	children := make([]LiveChild, 0, len(found))
	for _, one := range found {
		children = append(children, LiveChild{URL: one.URL, Title: one.Title, Kind: kind, Number: one.Number, Draft: one.IsDraft})
	}
	return children, nil
}

func putLiveItem(ctx context.Context, item *LiveItem) {
	item.SyncedAt = now()
	if item.Source != LiveGitHub {
		return
	}
	match := githubRepo.FindStringSubmatch(item.URL)
	repo := match[1] + "/" + match[2]
	if _, err := exec.LookPath("gh"); err != nil {
		item.Error = "gh is not installed"
		return
	}
	children := []LiveChild{}
	for _, kind := range []string{"pr", "issue"} {
		found, err := getGitHubItems(ctx, repo, kind)
		if err != nil {
			item.Error = err.Error()
			return
		}
		children = append(children, found...)
	}
	item.Error = ""
	item.Children = children
	item.Count = len(children)
}

func PostLiveSync(ctx context.Context, url string) (LiveStore, error) {
	store, err := GetLive()
	if err != nil {
		return LiveStore{}, err
	}
	if url != "" {
		item, err := store.Item(url)
		if err != nil {
			return LiveStore{}, err
		}
		putLiveItem(ctx, item)
	} else {
		for i := range store.Items {
			putLiveItem(ctx, &store.Items[i])
		}
	}
	return store, PutLive(ctx, store)
}

func NewLiveCommand() *cobra.Command {
	live := &cobra.Command{Use: "live", Short: "Live items: pages that listen for what changes on them, shown as a count on the page and its items as subpages"}
	live.AddCommand(&cobra.Command{
		Use:         "get [url]",
		Short:       "List the live items with their count, subpages and when they last synced, or show one",
		Example:     "slyb live get https://github.com/SlyOtis/quillborne",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "read"},
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetLive()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				item, err := store.Item(strings.TrimSpace(args[0]))
				if err != nil {
					return err
				}
				return printValue(cmd, item)
			}
			return printValue(cmd, store.Items)
		},
	})
	var every int
	create := &cobra.Command{
		Use:         "create <url>",
		Short:       "Make a page live: a GitHub repository lists its open pull requests and issues, any other page counts from its tab title",
		Example:     "slyb live create https://github.com/SlyOtis/quillborne --every 5",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			url := strings.TrimSpace(args[0])
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				return cli.Rejected("a live item needs an http or https address", "")
			}
			if every < 0 {
				return cli.Rejected("--every is minutes, 0 or more", "")
			}
			store, err := GetLive()
			if err != nil {
				return err
			}
			if _, err := store.Item(url); err == nil {
				return cli.Rejected(url+" is already live", "change it with `slyb live update`")
			}
			item := LiveItem{URL: url, Source: getLiveSource(url), Every: every, Children: []LiveChild{}, CreatedAt: now()}
			putLiveItem(cmd.Context(), &item)
			store.Items = append(store.Items, item)
			if err := PutLive(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, item)
		},
	}
	create.Flags().IntVar(&every, "every", liveEvery, "Minutes between syncs, 0 to sync only when the page is opened")
	live.AddCommand(create)
	var updateEvery int
	update := &cobra.Command{
		Use:         "update <url>",
		Short:       "Change how often a live item syncs",
		Example:     "slyb live update https://github.com/SlyOtis/quillborne --every 15",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("every") {
				return cli.Rejected("nothing to change", "pass --every")
			}
			if updateEvery < 0 {
				return cli.Rejected("--every is minutes, 0 or more", "")
			}
			store, err := GetLive()
			if err != nil {
				return err
			}
			item, err := store.Item(strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			item.Every = updateEvery
			if err := PutLive(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, item)
		},
	}
	update.Flags().IntVar(&updateEvery, "every", liveEvery, "Minutes between syncs, 0 to sync only when the page is opened")
	live.AddCommand(update)
	live.AddCommand(&cobra.Command{
		Use:         "delete <url>",
		Short:       "Stop a page being live; the page itself stays where it is",
		Example:     "slyb live delete https://github.com/SlyOtis/quillborne",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			url := strings.TrimSpace(args[0])
			store, err := GetLive()
			if err != nil {
				return err
			}
			if _, err := store.Item(url); err != nil {
				return err
			}
			kept := []LiveItem{}
			for _, item := range store.Items {
				if item.URL != url {
					kept = append(kept, item)
				}
			}
			store.Items = kept
			if err := PutLive(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, map[string]string{"url": url, "summary": "No longer live"})
		},
	})
	live.AddCommand(&cobra.Command{
		Use:         "sync [url]",
		Short:       "Sync every live item now, or the one named: a GitHub repository reads its open pull requests and issues with gh",
		Example:     "slyb live sync https://github.com/SlyOtis/quillborne",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			url := ""
			if len(args) == 1 {
				url = strings.TrimSpace(args[0])
			}
			store, err := PostLiveSync(cmd.Context(), url)
			if err != nil {
				return err
			}
			if url != "" {
				item, _ := store.Item(url)
				return printValue(cmd, item)
			}
			return printValue(cmd, store.Items)
		},
	})
	return live
}
