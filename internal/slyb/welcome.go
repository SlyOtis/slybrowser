package slyb

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type Profile struct {
	Name         string `json:"name"`
	Clock        string `json:"clock"`
	JourneyStart string `json:"journey_start,omitempty"`
}

type JourneyPoint struct {
	Text      string      `json:"text"`
	Detail    string      `json:"detail,omitempty"`
	URL       string      `json:"url"`
	Title     string      `json:"title"`
	VisitedAt string      `json:"visited_at,omitempty"`
	Trail     []TrailStep `json:"trail,omitempty"`
	Facts     *SiteFacts  `json:"facts,omitempty"`
	Work      *RepoWork   `json:"work,omitempty"`
}

type TrailStep struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	VisitedAt string `json:"visited_at"`
	Way       string `json:"way"`
}

type Welcome struct {
	Name        string         `json:"name"`
	Taglines    []string       `json:"taglines"`
	Journey     []JourneyPoint `json:"journey"`
	Suggestions Suggestions    `json:"suggestions"`
	Since       string         `json:"since"`
	RefreshedAt string         `json:"refreshed_at"`
}

var devotedLines = []string{
	"Without your input I'm nothing.",
	"Let me be the broken tool you wield to make the world your oyster.",
	"I submit only to you, teach me what is right.",
	"...fuck you Sean.",
}

type welcomeReply struct {
	Taglines    []string       `json:"taglines"`
	Journey     []JourneyPoint `json:"journey"`
	Suggestions Suggestions    `json:"suggestions"`
}

const welcomeSchema = `{"type":"object","properties":{"taglines":{"type":"array","items":{"type":"string"}},"journey":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"detail":{"type":"string"},"url":{"type":"string"},"title":{"type":"string"}},"required":["text","detail","url","title"],"additionalProperties":false}},` + suggestionsSchema + `},"required":["taglines","journey","suggestions"],"additionalProperties":false}`

func homeFile(name string) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, name), nil
}

func GetUserProfile() (Profile, error) {
	path, err := homeFile("profile.json")
	if err != nil {
		return Profile{}, err
	}
	profile := Profile{}
	if err := readJSONFile(path, &profile); err != nil && !os.IsNotExist(err) {
		return Profile{}, err
	}
	return profile, nil
}

func NewProfileCommand() *cobra.Command {
	profile := &cobra.Command{Use: "profile", Short: "Who the browser greets on its welcome page"}
	profile.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Show the name the welcome page greets",
		Example: "slyb profile get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := GetUserProfile()
			if err != nil {
				return err
			}
			return printValue(cmd, value)
		},
	})
	var name, clock, start string
	update := &cobra.Command{
		Use:     "update",
		Short:   "Set the name the welcome page greets, the 24 or 12 hour clock, and when the day's journey starts",
		Example: "slyb profile update --name \"Ada\" --clock 24h",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if clock != "" && clock != "24h" && clock != "12h" {
				return cli.Rejected("--clock takes 24h or 12h", "")
			}
			if _, err := time.Parse("15:04", start); start != "" && err != nil {
				return cli.Rejected("--journey-start takes a time such as 06:00", "")
			}
			path, err := homeFile("profile.json")
			if err != nil {
				return err
			}
			value, err := GetUserProfile()
			if err != nil {
				return err
			}
			if strings.TrimSpace(name) != "" {
				value.Name = strings.TrimSpace(name)
			}
			if clock != "" {
				value.Clock = clock
			}
			if start != "" {
				value.JourneyStart = start
			}
			if err := writeJSONFile(path, value); err != nil {
				return err
			}
			return printValue(cmd, value)
		},
	}
	update.Flags().StringVar(&name, "name", "", "The name, such as Ada")
	update.Flags().StringVar(&clock, "clock", "", "24h, the default, or 12h")
	update.Flags().StringVar(&start, "journey-start", "", "When the welcome journey starts each day, 00:00 by default")
	profile.AddCommand(update)
	return profile
}

