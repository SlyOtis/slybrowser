package slyb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

var urlPattern = regexp.MustCompile(`https?://[^\s"'<>\\)\]}|` + "`" + `]+`)

var fetchPattern = regexp.MustCompile(`"name":"(?:WebFetch|mcp__plugin_slyb_slyb__tab_create)","input":\{[^}]*?"(?:url|args)":\[?"(https?://[^"]+)"`)

var searchPattern = regexp.MustCompile(`"name":"WebSearch","input":\{"query":"((?:[^"\\]|\\.)*)"`)

var cwdPattern = regexp.MustCompile(`"cwd":"([^"]+)"`)

var stampPattern = regexp.MustCompile(`"timestamp":"(\d{4}-\d\d-\d\dT[^"]+)"`)

type sessionFile struct {
	Size     int64             `json:"size"`
	ModTime  int64             `json:"mod_time"`
	Cwd      string            `json:"cwd"`
	URLs     map[string]int    `json:"urls"`
	Fetched  map[string]int    `json:"fetched"`
	Searches map[string]int    `json:"searches"`
	Start    string            `json:"start"`
	End      string            `json:"end"`
	Asked    map[string]string `json:"asked"`
}

const sessionCacheVersion = 3

type sessionCache struct {
	Version int                    `json:"version"`
	Files   map[string]sessionFile `json:"files"`
	Roots   map[string]string      `json:"roots"`
}

type ProjectPage struct {
	URL      string `json:"url"`
	Sessions int    `json:"sessions"`
	LastSeen string `json:"last_seen"`
}

type ProjectSearch struct {
	Query    string `json:"query"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"`
}

type Project struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Path           string          `json:"path"`
	Remote         string          `json:"remote,omitempty"`
	Email          string          `json:"email,omitempty"`
	Space          string          `json:"space,omitempty"`
	Shelf          string          `json:"shelf,omitempty"`
	Sessions       int             `json:"sessions"`
	Prefixes       []string        `json:"prefixes"`
	Terms          []string        `json:"terms"`
	ClaudePages    []ProjectPage   `json:"claude_pages"`
	ClaudeSearches []ProjectSearch `json:"claude_searches"`
}

type Frequent struct {
	URL     string `json:"url"`
	Count   int    `json:"count"`
	Project string `json:"project,omitempty"`
}

type ProjectIndex struct {
	SyncedAt string     `json:"synced_at"`
	Projects []Project  `json:"projects"`
	Frequent []Frequent `json:"frequent"`
}

func normalURL(raw string) string {
	raw = strings.TrimRight(raw, ".,;:!?*'")
	if strings.ContainsAny(raw, "${<*") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || placeholderHost(parsed.Hostname()) {
		return ""
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimRight(strings.ToLower(parsed.Scheme)+"://"+parsed.Host+parsed.EscapedPath(), "/")
}

func scanSession(path string) (sessionFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return sessionFile{}, err
	}
	defer file.Close()
	scanned := sessionFile{URLs: map[string]int{}, Fetched: map[string]int{}, Searches: map[string]int{}, Asked: map[string]string{}}
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, err := reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			rest, _ := reader.ReadBytes('\n')
			line = append(append([]byte{}, line...), rest...)
			err = nil
		}
		if len(line) > 0 {
			stamp := ""
			if match := stampPattern.FindSubmatch(line); match != nil {
				stamp = string(match[1])
				if scanned.Start == "" || stamp < scanned.Start {
					scanned.Start = stamp
				}
				if stamp > scanned.End {
					scanned.End = stamp
				}
			}
			if scanned.Cwd == "" {
				if match := cwdPattern.FindSubmatch(line); match != nil {
					scanned.Cwd = string(match[1])
				}
			}
			if bytes.Contains(line, []byte(`"WebSearch"`)) {
				for _, match := range searchPattern.FindAllSubmatch(line, -1) {
					var query string
					if json.Unmarshal(append(append([]byte{'"'}, match[1]...), '"'), &query) == nil && strings.TrimSpace(query) != "" {
						scanned.Searches[strings.TrimSpace(query)]++
						if _, ok := scanned.Asked[strings.TrimSpace(query)]; !ok && stamp != "" {
							scanned.Asked[strings.TrimSpace(query)] = stamp
						}
					}
				}
			}
			if bytes.Contains(line, []byte("http")) {
				for _, match := range fetchPattern.FindAllSubmatch(line, -1) {
					if key := normalURL(string(match[1])); key != "" {
						scanned.Fetched[key]++
					}
				}
				for _, match := range urlPattern.FindAll(line, -1) {
					if key := normalURL(string(match)); key != "" {
						scanned.URLs[key]++
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	return scanned, nil
}

func gitRoot(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return dir
	}
	return strings.TrimSpace(string(out))
}

func gitRemote(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	remote := strings.TrimSuffix(strings.TrimSpace(string(out)), ".git")
	if rest, ok := strings.CutPrefix(remote, "git@"); ok {
		host, path, _ := strings.Cut(rest, ":")
		remote = "https://" + host + "/" + path
	}
	return normalURL(remote)
}

func gitEmail(dir string) string {
	out, err := exec.Command("git", "-C", dir, "config", "user.email").Output()
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(out)))
}

