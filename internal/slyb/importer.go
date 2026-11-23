package slyb

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

type ImportFolder struct {
	Label string `json:"label"`
	Tabs  []Page `json:"tabs"`
}

type ImportSpace struct {
	Name    string         `json:"name"`
	Icon    string         `json:"icon,omitempty"`
	Tabs    []Page         `json:"tabs"`
	Folders []ImportFolder `json:"folders"`
	Loose   []Page         `json:"loose"`
}

type ImportedSpace struct {
	Space   string `json:"space"`
	Name    string `json:"name"`
	Pinned  int    `json:"pinned"`
	Loose   int    `json:"loose"`
	Folders int    `json:"folders"`
}

type ImportedHistory struct {
	Imported int `json:"imported"`
	Failed   int `json:"failed"`
}

type Imported struct {
	Summary    string          `json:"summary"`
	Source     string          `json:"source"`
	Spaces     []ImportedSpace `json:"spaces"`
	Essentials int             `json:"essentials"`
	History    ImportedHistory `json:"history"`
	Preview    []ImportSpace   `json:"preview,omitempty"`
	Favorites  []Page          `json:"favorites,omitempty"`
}

type historyPage struct {
	URL    string  `json:"url"`
	Title  string  `json:"title"`
	Visits []int64 `json:"visits"`
}

type chromiumNode struct {
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	URL      string         `json:"url"`
	Children []chromiumNode `json:"children"`
}

type chromiumBookmarks struct {
	Roots map[string]chromiumNode `json:"roots"`
}

func collectFolder(node chromiumNode, label string, space *ImportSpace) {
	folder := ImportFolder{Label: label}
	for _, child := range node.Children {
		if child.Type == "url" {
			folder.Tabs = append(folder.Tabs, Page{URL: child.URL, Title: child.Name})
		}
	}
	if len(folder.Tabs) > 0 {
		space.Folders = append(space.Folders, folder)
	}
	for _, child := range node.Children {
		if child.Type == "folder" {
			collectFolder(child, label+" / "+child.Name, space)
		}
	}
}

type DiaProfile struct {
	Dir  string
	Name string
}

func GetDiaProfiles(userData string) ([]DiaProfile, error) {
	var state struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
			ProfilesOrder []string `json:"profiles_order"`
		} `json:"profile"`
	}
	if err := readJSONFile(filepath.Join(userData, "Local State"), &state); err != nil {
		return nil, cli.Rejected("no Dia profiles in "+userData, "pass --user-data with Dia's User Data folder")
	}
	order := state.Profile.ProfilesOrder
	if len(order) == 0 {
		for dir := range state.Profile.InfoCache {
			order = append(order, dir)
		}
	}
	var profiles []DiaProfile
	for _, dir := range order {
		info, ok := state.Profile.InfoCache[dir]
		if !ok {
			continue
		}
		profiles = append(profiles, DiaProfile{Dir: filepath.Join(userData, dir), Name: info.Name})
	}
	return profiles, nil
}

func GetDiaSpace(profile DiaProfile, name string) (ImportSpace, error) {
	space := ImportSpace{Name: name}
	var bookmarks chromiumBookmarks
	if err := readJSONFile(filepath.Join(profile.Dir, "Bookmarks"), &bookmarks); err != nil && !os.IsNotExist(err) {
		return ImportSpace{}, err
	}
	bar := bookmarks.Roots["bookmark_bar"].Children
	if len(bar) == 1 && bar[0].Type == "folder" && strings.EqualFold(bar[0].Name, profile.Name) {
		bar = bar[0].Children
	}
	for _, node := range bar {
		switch node.Type {
		case "url":
			space.Tabs = append(space.Tabs, Page{URL: node.URL, Title: node.Name})
		case "folder":
			collectFolder(node, node.Name, &space)
		}
	}
	for _, key := range []string{"other", "synced"} {
		for _, node := range bookmarks.Roots[key].Children {
			switch node.Type {
			case "url":
				space.Folders = append(space.Folders, ImportFolder{Label: "Other bookmarks", Tabs: []Page{{URL: node.URL, Title: node.Name}}})
			case "folder":
				collectFolder(node, node.Name, &space)
			}
		}
	}
	session, err := GetNewestSession(filepath.Join(profile.Dir, "Sessions"))
	if err != nil {
		return ImportSpace{}, err
	}
	if session != "" {
		open, err := GetSessionTabs(session)
		if err != nil {
			return ImportSpace{}, err
		}
		for _, tab := range open {
			if tab.Pinned {
				space.Tabs = append(space.Tabs, tab.Page)
			} else {
				space.Loose = append(space.Loose, tab.Page)
			}
		}
	}
	return space, nil
}

