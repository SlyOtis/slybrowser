package slyb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nimling/nimbody/cli"
	"github.com/nimling/nimbody/stream"
	"github.com/spf13/cobra"
)

const (
	SourceYou    = "you"
	SourceClaude = "claude"
	SourceImport = "import"
	KindSection  = "section"
	KindDivider  = "divider"
	SortRelevant = "relevance"
	SortDate     = "date"
	SortUsed     = "used"
	SortManual   = "manual"
)

type GroupPage struct {
	URL      string `json:"url"`
	Title    string `json:"title"`
	Source   string `json:"source"`
	Verified bool   `json:"verified"`
	AddedAt  string `json:"added_at"`
	Rule     string `json:"rule,omitempty"`
	RuleName string `json:"rule_name,omitempty"`
}

type GroupSection struct {
	ID    string      `json:"id"`
	Label string      `json:"label"`
	Kind  string      `json:"kind"`
	Pages []GroupPage `json:"pages"`
}

type Group struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Shelf     string         `json:"shelf"`
	Icon      string         `json:"icon"`
	Color     string         `json:"color"`
	Space     string         `json:"space"`
	Claude    bool           `json:"claude"`
	Sort      string         `json:"sort"`
	Project   string         `json:"project,omitempty"`
	SpaceID   string         `json:"space_id"`
	Login     string         `json:"login,omitempty"`
	Used      int            `json:"used,omitempty"`
	UsedAt    string         `json:"used_at,omitempty"`
	CreatedAt string         `json:"created_at"`
	Sections  []GroupSection `json:"sections"`
}

type SortRecord struct {
	At      string `json:"at"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Group   string `json:"group,omitempty"`
	Section string `json:"section,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type Space struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Icon      string       `json:"icon"`
	Workspace string       `json:"workspace"`
	Sort      string       `json:"sort,omitempty"`
	CreatedAt string       `json:"created_at"`
	History   []SortRecord `json:"history"`
	Discarded []SortRecord `json:"discarded"`
}

type GroupStore struct {
	Groups []Group `json:"groups"`
	Spaces []Space `json:"spaces"`
}

func newID() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func groupsPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "groups.json"), nil
}

func GetGroups() (GroupStore, error) {
	path, err := groupsPath()
	if err != nil {
		return GroupStore{}, err
	}
	store := GroupStore{Groups: []Group{}, Spaces: []Space{}}
	if err := readJSONFile(path, &store); err != nil && !os.IsNotExist(err) {
		return GroupStore{}, err
	}
	if store.Spaces == nil {
		store.Spaces = []Space{}
	}
	return store, nil
}

func PutGroups(ctx context.Context, store GroupStore) error {
	path, err := groupsPath()
	if err != nil {
		return err
	}
	if err := writeJSONFile(path, store); err != nil {
		return err
	}
	if _, err := Call(ctx, "groups.reload", nil); err != nil && !notRunning(err) {
		return err
	}
	return nil
}

func (store *GroupStore) Group(id string) (*Group, error) {
	for i := range store.Groups {
		if store.Groups[i].ID == id {
			return &store.Groups[i], nil
		}
	}
	return nil, cli.Rejected("no group with id "+id, "read the ids with `slyb group get`")
}

func (store *GroupStore) GroupTitled(space, title string) *Group {
	for i := range store.Groups {
		if store.Groups[i].Space == space && strings.EqualFold(store.Groups[i].Title, title) {
			return &store.Groups[i]
		}
	}
	return nil
}

func (store *GroupStore) Contains(url string) bool {
	for _, group := range store.Groups {
		for _, section := range group.Sections {
			for _, page := range section.Pages {
				if page.URL == url {
					return true
				}
			}
		}
	}
	return false
}

func (group *Group) Section(label, kind string) *GroupSection {
	for i := range group.Sections {
		if group.Sections[i].Label == label && group.Sections[i].Kind == kind {
			return &group.Sections[i]
		}
	}
	group.Sections = append(group.Sections, GroupSection{ID: newID(), Label: label, Kind: kind, Pages: []GroupPage{}})
	return &group.Sections[len(group.Sections)-1]
}