func PostProjectSync(ctx context.Context) (ProjectIndex, error) {
	home, err := Home()
	if err != nil {
		return ProjectIndex{}, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ProjectIndex{}, err
	}
	cachePath := filepath.Join(home, "cache", "sessions.json")
	cache, err := getSessionCache(cachePath)
	if err != nil {
		return ProjectIndex{}, err
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(userHome, ".claude")
	}
	files, err := filepath.Glob(filepath.Join(claudeHome, "projects", "*", "*.jsonl"))
	if err != nil {
		return ProjectIndex{}, err
	}
	type job struct {
		path string
		info os.FileInfo
	}
	var jobs []job
	present := map[string]bool{}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		present[path] = true
		cached, ok := cache.Files[path]
		if !ok || cached.Size != info.Size() || cached.ModTime != info.ModTime().Unix() {
			jobs = append(jobs, job{path, info})
		}
	}
	for path := range cache.Files {
		if !present[path] {
			delete(cache.Files, path)
		}
	}
	finish := step("sessions", fmt.Sprintf("Reading %d changed Claude sessions of %d", len(jobs), len(files)))
	var lock sync.Mutex
	queue := make(chan job)
	var group sync.WaitGroup
	for range runtime.NumCPU() {
		group.Go(func() {
			for work := range queue {
				scanned, err := scanSession(work.path)
				if err != nil {
					continue
				}
				scanned.Size, scanned.ModTime = work.info.Size(), work.info.ModTime().Unix()
				lock.Lock()
				cache.Files[work.path] = scanned
				lock.Unlock()
			}
		})
	}
	for _, work := range jobs {
		queue <- work
	}
	close(queue)
	group.Wait()
	finish(nil)

	mentions := map[string]map[string]int{}
	forgotten, err := GetForgotten()
	if err != nil {
		return ProjectIndex{}, err
	}
	pageSeen := map[string]map[string]*ProjectPage{}
	searchSeen := map[string]map[string]*ProjectSearch{}
	fetched := map[string]map[string]int{}
	sessions := map[string]int{}
	for _, scanned := range cache.Files {
		if scanned.Cwd == "" {
			continue
		}
		root, ok := cache.Roots[scanned.Cwd]
		if !ok {
			root = gitRoot(scanned.Cwd)
			cache.Roots[scanned.Cwd] = root
		}
		if root == userHome || scratchRoot(root) {
			continue
		}
		sessions[root]++
		seen := time.Unix(scanned.ModTime, 0).UTC().Format(time.RFC3339)
		if pageSeen[root] == nil {
			pageSeen[root] = map[string]*ProjectPage{}
			searchSeen[root] = map[string]*ProjectSearch{}
		}
		for query, count := range scanned.Searches {
			if forgotten.Searches[strings.ToLower(query)] {
				continue
			}
			entry := searchSeen[root][query]
			if entry == nil {
				entry = &ProjectSearch{Query: query}
				searchSeen[root][query] = entry
			}
			entry.Count += count
			if seen > entry.LastSeen {
				entry.LastSeen = seen
			}
		}
		for key := range scanned.URLs {
			if normalURL(key) == "" || strings.Contains(key, "/org/repo") || forgotten.URLs[normalURL(key)] {
				continue
			}
			if mentions[key] == nil {
				mentions[key] = map[string]int{}
			}
			mentions[key][root]++
			page := pageSeen[root][key]
			if page == nil {
				page = &ProjectPage{URL: key}
				pageSeen[root][key] = page
			}
			page.Sessions++
			if seen > page.LastSeen {
				page.LastSeen = seen
			}
		}
		for key, count := range scanned.Fetched {
			if normalURL(key) == "" || forgotten.URLs[normalURL(key)] {
				continue
			}
			if fetched[key] == nil {
				fetched[key] = map[string]int{}
			}
			fetched[key][root] += count
		}
	}
	if err := writeJSONFile(cachePath, cache); err != nil {
		return ProjectIndex{}, err
	}

	dominant := func(counts map[string]int) (string, int, int) {
		best, top, total := "", 0, 0
		for root, count := range counts {
			total += count
			if count > top {
				best, top = root, count
			}
		}
		return best, top, total
	}
	prefixes := map[string][]string{}
	hosts := map[string]map[string]int{}
	for key, counts := range mentions {
		parsed, _ := url.Parse(key)
		host := parsed.Scheme + "://" + parsed.Host
		if hosts[host] == nil {
			hosts[host] = map[string]int{}
		}
		for root, count := range counts {
			hosts[host][root] += count
		}
		if parsed.Path == "" {
			continue
		}
		root, top, total := dominant(counts)
		deep := strings.Count(strings.Trim(parsed.Path, "/"), "/") >= 1
		if top >= 2 && float64(top) >= 0.7*float64(total) && (deep || localHost(parsed.Hostname()) || hostNamesProject(parsed.Hostname(), root)) {
			prefixes[root] = append(prefixes[root], key)
		}
	}
	for host, counts := range hosts {
		parsed, _ := url.Parse(host)
		if localHost(parsed.Hostname()) {
			continue
		}
		root, top, total := dominant(counts)
		if top >= 3 && float64(top) >= 0.9*float64(total) && hostNamesProject(parsed.Hostname(), root) {
			prefixes[root] = append(prefixes[root], host)
		}
	}

	index := ProjectIndex{SyncedAt: time.Now().UTC().Format(time.RFC3339), Projects: []Project{}, Frequent: []Frequent{}}
	for root, count := range sessions {
		project := Project{ID: root, Name: filepath.Base(root), Path: root, Sessions: count, Remote: gitRemote(root), Email: gitEmail(root)}
		list := prefixes[root]
		sort.Slice(list, func(i, j int) bool { return mentions[list[i]][root] > mentions[list[j]][root] })
		if len(list) > 300 {
			list = list[:300]
		}
		if project.Remote != "" {
			list = append([]string{project.Remote}, list...)
		}
		for i := range list {
			list[i] = strings.ToLower(list[i])
		}
		project.Prefixes = list
		project.Terms = projectTerms(project)
		project.ClaudePages = topPages(pageSeen[root], 60)
		project.ClaudeSearches = topSearches(searchSeen[root], 40)
		if len(project.Prefixes) > 0 || len(project.ClaudePages) > 0 {
			index.Projects = append(index.Projects, project)
		}
	}
	known := map[string]bool{}
	for _, project := range index.Projects {
		known[project.Path] = true
	}
	for _, repo := range localRepos(userHome) {
		if known[repo] {
			continue
		}
		project := Project{ID: repo, Name: filepath.Base(repo), Path: repo, Remote: gitRemote(repo), Email: gitEmail(repo), Prefixes: []string{}, Terms: []string{}, ClaudePages: []ProjectPage{}, ClaudeSearches: []ProjectSearch{}}
		if project.Remote != "" {
			project.Prefixes = []string{strings.ToLower(project.Remote)}
		}
		project.Terms = projectTerms(project)
		index.Projects = append(index.Projects, project)
	}
	sort.Slice(index.Projects, func(i, j int) bool { return index.Projects[i].Sessions > index.Projects[j].Sessions })
	visible := index.Projects[:0]
	for _, project := range index.Projects {
		if !forgotten.Projects[project.ID] {
			visible = append(visible, project)
		}
	}
	index.Projects = visible
	siblings := map[string]int{}
	for _, project := range index.Projects {
		siblings[filepath.Dir(project.Path)]++
	}
	for i, project := range index.Projects {
		index.Projects[i].Shelf = "Other"
		if siblings[filepath.Dir(project.Path)] > 1 {
			index.Projects[i].Shelf = filepath.Base(filepath.Dir(project.Path))
		}
	}
	names := map[string]int{}
	for _, project := range index.Projects {
		names[project.Name]++
	}
	for i, project := range index.Projects {
		if names[project.Name] > 1 {
			index.Projects[i].Name = filepath.Base(filepath.Dir(project.Path)) + "/" + project.Name
		}
	}
	for key, counts := range fetched {
		root, _, total := dominant(counts)
		if total >= 3 {
			index.Frequent = append(index.Frequent, Frequent{URL: key, Count: total, Project: root})
		}
	}
	sort.Slice(index.Frequent, func(i, j int) bool { return index.Frequent[i].Count > index.Frequent[j].Count })
	if len(index.Frequent) > 15 {
		index.Frequent = index.Frequent[:15]
	}
	ties, err := GetSpaceTies()
	if err != nil {
		return ProjectIndex{}, err
	}
	rules, err := GetRules()
	if err != nil {
		return ProjectIndex{}, err
	}
	groups, err := GetGroups()
	if err != nil {
		return ProjectIndex{}, err
	}
	for i := range index.Projects {
		index.Projects[i].Space = projectSpace(ctx, index.Projects[i], ties, rules, groups)
	}
	if err := writeJSONFile(filepath.Join(home, "projects.json"), index); err != nil {
		return ProjectIndex{}, err
	}
	return index, nil
}