type arcItem struct {
	ID          string   `json:"id"`
	Title       *string  `json:"title"`
	ChildrenIDs []string `json:"childrenIds"`
	Data        struct {
		Tab *struct {
			SavedURL   string `json:"savedURL"`
			SavedTitle string `json:"savedTitle"`
		} `json:"tab"`
		List *json.RawMessage `json:"list"`
	} `json:"data"`
}

type arcSpace struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	ContainerIDs []json.RawMessage `json:"containerIDs"`
	CustomInfo   struct {
		IconType struct {
			Emoji string `json:"emoji_v2"`
		} `json:"iconType"`
	} `json:"customInfo"`
}

type arcSidebar struct {
	Sidebar struct {
		Containers []struct {
			Spaces  []json.RawMessage `json:"spaces"`
			Items   []json.RawMessage `json:"items"`
			TopApps []json.RawMessage `json:"topAppsContainerIDs"`
		} `json:"containers"`
	} `json:"sidebar"`
}

type ArcImport struct {
	Spaces     []ImportSpace
	Essentials []Page
}

func arcTitle(item arcItem) string {
	if item.Title != nil && *item.Title != "" {
		return *item.Title
	}
	if item.Data.Tab != nil {
		return item.Data.Tab.SavedTitle
	}
	return ""
}

func arcContainer(ids []json.RawMessage, marker string) string {
	for i := 0; i+1 < len(ids); i++ {
		var name string
		if json.Unmarshal(ids[i], &name) == nil && name == marker {
			var id string
			if json.Unmarshal(ids[i+1], &id) == nil {
				return id
			}
		}
	}
	return ""
}

func arcTabs(items map[string]arcItem, id string) []Page {
	var pages []Page
	for _, childID := range items[id].ChildrenIDs {
		child := items[childID]
		if child.Data.Tab != nil {
			pages = append(pages, Page{URL: child.Data.Tab.SavedURL, Title: arcTitle(child)})
			continue
		}
		pages = append(pages, arcTabs(items, childID)...)
	}
	return pages
}

func GetArcSpaces(path string) (ArcImport, error) {
	var sidebar arcSidebar
	if err := readJSONFile(path, &sidebar); err != nil {
		return ArcImport{}, cli.Rejected("no Arc sidebar at "+path, "install Arc, sign in so Arc Sync restores the spaces, quit it, then import; or pass --sidebar with a StorableSidebar.json")
	}
	items := map[string]arcItem{}
	var spaces []arcSpace
	var essentials []Page
	var topApps []json.RawMessage
	for _, container := range sidebar.Sidebar.Containers {
		for _, raw := range container.Items {
			var item arcItem
			if json.Unmarshal(raw, &item) == nil && item.ID != "" {
				items[item.ID] = item
			}
		}
		for _, raw := range container.Spaces {
			var space arcSpace
			if json.Unmarshal(raw, &space) == nil && space.ID != "" {
				spaces = append(spaces, space)
			}
		}
		topApps = append(topApps, container.TopApps...)
	}
	for _, raw := range topApps {
		var id string
		if json.Unmarshal(raw, &id) == nil {
			essentials = append(essentials, arcTabs(items, id)...)
		}
	}
	result := ArcImport{Essentials: essentials}
	for _, space := range spaces {
		target := ImportSpace{Name: space.Title, Icon: space.CustomInfo.IconType.Emoji}
		if target.Name == "" {
			target.Name = "Arc"
		}
		var walk func(id, label string)
		walk = func(id, label string) {
			folder := ImportFolder{Label: label}
			for _, childID := range items[id].ChildrenIDs {
				if child := items[childID]; child.Data.Tab != nil {
					folder.Tabs = append(folder.Tabs, Page{URL: child.Data.Tab.SavedURL, Title: arcTitle(child)})
				}
			}
			if label == "" {
				target.Tabs = append(target.Tabs, folder.Tabs...)
			} else if len(folder.Tabs) > 0 {
				target.Folders = append(target.Folders, folder)
			}
			for _, childID := range items[id].ChildrenIDs {
				if child := items[childID]; child.Data.List != nil {
					name := arcTitle(child)
					if label != "" {
						name = label + " / " + name
					}
					walk(childID, name)
				}
			}
		}
		if pinned := arcContainer(space.ContainerIDs, "pinned"); pinned != "" {
			walk(pinned, "")
		}
		if unpinned := arcContainer(space.ContainerIDs, "unpinned"); unpinned != "" {
			target.Loose = arcTabs(items, unpinned)
		}
		if len(target.Tabs) > 0 || len(target.Folders) > 0 || len(target.Loose) > 0 {
			result.Spaces = append(result.Spaces, target)
		}
	}
	return result, nil
}