func (group *Group) Add(sectionLabel, kind string, page GroupPage) bool {
	for _, section := range group.Sections {
		for _, existing := range section.Pages {
			if existing.URL == page.URL {
				return false
			}
		}
	}
	if page.AddedAt == "" {
		page.AddedAt = now()
	}
	section := group.Section(sectionLabel, kind)
	section.Pages = append(section.Pages, page)
	return true
}

func (store *GroupStore) PostGroup(space, title string, claude bool) *Group {
	store.Groups = append(store.Groups, Group{
		ID:        newID(),
		Title:     title,
		Space:     space,
		SpaceID:   store.DefaultSpace(space).ID,
		Claude:    claude,
		Sort:      SortRelevant,
		CreatedAt: now(),
		Sections:  []GroupSection{},
	})
	return &store.Groups[len(store.Groups)-1]
}

type GroupSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Shelf      string `json:"shelf"`
	Space      string `json:"space"`
	SpaceID    string `json:"space_id"`
	Claude     bool   `json:"claude"`
	Sort       string `json:"sort"`
	Sections   int    `json:"sections"`
	Pages      int    `json:"pages"`
	Unverified int    `json:"unverified"`
}

func summarizeGroup(group Group) GroupSummary {
	summary := GroupSummary{ID: group.ID, Title: group.Title, Shelf: group.Shelf, Space: group.Space, SpaceID: group.SpaceID, Claude: group.Claude, Sort: group.Sort, Sections: len(group.Sections)}
	for _, section := range group.Sections {
		for _, page := range section.Pages {
			summary.Pages++
			if !page.Verified {
				summary.Unverified++
			}
		}
	}
	return summary
}

func activeSpace(ctx context.Context, space string) (string, error) {
	if space != "" {
		return space, nil
	}
	var current status
	if err := CallInto(ctx, "status", nil, &current); err != nil {
		return "", err
	}
	return current.Space, nil
}