type ProjectSummary struct {
	Summary  string     `json:"summary"`
	SyncedAt string     `json:"synced_at"`
	Projects []Project  `json:"projects"`
	Frequent []Frequent `json:"frequent"`
}

func NewProjectCommand() *cobra.Command {
	project := &cobra.Command{Use: "project", Short: "The projects tabs belong to, learned from the Claude Code sessions"}
	var prefixes bool
	summarize := func(index ProjectIndex) ProjectSummary {
		if !prefixes {
			for i := range index.Projects {
				if len(index.Projects[i].Prefixes) > 5 {
					index.Projects[i].Prefixes = index.Projects[i].Prefixes[:5]
				}
			}
		}
		return ProjectSummary{
			Summary:  fmt.Sprintf("%d projects", len(index.Projects)),
			SyncedAt: index.SyncedAt,
			Projects: index.Projects,
			Frequent: index.Frequent,
		}
	}
	get := &cobra.Command{
		Use:     "get",
		Short:   "List the projects with their repo, session count and the urls that mark a tab as theirs",
		Example: "slyb project get --prefixes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := GetProjectIndex()
			if err != nil {
				return err
			}
			if index.SyncedAt == "" {
				return cli.Rejected("no project index yet", "slyb project sync")
			}
			return printValue(cmd, summarize(index))
		},
	}
	get.Flags().BoolVar(&prefixes, "prefixes", false, "Print every url prefix, not the first five")
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Read the Claude Code sessions again, rebuild the project index and retag the open tabs",
		Long: "Read every Claude Code session under CLAUDE_CONFIG_DIR/projects, ~/.claude/projects when it is unset, that changed since the last sync, map each to the git\n" +
			"repository it ran in, and give a project the urls its sessions name far more than any other project's do,\n" +
			"plus its git remote and git email. Every git repository under ~/Projects, ~/Developer, ~/code, ~/src, ~/repos,\n" +
			"~/GitHub, ~/Documents/GitHub and ~/Sites becomes a project too. Each project goes to a space by the folders tied\n" +
			"to spaces, then the space rules, then the hosts its sessions used, and onto a shelf named after its parent folder\n" +
			"when it has siblings, or Other. Urls Claude fetched or opened three times or more become the frequent list.\n" +
			"Projects removed from the sidebar and forgotten pages stay out. The open tabs are retagged when Zen is running.",
		Example: "slyb project sync",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			index, err := PostProjectSync(cmd.Context())
			if err != nil {
				return err
			}
			if _, err := Call(cmd.Context(), "project.reload", nil); err != nil && !notRunning(err) {
				return err
			}
			return printValue(cmd, summarize(index))
		},
	}
	sync.Flags().BoolVar(&prefixes, "prefixes", false, "Print every url prefix, not the first five")
	project.AddCommand(get, sync)
	return project
}

