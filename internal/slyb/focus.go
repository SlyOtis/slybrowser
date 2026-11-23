package slyb

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FocusSpan struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Space    string `json:"space"`
	DevTools bool   `json:"devtools"`
}

type SiteFacts struct {
	Host     string   `json:"host"`
	Visits   int      `json:"visits"`
	Pages    int      `json:"pages"`
	First    string   `json:"first,omitempty"`
	Last     string   `json:"last,omitempty"`
	Minutes  int      `json:"minutes"`
	DevTools int      `json:"devtools_minutes"`
	Top      []string `json:"top"`
}

func GetFocus(from time.Time) ([]FocusSpan, error) {
	dir, err := homeFile("focus")
	if err != nil {
		return nil, err
	}
	var spans []FocusSpan
	for day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local); !day.After(time.Now()); day = day.AddDate(0, 0, 1) {
		file, err := os.Open(filepath.Join(dir, day.Format("2006-01-02")+".jsonl"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var span FocusSpan
			if json.Unmarshal(scanner.Bytes(), &span) == nil && span.Start >= from.UTC().Format(time.RFC3339) {
				spans = append(spans, span)
			}
		}
		file.Close()
	}
	return spans, nil
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

func spanMinutes(span FocusSpan) float64 {
	start, err1 := time.Parse(time.RFC3339, span.Start)
	end, err2 := time.Parse(time.RFC3339, span.End)
	if err1 != nil || err2 != nil {
		return 0
	}
	return end.Sub(start).Minutes()
}

func GetSiteFacts(history []Visit, spans []FocusSpan) map[string]*SiteFacts {
	sites := map[string]*SiteFacts{}
	pages := map[string]map[string]bool{}
	weight := map[string]map[string]float64{}
	titles := map[string]string{}
	site := func(host string) *SiteFacts {
		if sites[host] == nil {
			sites[host] = &SiteFacts{Host: host, Top: []string{}}
			pages[host] = map[string]bool{}
			weight[host] = map[string]float64{}
		}
		return sites[host]
	}
	devtools := map[string]float64{}
	minutes := map[string]float64{}
	stretch := func(facts *SiteFacts, at string) {
		if at == "" {
			return
		}
		if facts.First == "" || at < facts.First {
			facts.First = at
		}
		if at > facts.Last {
			facts.Last = at
		}
	}
	for _, visit := range history {
		host := hostOf(visit.URL)
		if host == "" {
			continue
		}
		facts := site(host)
		facts.Visits += visit.Visits
		pages[host][visit.URL] = true
		weight[host][visit.URL] += float64(visit.Visits)
		titles[visit.URL] = visit.Title
		stretch(facts, visit.LastVisit)
	}
	for _, span := range spans {
		host := hostOf(span.URL)
		if host == "" {
			continue
		}
		facts := site(host)
		length := spanMinutes(span)
		minutes[host] += length
		if span.DevTools {
			devtools[host] += length
		}
		pages[host][span.URL] = true
		weight[host][span.URL] += length * 2
		if span.Title != "" {
			titles[span.URL] = span.Title
		}
		stretch(facts, span.Start)
		stretch(facts, span.End)
	}
	for host, facts := range sites {
		facts.Pages = len(pages[host])
		facts.Minutes = int(minutes[host] + 0.5)
		facts.DevTools = int(devtools[host] + 0.5)
		ranked := make([]string, 0, len(weight[host]))
		for page := range weight[host] {
			ranked = append(ranked, page)
		}
		sort.Slice(ranked, func(i, j int) bool { return weight[host][ranked[i]] > weight[host][ranked[j]] })
		seen := map[string]bool{}
		for _, page := range ranked {
			title := strings.TrimSpace(titles[page])
			if title == "" || seen[title] || len(facts.Top) == 5 {
				continue
			}
			seen[title] = true
			facts.Top = append(facts.Top, title)
		}
	}
	return sites
}