func NewGroupCommand() *cobra.Command {
	group := &cobra.Command{Use: "group", Short: "Read, make and change the groups that cards show: sections, dividers and pages"}

	var getSpace string
	var detailed bool
	get := &cobra.Command{
		Use:     "get [group_id]",
		Short:   "List groups with their page counts, or show one group with its sections and pages",
		Example: "slyb group get\nslyb group get 3f9a2c1b7d4e",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				found, err := store.Group(args[0])
				if err != nil {
					return err
				}
				return printValue(cmd, found)
			}
			list := []any{}
			for _, entry := range store.Groups {
				if getSpace != "" && entry.Space != getSpace {
					continue
				}
				if detailed {
					list = append(list, entry)
				} else {
					list = append(list, summarizeGroup(entry))
				}
			}
			return printValue(cmd, list)
		},
	}
	get.Flags().StringVar(&getSpace, "workspace", "", "Only groups in this workspace id")
	get.Flags().BoolVar(&detailed, "detailed", false, "Print every section and page")

	var createSpace, createSidebar, ask, model string
	var fromFolders bool
	create := &cobra.Command{
		Use:   "create [title]",
		Short: "Make a group by title, by asking Claude, or from the space's Zen folders",
		Long: "With a title, make an empty group. With --ask, Claude reads the open tabs, the browsing history, the Claude\n" +
			"Code sessions' pages, the existing groups and the findings, and builds the group the instruction describes,\n" +
			"with a labeled section for each part, for example one per environment. With --from-folders, every Zen folder\n" +
			"in the space becomes a group, nested folders becoming dividers.",
		Example:     "slyb group create --ask \"tst and prd pages, a section per environment\"\nslyb group create Reading\nslyb group create GitHub --space Work --workspace {uuid}",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			space, err := activeSpace(ctx, createSpace)
			if err != nil {
				return err
			}
			if createSidebar != "" && (fromFolders || ask != "") {
				return cli.Rejected("--space goes with a title", "slyb group create Reading --space Work")
			}
			switch {
			case fromFolders:
				return postGroupsFromFolders(cmd, space)
			case ask != "":
				return postGroupAsk(cmd, space, ask, model)
			case len(args) == 1:
				store, err := GetGroups()
				if err != nil {
					return err
				}
				sidebarID := ""
				if createSidebar != "" {
					sidebar := store.SpaceOf(createSidebar)
					if sidebar == nil || sidebar.Workspace != space {
						sidebar = store.SpaceNamed(space, createSidebar)
					}
					if sidebar == nil {
						sidebar = store.PostSpace(space, createSidebar)
					}
					sidebarID = sidebar.ID
				}
				made := store.PostGroup(space, args[0], false)
				if sidebarID != "" {
					made.SpaceID = sidebarID
				}
				summary := summarizeGroup(*made)
				if err := PutGroups(ctx, store); err != nil {
					return err
				}
				return printValue(cmd, summary)
			default:
				return cli.Rejected("name the group, or pass --ask or --from-folders", "slyb group create Reading")
			}
		},
	}
	create.Flags().StringVar(&createSpace, "workspace", "", "Workspace id, the active workspace when empty")
	create.Flags().StringVar(&createSidebar, "space", "", "Sidebar space id or name in the workspace to put the group in, made when no space has the name; the workspace's first space when empty")
	create.Flags().StringVar(&ask, "ask", "", "What the group should hold, in plain language, for Claude to build")
	create.Flags().StringVar(&model, "model", "", "Claude model for --ask, the claude default when empty")
	create.Flags().BoolVar(&fromFolders, "from-folders", false, "Turn every Zen folder of the space into a group")

	var title, sortBy, shelf, icon, color, login, moveSpace string
	update := &cobra.Command{
		Use:     "update <group_id>",
		Short:   "Rename a group, move it to another space, put it on a shelf, give it an icon or a separate login, or change how its pages sort",
		Example: "slyb group update 3f9a2c1b7d4e --shelf Work --sort used\nslyb group update 3f9a2c1b7d4e --space Library",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != "" && sortBy != SortRelevant && sortBy != SortDate && sortBy != SortUsed {
				return cli.Rejected("--sort takes relevance, date or used", "")
			}
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found, err := store.Group(args[0])
			if err != nil {
				return err
			}
			if title != "" {
				found.Title = title
			}
			if sortBy != "" {
				found.Sort = sortBy
			}
			if cmd.Flags().Changed("shelf") {
				found.Shelf = shelf
			}
			if cmd.Flags().Changed("icon") {
				found.Icon = icon
			}
			if cmd.Flags().Changed("color") {
				found.Color = color
			}
			if cmd.Flags().Changed("login") {
				found.Login = strings.TrimSpace(login)
			}
			if moveSpace != "" {
				sidebar := store.SpaceOf(moveSpace)
				if sidebar == nil || sidebar.Workspace != found.Space {
					sidebar = store.SpaceNamed(found.Space, moveSpace)
				}
				if sidebar == nil {
					sidebar = store.PostSpace(found.Space, moveSpace)
				}
				found.SpaceID = sidebar.ID
			}
			summary := summarizeGroup(*found)
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	update.Flags().StringVar(&title, "title", "", "New title")
	update.Flags().StringVar(&sortBy, "sort", "", "relevance, date or used")
	update.Flags().StringVar(&shelf, "shelf", "", "The shelf heading the card sits under; empty puts it at the top")
	update.Flags().StringVar(&icon, "icon", "", "A site origin whose favicon to show, an emoji, or empty for the most visited site no other group uses")
	update.Flags().StringVar(&login, "login", "", "Give the group a separate login of its own under this name, so its pages keep their own sessions; empty uses the workspace login")
	update.Flags().StringVar(&moveSpace, "space", "", "Sidebar space id or name in the group's workspace to move it to, made when no space has the name")
	update.Flags().StringVar(&color, "color", "", "Theme color while one of its pages is open: red, purple, blue, green, turquoise, yellow, orange, pink or #rrggbb; empty follows the favicon")

	remove := &cobra.Command{
		Use:     "delete <group_id>",
		Short:   "Delete a group and its pages; open tabs stay open",
		Example: "slyb group delete 3f9a2c1b7d4e",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found, err := store.Group(args[0])
			if err != nil {
				return err
			}
			summary := summarizeGroup(*found)
			kept := store.Groups[:0]
			for _, entry := range store.Groups {
				if entry.ID != args[0] {
					kept = append(kept, entry)
				}
			}
			store.Groups = kept
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}

	group.AddCommand(get, create, update, remove, NewGroupPageCommand(), NewGroupSimilarCommand())
	return group
}