func placeholderHost(host string) bool {
	for _, placeholder := range []string{"example.com", "example.org", "example.net", "my-namespace", "your-", "w3.org", "schemas.", "xmlsoap.org", "purl.org"} {
		if strings.Contains(host, placeholder) {
			return true
		}
	}
	return !strings.Contains(host, ".") && host != "localhost"
}

func scratchRoot(root string) bool {
	for _, prefix := range []string{os.TempDir(), "/tmp", "/private/tmp", "/private/var/folders", "/var/folders"} {
		if prefix != "" && strings.HasPrefix(root, strings.TrimRight(prefix, "/")) {
			return true
		}
	}
	_, err := os.Stat(root)
	return err != nil
}

func localHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".localhost")
}

func hostNamesProject(host, root string) bool {
	host = strings.ToLower(host)
	for _, token := range strings.FieldsFunc(strings.ToLower(filepath.Base(root)), func(r rune) bool { return r == '-' || r == '_' || r == '.' }) {
		if len(token) >= 4 && strings.Contains(host, token) {
			return true
		}
	}
	return false
}

func projectTerms(project Project) []string {
	terms := []string{}
	seen := map[string]bool{}
	add := func(value string) {
		for _, token := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '/' || r == ' ' }) {
			if len(token) >= 4 && !seen[token] {
				seen[token] = true
				terms = append(terms, token)
			}
		}
	}
	add(filepath.Base(project.Path))
	if project.Remote != "" {
		add(filepath.Base(project.Remote))
	}
	return terms
}