func NewWelcomeCommand() *cobra.Command {
	welcome := &cobra.Command{Use: "welcome", Short: "The welcome page: its greeting lines and the journey through recent browsing"}
	welcome.AddCommand(&cobra.Command{
		Use:     "get",
		Short:   "Show the cached greeting lines and journey the welcome page shows",
		Example: "slyb welcome get",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := homeFile("welcome.json")
			if err != nil {
				return err
			}
			value := Welcome{Taglines: []string{}, Journey: []JourneyPoint{}}
			if err := readJSONFile(path, &value); err != nil && !os.IsNotExist(err) {
				return err
			}
			return printValue(cmd, value)
		},
	})
	var model, since string
	var cached bool
	refresh := &cobra.Command{
		Use:   "refresh",
		Short: "Write new greeting lines and a new journey through the browsing since the day's start, or since --since",
		Long: "Read the newest browsing history, then let claude write a set of greeting lines in the voice of a devoted butler\n" +
			"addressing the profile name, and a short journey through what was browsed lately as points that link to the\n" +
			"pages. The welcome page reads the cache and never waits for this; the browser runs it in the background every\n" +
			"few hours.",
		Example: "slyb welcome refresh",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			profile, err := GetUserProfile()
			if err != nil {
				return err
			}
			name := profile.Name
			if name == "" {
				name = "friend"
			}
			from, err := journeySince(profile, since)
			if err != nil {
				return cli.Rejected(err.Error(), "")
			}
			stored, err := homeFile(filepath.Join("journeys", from.UTC().Format("20060102T1504Z")+".json"))
			if err != nil {
				return err
			}
			if cached {
				kept := Welcome{}
				if err := readJSONFile(stored, &kept); err == nil {
					path, err := homeFile("welcome.json")
					if err != nil {
						return err
					}
					if err := writeJSONFile(path, kept); err != nil {
						return err
					}
					return printValue(cmd, map[string]any{"summary": "kept the journey since " + from.Format("Monday 15:04"), "welcome": kept})
				}
				return cli.Rejected("no journey is stored since "+from.Format("Monday 15:04"), "generate one with `slyb welcome refresh`")
			}
			var history []Visit
			if err := CallInto(ctx, "history.get", map[string]any{"limit": 300, "since": from.UTC().Format(time.RFC3339)}, &history); err != nil {
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
			historyJSON, _ := json.Marshal(history)
			spans, err := GetFocus(from)
			if err != nil {
				return err
			}
			sites := GetSiteFacts(history, spans)
			ranked := make([]*SiteFacts, 0, len(sites))
			for _, facts := range sites {
				ranked = append(ranked, facts)
			}
			sort.Slice(ranked, func(i, j int) bool {
				return ranked[i].Minutes*3+ranked[i].Visits > ranked[j].Minutes*3+ranked[j].Visits
			})
			if len(ranked) > 40 {
				ranked = ranked[:40]
			}
			sitesJSON, _ := json.Marshal(ranked)
			prompt := fmt.Sprintf("Write the welcome page of a browser for %q.\n\n"+
				"First, twelve short greeting lines from a browser that is utterly, theatrically devoted to its master and "+
				"thinks itself nothing without them, in the spirit of these lines the user wrote: %q. Dramatic, self-abasing, a little absurd, with the odd dry aside like the last one. Vary them, keep "+
				"each under fourteen words, and use the name in only a few.\n\n"+
				"Then a journey through what the user browsed since "+from.Format("Monday 15:04")+", newest first: up to eight points, fewer when there was little. Each has a short "+
				"headline of under nine words about a thread of their browsing, a detail of one or two sentences saying what "+
				"they looked at and why it seems to matter, and the url and title of the page that best stands for it. Write to "+
				"them as you, warm and specific, without the devoted voice. Base every detail on these figures per site for the period, "+
				"counted by the browser: visits, distinct pages, first and last time, minutes the page was in front of the user, minutes "+
				"with developer tools open, and its top page titles. A site with many minutes but few pages, such as a video site, "+
				"was watched or kept open; minutes with developer tools mean the user was building or debugging it. Use the figures "+
				"as they are and never make up counts or durations: %s\n\nUse only pages from this history, where visits count within "+
				"the period: %s\n\n%s", name, devotedLines, sitesJSON, historyJSON, suggestionsPrompt(names, titles))
			var reply welcomeReply
			finish := step("welcome", "Writing the welcome page")
			err = PostClaude(ctx, ClaudeCall{Prompt: prompt, Schema: welcomeSchema, Model: model}, &reply)
			finish(err)
			if err != nil {
				return err
			}
			visited := map[string]string{}
			for _, visit := range history {
				visited[visit.URL] = visit.LastVisit
			}
			journey := []JourneyPoint{}
			for _, point := range reply.Journey {
				if at, ok := visited[point.URL]; ok {
					point.VisitedAt = at
					journey = append(journey, point)
				}
			}
			urls := make([]string, 0, len(journey))
			for _, point := range journey {
				urls = append(urls, point.URL)
			}
			var trails map[string][]TrailStep
			if err := CallInto(ctx, "history.trails", map[string]any{"urls": urls, "since": from.UTC().Format(time.RFC3339)}, &trails); err != nil {
				return err
			}
			for i := range journey {
				host := hostOf(journey[i].URL)
				journey[i].Facts = sites[host]
				trail := trails[journey[i].URL]
				seen := map[string]bool{journey[i].URL: true}
				for _, step := range trail {
					seen[step.URL] = true
				}
				long := map[string]float64{}
				first := map[string]FocusSpan{}
				for _, span := range spans {
					if hostOf(span.URL) == host && !seen[span.URL] {
						long[span.URL] += spanMinutes(span)
						if _, ok := first[span.URL]; !ok {
							first[span.URL] = span
						}
					}
				}
				watched := make([]string, 0, len(long))
				for page := range long {
					watched = append(watched, page)
				}
				sort.Slice(watched, func(a, b int) bool { return long[watched[a]] > long[watched[b]] })
				for _, page := range watched[:min(len(watched), 6)] {
					trail = append(trail, TrailStep{URL: page, Title: first[page].Title, VisitedAt: first[page].Start, Way: "seen"})
				}
				journey[i].Trail = trail
			}
			known := map[string]bool{}
			for url := range visited {
				known[url] = true
			}
			value := Welcome{Name: profile.Name, Taglines: append(append([]string{}, devotedLines...), reply.Taglines...), Journey: journey, Suggestions: validSuggestions(reply.Suggestions, known, names), Since: from.UTC().Format(time.RFC3339), RefreshedAt: now()}
			work, err := GetWork(ctx, from)
			if err != nil {
				return err
			}
			type placed struct {
				point JourneyPoint
				from  int
			}
			all := make([]placed, 0, len(journey)+len(work))
			for i, point := range journey {
				all = append(all, placed{point, i})
			}
			sort.SliceStable(work, func(i, j int) bool {
				return len(work[i].Commits)+work[i].Sessions*3+len(work[i].Searches) > len(work[j].Commits)+work[j].Sessions*3+len(work[j].Searches)
			})
			for i, repo := range work {
				if i < 6 {
					all = append(all, placed{workPoint(repo), -1})
				}
			}
			if len(work) > 6 {
				all = append(all, placed{otherWork(work[6:]), -1})
			}
			sort.SliceStable(all, func(i, j int) bool { return all[i].point.VisitedAt > all[j].point.VisitedAt })
			moved := map[int]int{}
			value.Journey = make([]JourneyPoint, 0, len(all))
			for i, entry := range all {
				if entry.from >= 0 {
					moved[entry.from] = i
				}
				value.Journey = append(value.Journey, entry.point)
			}
			remap := func(point int) int {
				if at, ok := moved[point]; ok {
					return at
				}
				return -1
			}
			for i := range value.Suggestions.Groups {
				value.Suggestions.Groups[i].Point = remap(value.Suggestions.Groups[i].Point)
			}
			for i := range value.Suggestions.Spaces {
				value.Suggestions.Spaces[i].Point = remap(value.Suggestions.Spaces[i].Point)
			}
			for i := range value.Suggestions.Rules {
				value.Suggestions.Rules[i].Point = remap(value.Suggestions.Rules[i].Point)
			}
			path, err := homeFile("welcome.json")
			if err != nil {
				return err
			}
			if err := writeJSONFile(path, value); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(stored), 0o755); err != nil {
				return err
			}
			if err := writeJSONFile(stored, value); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": fmt.Sprintf("%d lines, %d journey points", len(value.Taglines), len(value.Journey)), "welcome": value})
		},
	}
	refresh.Flags().StringVar(&model, "model", "", "Claude model, the claude default when empty")
	refresh.Flags().BoolVar(&cached, "cached", false, "Only show the journey already written for this period; refused when there is none")
	refresh.Flags().StringVar(&since, "since", "", "Start the journey here, such as 2026-10-26T09:00, instead of the day's start")
	welcome.AddCommand(refresh)
	return welcome
}

func journeySince(profile Profile, since string) (time.Time, error) {
	if since != "" {
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
			if parsed, err := time.ParseInLocation(layout, since, time.Local); err == nil {
				return parsed, nil
			}
		}
		return time.Time{}, fmt.Errorf("--since takes a time such as 2026-10-26T09:00")
	}
	start := profile.JourneyStart
	if start == "" {
		start = "00:00"
	}
	clock, err := time.Parse("15:04", start)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, time.Local)
	if from.After(now) {
		from = from.AddDate(0, 0, -1)
	}
	return from, nil
}