func NewGroupPageCommand() *cobra.Command {
	page := &cobra.Command{Use: "page", Short: "Add, verify and remove the pages of a group"}

	var section, divider, pageTitle string
	var unverified bool
	create := &cobra.Command{
		Use:     "create <group_id> <url>",
		Short:   "Add a page to a group, in a labeled section or under a divider",
		Example: "slyb group page create 3f9a2c1b7d4e https://app.tst.example.com --section tst",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found, err := store.Group(args[0])
			if err != nil {
				return err
			}
			label, kind := section, KindSection
			if divider != "" {
				label, kind = divider, KindDivider
			}
			source := SourceYou
			if stream.Machine() {
				source = SourceClaude
			}
			if !found.Add(label, kind, GroupPage{URL: args[1], Title: pageTitle, Source: source, Verified: !unverified}) {
				return cli.Rejected(args[1]+" is already in "+found.Title, "")
			}
			summary := summarizeGroup(*found)
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	create.Flags().StringVar(&section, "section", "", "Section label, the group's first part when empty")
	create.Flags().StringVar(&divider, "divider", "", "Put the page under this divider instead of a section")
	create.Flags().StringVar(&pageTitle, "title", "", "Page title")
	create.Flags().BoolVar(&unverified, "unverified", false, "Add it as unverified, for the user to keep or dismiss")

	verify := &cobra.Command{
		Use:     "update <group_id> <url>",
		Short:   "Mark a page of a group verified, as opening it from the card does",
		Example: "slyb group page update 3f9a2c1b7d4e https://app.tst.example.com --verified on",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			verified, err := onOff(cmd, "verified")
			if err != nil {
				return err
			}
			if verified == nil {
				return cli.Rejected("pass --verified on or off", "")
			}
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found, err := store.Group(args[0])
			if err != nil {
				return err
			}
			changed := false
			for i := range found.Sections {
				for j := range found.Sections[i].Pages {
					if found.Sections[i].Pages[j].URL == args[1] {
						found.Sections[i].Pages[j].Verified = *verified
						changed = true
					}
				}
			}
			if !changed {
				return cli.Rejected(args[1]+" is not in "+found.Title, "")
			}
			summary := summarizeGroup(*found)
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	verify.Flags().String("verified", "", "on or off")

	remove := &cobra.Command{
		Use:     "delete <group_id> <url>",
		Short:   "Remove a page from a group, as the card's close button does",
		Example: "slyb group page delete 3f9a2c1b7d4e https://app.tst.example.com",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found, err := store.Group(args[0])
			if err != nil {
				return err
			}
			removed := false
			for i := range found.Sections {
				kept := found.Sections[i].Pages[:0]
				for _, entry := range found.Sections[i].Pages {
					if entry.URL == args[1] {
						removed = true
						continue
					}
					kept = append(kept, entry)
				}
				found.Sections[i].Pages = kept
			}
			if !removed {
				return cli.Rejected(args[1]+" is not in "+found.Title, "")
			}
			summary := summarizeGroup(*found)
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}

	page.AddCommand(create, verify, remove)
	return page
}

func postGroupsFromFolders(cmd *cobra.Command, space string) error {
	ctx := cmd.Context()
	var folders []Folder
	if err := CallInto(ctx, "folder.get", map[string]any{"space": space}, &folders); err != nil {
		return err
	}
	var tabs []Tab
	if err := CallInto(ctx, "tab.get", map[string]any{"space": space}, &tabs); err != nil {
		return err
	}
	byID := map[string]Tab{}
	for _, tab := range tabs {
		byID[tab.ID] = tab
	}
	store, err := GetGroups()
	if err != nil {
		return err
	}
	made, pages := 0, 0
	for _, folder := range folders {
		parts := strings.Split(folder.Label, " / ")
		title := strings.TrimSpace(parts[0])
		target := store.GroupTitled(space, title)
		if target == nil {
			target = store.PostGroup(space, title, false)
			made++
		}
		label, kind := "", KindSection
		if len(parts) > 1 {
			label, kind = strings.Join(parts[1:], " / "), KindDivider
		}
		for _, id := range folder.Tabs {
			tab, ok := byID[id]
			if !ok || !strings.HasPrefix(tab.URL, "http") {
				continue
			}
			if target.Add(label, kind, GroupPage{URL: tab.URL, Title: tab.Title, Source: SourceImport, Verified: true}) {
				pages++
			}
		}
	}
	if err := PutGroups(ctx, store); err != nil {
		return err
	}
	return printValue(cmd, map[string]any{"summary": fmt.Sprintf("%d groups, %d pages from %d folders", made, pages, len(folders)), "groups": made, "pages": pages})
}

func (store *GroupStore) SpaceNamed(workspace, name string) *Space {
	for i := range store.Spaces {
		if store.Spaces[i].Workspace == workspace && strings.EqualFold(store.Spaces[i].Name, name) {
			return &store.Spaces[i]
		}
	}
	return nil
}

func (store *GroupStore) PostSpace(workspace, name string) *Space {
	store.Spaces = append(store.Spaces, Space{ID: newID(), Name: name, Workspace: workspace, CreatedAt: now(), History: []SortRecord{}, Discarded: []SortRecord{}})
	return &store.Spaces[len(store.Spaces)-1]
}

func (store *GroupStore) DefaultSpace(workspace string) *Space {
	for i := range store.Spaces {
		if store.Spaces[i].Workspace == workspace {
			return &store.Spaces[i]
		}
	}
	return store.PostSpace(workspace, "Space")
}

func (store *GroupStore) SpaceOf(id string) *Space {
	for i := range store.Spaces {
		if store.Spaces[i].ID == id {
			return &store.Spaces[i]
		}
	}
	return nil
}

type SpaceSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Icon      string `json:"icon"`
	Workspace string `json:"workspace"`
	Sort      string `json:"sort"`
	Groups    int    `json:"groups"`
	CreatedAt string `json:"created_at"`
}

func (store *GroupStore) summarizeSpace(space Space) SpaceSummary {
	summary := SpaceSummary{ID: space.ID, Name: space.Name, Icon: space.Icon, Workspace: space.Workspace, Sort: space.Sort, CreatedAt: space.CreatedAt}
	if summary.Sort == "" {
		summary.Sort = SortUsed
	}
	for _, group := range store.Groups {
		if group.SpaceID == space.ID {
			summary.Groups++
		}
	}
	return summary
}

func NewSpaceCommand() *cobra.Command {
	space := &cobra.Command{Use: "space", Short: "Read, make, rename, remove and build the sidebar's spaces, the named parts of a workspace that hold its groups"}

	var getWorkspace string
	get := &cobra.Command{
		Use:     "get [space_id]",
		Short:   "List the sidebar spaces with their workspace and group count, or show one space with its sort history and discarded pages",
		Example: "slyb space get --workspace {uuid}\nslyb space get 3f9a2c1b7d4e",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				found := store.SpaceOf(args[0])
				if found == nil {
					return cli.Rejected("no space with id "+args[0], "read the ids with `slyb space get`")
				}
				return printValue(cmd, found)
			}
			list := []SpaceSummary{}
			for _, entry := range store.Spaces {
				if getWorkspace == "" || entry.Workspace == getWorkspace {
					list = append(list, store.summarizeSpace(entry))
				}
			}
			return printValue(cmd, list)
		},
	}
	get.Flags().StringVar(&getWorkspace, "workspace", "", "Only the spaces of this workspace id")

	var createWorkspace, createIcon string
	create := &cobra.Command{
		Use:     "create <name>",
		Short:   "Make an empty sidebar space in a workspace",
		Example: "slyb space create Work --workspace {uuid}",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			workspace, err := activeSpace(ctx, createWorkspace)
			if err != nil {
				return err
			}
			store, err := GetGroups()
			if err != nil {
				return err
			}
			if store.SpaceNamed(workspace, args[0]) != nil {
				return cli.Rejected("the workspace already has a space named "+args[0], "read them with `slyb space get --workspace "+workspace+"`")
			}
			made := store.PostSpace(workspace, strings.TrimSpace(args[0]))
			made.Icon = createIcon
			summary := store.summarizeSpace(*made)
			if err := PutGroups(ctx, store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	create.Flags().StringVar(&createWorkspace, "workspace", "", "Workspace id, the active workspace when empty")
	create.Flags().StringVar(&createIcon, "icon", "", "A site origin whose favicon to show, or an emoji")

	var name, icon, sortBy string
	update := &cobra.Command{
		Use:     "update <space_id>",
		Short:   "Rename a sidebar space, change its icon, or set its groups to sort by use or as arranged",
		Example: "slyb space update 3f9a2c1b7d4e --name Work --sort manual",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sortBy != "" && sortBy != SortUsed && sortBy != SortManual {
				return cli.Rejected("--sort takes used or manual", "")
			}
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found := store.SpaceOf(args[0])
			if found == nil {
				return cli.Rejected("no space with id "+args[0], "read the ids with `slyb space get`")
			}
			if name = strings.TrimSpace(name); name != "" {
				if other := store.SpaceNamed(found.Workspace, name); other != nil && other.ID != found.ID {
					return cli.Rejected("the workspace already has a space named "+name, "")
				}
				found.Name = name
			}
			if cmd.Flags().Changed("icon") {
				found.Icon = icon
			}
			switch sortBy {
			case SortUsed:
				found.Sort = ""
			case SortManual:
				found.Sort = SortManual
			}
			summary := store.summarizeSpace(*found)
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	update.Flags().StringVar(&name, "name", "", "New name")
	update.Flags().StringVar(&icon, "icon", "", "A site origin whose favicon to show, an emoji, or empty for none")
	update.Flags().StringVar(&sortBy, "sort", "", "used puts the most used and most recently used groups first, manual keeps them as arranged")

	var into string
	remove := &cobra.Command{
		Use:     "delete <space_id>",
		Short:   "Remove a sidebar space; its groups move to another space of the same workspace",
		Example: "slyb space delete 3f9a2c1b7d4e --into 9c2e7a1f4b3d",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetGroups()
			if err != nil {
				return err
			}
			found := store.SpaceOf(args[0])
			if found == nil {
				return cli.Rejected("no space with id "+args[0], "read the ids with `slyb space get`")
			}
			summary := store.summarizeSpace(*found)
			target := ""
			for _, entry := range store.Spaces {
				if entry.ID != found.ID && entry.Workspace == found.Workspace && (into == "" || entry.ID == into) {
					target = entry.ID
					break
				}
			}
			if into != "" && target == "" {
				return cli.Rejected(into+" is not another space of the same workspace", "read them with `slyb space get --workspace "+found.Workspace+"`")
			}
			if target == "" && summary.Groups > 0 {
				return cli.Rejected("the space holds the workspace's only groups; make another space first or delete the groups", "slyb space create <name> --workspace "+found.Workspace)
			}
			for i := range store.Groups {
				if store.Groups[i].SpaceID == found.ID {
					store.Groups[i].SpaceID = target
				}
			}
			kept := store.Spaces[:0]
			for _, entry := range store.Spaces {
				if entry.ID != args[0] {
					kept = append(kept, entry)
				}
			}
			store.Spaces = kept
			if err := PutGroups(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, summary)
		},
	}
	remove.Flags().StringVar(&into, "into", "", "The space id its groups move to, the workspace's first other space when empty")

	space.AddCommand(get, create, update, remove, NewSpaceBuildCommand())
	return space
}