func topPages(pages map[string]*ProjectPage, limit int) []ProjectPage {
	list := make([]ProjectPage, 0, len(pages))
	for _, page := range pages {
		list = append(list, *page)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Sessions != list[j].Sessions {
			return list[i].Sessions > list[j].Sessions
		}
		return list[i].LastSeen > list[j].LastSeen
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

func topSearches(searches map[string]*ProjectSearch, limit int) []ProjectSearch {
	list := make([]ProjectSearch, 0, len(searches))
	for _, search := range searches {
		list = append(list, *search)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Count != list[j].Count {
			return list[i].Count > list[j].Count
		}
		return list[i].LastSeen > list[j].LastSeen
	})
	if len(list) > limit {
		list = list[:limit]
	}
	return list
}

var repoRoots = []string{"Projects", "Developer", "code", "src", "repos", "GitHub", "Documents/GitHub", "Sites"}

var skippedDirs = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true, ".venv": true, "Pods": true, ".cache": true, ".git": true}

func localRepos(home string) []string {
	var repos []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if entry.Name() == ".git" {
				repos = append(repos, dir)
				break
			}
		}
		if depth == 0 {
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && !skippedDirs[entry.Name()] && !strings.HasPrefix(entry.Name(), ".") {
				walk(filepath.Join(dir, entry.Name()), depth-1)
			}
		}
	}
	for _, root := range repoRoots {
		walk(filepath.Join(home, root), 3)
	}
	return repos
}

func getSessionCache(path string) (sessionCache, error) {
	cache := sessionCache{Files: map[string]sessionFile{}, Roots: map[string]string{}}
	if err := readJSONFile(path, &cache); err != nil && !os.IsNotExist(err) {
		return sessionCache{}, err
	}
	if cache.Files == nil || cache.Version != sessionCacheVersion {
		cache.Files = map[string]sessionFile{}
	}
	cache.Version = sessionCacheVersion
	if cache.Roots == nil {
		cache.Roots = map[string]string{}
	}
	return cache, nil
}
