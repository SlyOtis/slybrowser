package slyb

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	At      string `json:"at"`
	URL     string `json:"url,omitempty"`
	Files   int    `json:"files"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

type ClaudeSearch struct {
	Query  string `json:"query"`
	At     string `json:"at"`
	URL    string `json:"url,omitempty"`
	Change string `json:"change,omitempty"`
}

type PullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
}

type RepoWork struct {
	Root     string         `json:"root"`
	Name     string         `json:"name"`
	Remote   string         `json:"remote,omitempty"`
	Commits  []Commit       `json:"commits"`
	Sessions int            `json:"sessions"`
	Searches []ClaudeSearch `json:"searches"`
	Pulls    []PullRequest  `json:"pulls"`
	Files    int            `json:"files"`
	Added    int            `json:"added"`
	Removed  int            `json:"removed"`
	First    string         `json:"first,omitempty"`
	Last     string         `json:"last,omitempty"`
}

func toolOutput(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) ([]byte, error) {
	limited, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(limited, name, args...)
	command.Dir = dir
	return command.Output()
}

func gitCommits(ctx context.Context, root, email, remote string, from time.Time) []Commit {
	args := []string{"log", "--since=" + from.Format(time.RFC3339), "--no-merges", "--pretty=format:@%H\x1f%cI\x1f%s", "--numstat"}
	if email != "" {
		args = append(args, "--author="+email)
	}
	out, err := toolOutput(ctx, root, 10*time.Second, "git", args...)
	if err != nil {
		return nil
	}
	var commits []Commit
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "@") {
			parts := strings.SplitN(line[1:], "\x1f", 3)
			if len(parts) == 3 {
				commit := Commit{SHA: parts[0], At: parts[1], Subject: parts[2]}
				if remote != "" {
					commit.URL = remote + "/commit/" + commit.SHA
				}
				commits = append(commits, commit)
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(commits) > 0 {
			last := &commits[len(commits)-1]
			added, _ := strconv.Atoi(fields[0])
			removed, _ := strconv.Atoi(fields[1])
			last.Files++
			last.Added += added
			last.Removed += removed
		}
	}
	return commits
}

func ghPulls(ctx context.Context, root string, from time.Time) []PullRequest {
	if _, err := exec.LookPath("gh"); err != nil {
		return []PullRequest{}
	}
	out, err := toolOutput(ctx, root, 8*time.Second, "gh", "pr", "list", "--author", "@me", "--state", "all", "--search", "updated:>="+from.Format("2006-01-02"), "--json", "number,title,url,state,updatedAt", "--limit", "20")
	if err != nil {
		return []PullRequest{}
	}
	pulls := []PullRequest{}
	if json.Unmarshal(out, &pulls) != nil {
		return []PullRequest{}
	}
	return pulls
}

func GetWork(ctx context.Context, from time.Time) ([]RepoWork, error) {
	index, err := PostProjectSync(ctx)
	if err != nil {
		return nil, err
	}
	home, err := Home()
	if err != nil {
		return nil, err
	}
	cache, err := getSessionCache(filepath.Join(home, "cache", "sessions.json"))
	if err != nil {
		return nil, err
	}
	since := from.UTC().Format(time.RFC3339)
	type session struct {
		start, end string
		asked      map[string]string
	}
	sessions := map[string][]session{}
	for _, file := range cache.Files {
		if file.End == "" || file.End < since {
			continue
		}
		root := cache.Roots[file.Cwd]
		if root == "" {
			root = file.Cwd
		}
		sessions[root] = append(sessions[root], session{start: file.Start, end: file.End, asked: file.Asked})
	}
	var work []RepoWork
	for _, project := range index.Projects {
		commits := gitCommits(ctx, project.Path, project.Email, project.Remote, from)
		if commits == nil {
			commits = []Commit{}
		}
		mine := sessions[project.Path]
		if len(commits) == 0 && len(mine) == 0 {
			continue
		}
		repo := RepoWork{Root: project.Path, Name: project.Name, Remote: project.Remote, Commits: commits, Sessions: len(mine), Searches: []ClaudeSearch{}, Pulls: []PullRequest{}}
		sort.Slice(repo.Commits, func(i, j int) bool { return repo.Commits[i].At < repo.Commits[j].At })
		for _, commit := range repo.Commits {
			repo.Files += commit.Files
			repo.Added += commit.Added
			repo.Removed += commit.Removed
		}
		stretch := func(at string) {
			if at == "" {
				return
			}
			stamp, err := time.Parse(time.RFC3339, at)
			if err != nil {
				return
			}
			value := stamp.UTC().Format(time.RFC3339)
			if repo.First == "" || value < repo.First {
				repo.First = value
			}
			if value > repo.Last {
				repo.Last = value
			}
		}
		for _, commit := range repo.Commits {
			stretch(commit.At)
		}
		for _, item := range mine {
			stretch(item.start)
			stretch(item.end)
			for query, at := range item.asked {
				if at < since {
					continue
				}
				search := ClaudeSearch{Query: query, At: at}
				asked, _ := time.Parse(time.RFC3339, at)
				ended, _ := time.Parse(time.RFC3339, item.end)
				for _, commit := range repo.Commits {
					made, err := time.Parse(time.RFC3339, commit.At)
					if err == nil && !made.Before(asked) && made.Before(ended.Add(2*time.Hour)) {
						search.URL, search.Change = commit.URL, commit.Subject
						break
					}
				}
				if search.URL == "" {
					search.URL = repo.Remote
				}
				if search.URL == "" {
					search.URL = "file://" + repo.Root
				}
				repo.Searches = append(repo.Searches, search)
			}
		}
		sort.Slice(repo.Searches, func(i, j int) bool { return repo.Searches[i].At > repo.Searches[j].At })
		if len(repo.Commits) > 0 && strings.HasPrefix(repo.Remote, "https://") {
			repo.Pulls = ghPulls(ctx, repo.Root, from)
		}
		work = append(work, repo)
	}
	sort.Slice(work, func(i, j int) bool { return work[i].Last > work[j].Last })
	return work, nil
}

func workPoint(repo RepoWork) JourneyPoint {
	link := repo.Remote
	if link == "" {
		link = "file://" + repo.Root
	}
	point := JourneyPoint{Text: "Working on " + repo.Name, URL: link, Title: repo.Name, VisitedAt: repo.Last, Trail: []TrailStep{}}
	for _, pull := range repo.Pulls {
		point.Trail = append(point.Trail, TrailStep{URL: pull.URL, Title: fmt.Sprintf("#%d %s", pull.Number, pull.Title), VisitedAt: pull.UpdatedAt, Way: "pr"})
	}
	for i := len(repo.Commits) - 1; i >= 0 && len(point.Trail) < 8; i-- {
		commit := repo.Commits[i]
		link := commit.URL
		if link == "" {
			link = "file://" + repo.Root
		}
		point.Trail = append(point.Trail, TrailStep{URL: link, Title: fmt.Sprintf("%s (+%d −%d)", commit.Subject, commit.Added, commit.Removed), VisitedAt: commit.At, Way: "commit"})
	}
	for _, search := range repo.Searches[:min(len(repo.Searches), 6)] {
		title := "Claude searched: " + search.Query
		if search.Change != "" {
			title += " → " + search.Change
		}
		point.Trail = append(point.Trail, TrailStep{URL: search.URL, Title: title, VisitedAt: search.At, Way: "search"})
	}
	copied := repo
	point.Work = &copied
	return point
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

func NewWorkCommand() *cobra.Command {
	work := &cobra.Command{Use: "work", Short: "What was built in each repository: commits and their diff, pull requests, and the Claude sessions and searches behind them"}
	var since string
	get := &cobra.Command{
		Use:     "get",
		Short:   "List the repositories worked on since the day's start or --since, with commits, diff stats, pull requests and Claude searches linked to the change they led to",
		Example: "slyb work get --since 2026-10-27T00:00",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, err := GetUserProfile()
			if err != nil {
				return err
			}
			from, err := journeySince(profile, since)
			if err != nil {
				return cli.Rejected(err.Error(), "")
			}
			work, err := GetWork(cmd.Context(), from)
			if err != nil {
				return err
			}
			return printValue(cmd, work)
		},
	}
	get.Flags().StringVar(&since, "since", "", "Start here, such as 2026-10-27T00:00, instead of the day's start")
	work.AddCommand(get)
	return work
}

func otherWork(repos []RepoWork) JourneyPoint {
	point := JourneyPoint{Text: "Other repositories", Trail: []TrailStep{}}
	commits := 0
	for _, repo := range repos {
		commits += len(repo.Commits)
		if repo.Last > point.VisitedAt {
			point.VisitedAt = repo.Last
		}
		link := repo.Remote
		if link == "" {
			link = "file://" + repo.Root
		}
		did := []string{}
		if count := len(repo.Commits); count > 0 {
			did = append(did, fmt.Sprintf("%d %s, +%d −%d", count, plural(count, "commit", "commits"), repo.Added, repo.Removed))
		}
		if repo.Sessions > 0 {
			did = append(did, fmt.Sprintf("%d Claude %s", repo.Sessions, plural(repo.Sessions, "session", "sessions")))
		}
		point.Trail = append(point.Trail, TrailStep{URL: link, Title: repo.Name + ": " + strings.Join(did, ", "), VisitedAt: repo.Last, Way: "commit"})
	}
	point.URL, point.Title = point.Trail[0].URL, repos[0].Name
	for _, repo := range repos {
		if repo.Remote != "" {
			point.URL, point.Title = repo.Remote, repo.Name
			break
		}
	}
	point.Detail = fmt.Sprintf("%d more %s, %d %s.", len(repos), plural(len(repos), "repository", "repositories"), commits, plural(commits, "commit", "commits"))
	return point
}