func GetChromiumHistory(path string, limit int) ([]historyPage, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	scratch, err := os.MkdirTemp("", "slyb-history-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	for _, suffix := range []string{"", "-wal"} {
		source, err := os.Open(path + suffix)
		if err != nil {
			continue
		}
		target, err := os.Create(filepath.Join(scratch, "History"+suffix))
		if err != nil {
			source.Close()
			return nil, err
		}
		_, err = io.Copy(target, source)
		source.Close()
		target.Close()
		if err != nil {
			return nil, err
		}
	}
	query := "SELECT u.url AS url, u.title AS title, " +
		"(SELECT group_concat(t, ',') FROM (SELECT v.visit_time / 1000 - 11644473600000 AS t FROM visits v WHERE v.url = u.id ORDER BY v.visit_time DESC LIMIT 50)) AS visits " +
		"FROM urls u WHERE u.hidden = 0 AND (u.url LIKE 'http://%' OR u.url LIKE 'https://%') ORDER BY u.last_visit_time DESC LIMIT " + strconv.Itoa(limit)
	out, err := exec.Command("sqlite3", "-readonly", "-json", filepath.Join(scratch, "History"), query).Output()
	if err != nil {
		return nil, &cli.Error{Code: cli.ExitFailure, Message: "sqlite3 could not read " + path, Cause: err}
	}
	var rows []struct {
		URL    string  `json:"url"`
		Title  string  `json:"title"`
		Visits *string `json:"visits"`
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, err
		}
	}
	pages := make([]historyPage, 0, len(rows))
	for _, row := range rows {
		page := historyPage{URL: row.URL, Title: row.Title}
		if row.Visits != nil {
			for _, value := range strings.Split(*row.Visits, ",") {
				if at, err := strconv.ParseInt(value, 10, 64); err == nil {
					page.Visits = append(page.Visits, at)
				}
			}
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func PostImport(cmd *cobra.Command, source string, spaces []ImportSpace, essentials []Page, history []historyPage, withHistory, plan bool) (Imported, error) {
	ctx := cmd.Context()
	result := Imported{Source: source, Spaces: []ImportedSpace{}}
	if withHistory {
		result.History.Imported = len(history)
	}
	if plan {
		for _, space := range spaces {
			result.Spaces = append(result.Spaces, ImportedSpace{Name: space.Name, Pinned: len(space.Tabs), Loose: len(space.Loose), Folders: len(space.Folders)})
		}
		result.Essentials = len(essentials)
		result.Preview, result.Favorites = spaces, essentials
		result.Summary = "would import " + importSummary(result)
		return result, nil
	}
	if len(essentials) > 0 {
		finish := step("essentials", "Importing favorites as essentials")
		var made struct {
			Essentials int `json:"essentials"`
		}
		err := CallInto(ctx, "import.essentials", map[string]any{"pages": essentials}, &made)
		finish(err)
		if err != nil {
			return Imported{}, err
		}
		result.Essentials = made.Essentials
	}
	store, err := GetGroups()
	if err != nil {
		return Imported{}, err
	}
	for _, space := range spaces {
		finish := step("space-"+space.Name, "Importing "+space.Name)
		var made ImportedSpace
		err := CallInto(ctx, "import.space", map[string]any{"name": space.Name, "icon": space.Icon, "tabs": space.Tabs, "loose": space.Loose}, &made)
		finish(err)
		if err != nil {
			return Imported{}, err
		}
		for _, folder := range space.Folders {
			parts := strings.Split(folder.Label, " / ")
			target := store.GroupTitled(made.Space, parts[0])
			if target == nil {
				target = store.PostGroup(made.Space, parts[0], false)
				made.Folders++
			}
			label, kind := "", KindSection
			if len(parts) > 1 {
				label, kind = strings.Join(parts[1:], " / "), KindDivider
			}
			for _, page := range folder.Tabs {
				target.Add(label, kind, GroupPage{URL: page.URL, Title: page.Title, Source: SourceImport, Verified: true})
			}
		}
		result.Spaces = append(result.Spaces, made)
	}
	if err := PutGroups(ctx, store); err != nil {
		return Imported{}, err
	}
	result.History.Imported = 0
	if withHistory && len(history) > 0 {
		finish := step("history", fmt.Sprintf("Importing %d visited pages", len(history)))
		for start := 0; start < len(history); start += 500 {
			end := min(start+500, len(history))
			var batch ImportedHistory
			if err := CallInto(ctx, "history.import", map[string]any{"pages": history[start:end]}, &batch); err != nil {
				finish(err)
				return Imported{}, err
			}
			result.History.Imported += batch.Imported
			result.History.Failed += batch.Failed
		}
		finish(nil)
	}
	result.Summary = importSummary(result)
	return result, nil
}

func importSummary(result Imported) string {
	pinned, loose, folders := 0, 0, 0
	for _, space := range result.Spaces {
		pinned += space.Pinned
		loose += space.Loose
		folders += space.Folders
	}
	return fmt.Sprintf("%d spaces, %d essentials, %d pinned, %d open tabs, %d groups, %d history pages", len(result.Spaces), result.Essentials, pinned, loose, folders, result.History.Imported)
}

func NewImportCommand() *cobra.Command {
	importer := &cobra.Command{Use: "just import", Short: "One time import of Arc's or Dia's spaces, essentials, pinned tabs, folders and history into Zen"}

	var withHistory, plan bool
	var limit int
	var diaUserData string
	var renames []string
	dia := &cobra.Command{
		Use:   "dia",
		Short: "Copy every Dia space, its bookmarks, open tabs and history, into a Zen space of the same name",
		Long: "Each Dia space is a Chromium profile under Dia's User Data, named in its Local State. Every one becomes a Zen\n" +
			"space of that name, or the name --rename gives it: its bookmark folders become groups, nested folders\n" +
			"becoming sections, loose bookmarks pinned tabs, the open tabs of its newest session unloaded tabs, and its\n" +
			"history goes into Zen's. A bookmarks bar holding only one folder named like the space is unwrapped. Running\n" +
			"it again adds only what is missing. Dia can stay open.",
		Example: "just import dia --plan\njust import dia --rename Work=Office",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if diaUserData == "" {
				userHome, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				diaUserData = filepath.Join(userHome, "Library", "Application Support", "Dia", "User Data")
			}
			names := map[string]string{}
			for _, rename := range renames {
				from, to, ok := strings.Cut(rename, "=")
				if !ok || from == "" || to == "" {
					return cli.Rejected("--rename takes DIA=ZEN, such as Work=Office", "")
				}
				names[from] = to
			}
			profiles, err := GetDiaProfiles(diaUserData)
			if err != nil {
				return err
			}
			var spaces []ImportSpace
			var history []historyPage
			for _, profile := range profiles {
				name := profile.Name
				if renamed, ok := names[name]; ok {
					name = renamed
				}
				space, err := GetDiaSpace(profile, name)
				if err != nil {
					return err
				}
				spaces = append(spaces, space)
				visits, err := GetChromiumHistory(filepath.Join(profile.Dir, "History"), limit)
				if err != nil {
					return err
				}
				history = append(history, visits...)
			}
			result, err := PostImport(cmd, "dia", spaces, nil, history, withHistory, plan)
			if err != nil {
				return err
			}
			return printValue(cmd, result)
		},
	}
	dia.Flags().StringVar(&diaUserData, "user-data", "", "Dia's User Data folder, the installed Dia's when empty")
	dia.Flags().StringArrayVar(&renames, "rename", nil, "Map a Dia space to a Zen space name, DIA=ZEN, repeatable")

	var arcSidebarPath, arcProfile string
	arc := &cobra.Command{
		Use:   "arc",
		Short: "Import everything Arc keeps: spaces, favorites, pinned tabs, folders, open tabs and history",
		Long: "Read Arc's StorableSidebar.json and History: favorites become Zen essentials, every space becomes a Zen space\n" +
			"with its emoji, pinned tabs stay pinned, folders become folders with nested ones named Parent / Child, the\n" +
			"open Today tabs come over unloaded, and the history goes into Zen's. Folders made by the import count as the\n" +
			"user's own, so Sort never changes them. Running it again adds only what is missing.",
		Example: "just import arc --plan",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			base := filepath.Join(userHome, "Library", "Application Support", "Arc")
			if arcSidebarPath == "" {
				arcSidebarPath = filepath.Join(base, "StorableSidebar.json")
			}
			if arcProfile == "" {
				arcProfile = filepath.Join(base, "User Data", "Default")
			}
			sidebar, err := GetArcSpaces(arcSidebarPath)
			if err != nil {
				return err
			}
			history, err := GetChromiumHistory(filepath.Join(arcProfile, "History"), limit)
			if err != nil {
				return err
			}
			result, err := PostImport(cmd, "arc", sidebar.Spaces, sidebar.Essentials, history, withHistory, plan)
			if err != nil {
				return err
			}
			return printValue(cmd, result)
		},
	}
	arc.Flags().StringVar(&arcSidebarPath, "sidebar", "", "Path to StorableSidebar.json, Arc's own when empty")
	arc.Flags().StringVar(&arcProfile, "profile-dir", "", "Arc profile folder, User Data/Default when empty")

	for _, command := range []*cobra.Command{dia, arc} {
		command.Flags().BoolVar(&withHistory, "history", true, "Import the browsing history too")
		command.Flags().IntVar(&limit, "limit", 50000, "Most visited pages to import")
		command.Flags().BoolVar(&plan, "plan", false, "Print what would be imported and change nothing")
	}
	importer.AddCommand(dia, arc)
	return importer
}
