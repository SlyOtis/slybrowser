package slyb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/google/uuid"
	"github.com/nimling/nimbody/cli"
	"github.com/nimling/nimflow/exec"
	"github.com/nimling/nimflow/graph"
	"github.com/nimling/nimflow/node"
	"github.com/nimling/nimflow/script"
	"github.com/spf13/cobra"
	"golang.org/x/net/publicsuffix"
)

const (
	TriggerTabOpened    = "tab.opened"
	TriggerTabClosed    = "tab.closed"
	TriggerTabSelected  = "tab.selected"
	TriggerPageLoaded   = "page.loaded"
	TriggerPageOutside  = "page.outside"
	TriggerInterval     = "interval"
	TriggerOrganizeDone = "organize.done"

	ActionActive = "active"
	ActionPaused = "paused"

	ScopeWorkspace = "workspace"

	NodeTrigger     = "trigger"
	NodePagePlace   = "page.place"
	NodePageContent = "page.content"
	NodeGitHubItems = "github.items"
	NodeLivePut     = "live.put"
	NodeLiveTitle   = "live.title"
	NodeEventEmit   = "event.emit"
	NodeGroupLoose  = "group.loose"
	NodeGroupOrder  = "group.order"

	EffectPlace = "place"
	EffectGroup = "group"
	EffectOrder = "order"
	EffectLive  = "live"
	EffectEmit  = "emit"

	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunRefused   = "refused"

	SourceAction = "action"

	actionRunsKept = 20
	actionEmitMax  = 4
	actionFocusGap = 30 * time.Second
)

var Triggers = []string{TriggerTabOpened, TriggerTabClosed, TriggerTabSelected, TriggerPageLoaded, TriggerPageOutside, TriggerInterval, TriggerOrganizeDone}

type ActionScope struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Section string `json:"section,omitempty"`
}

type NodePosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type CatalogueNode struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Description string         `json:"description"`
	Icon        string         `json:"icon,omitempty"`
	Code        string         `json:"code,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	LinksSchema map[string]any `json:"links_schema,omitempty"`
}

type ActionNode struct {
	ID       string         `json:"id"`
	Node     string         `json:"node,omitempty"`
	Name     string         `json:"name"`
	Kind     string         `json:"kind,omitempty"`
	Code     string         `json:"code,omitempty"`
	Input    map[string]any `json:"input,omitempty"`
	Position *NodePosition  `json:"position,omitempty"`
}

type ActionLink struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Type      string `json:"type"`
	ValuePath string `json:"value_path,omitempty"`
}

type Action struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    string       `json:"status"`
	Scope     ActionScope  `json:"scope"`
	CreatedAt string       `json:"created_at"`
	Nodes     []ActionNode `json:"nodes,omitempty"`
	Links     []ActionLink `json:"links,omitempty"`
	Problems  []string     `json:"problems,omitempty"`
}

type ActionSummary struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Status    string      `json:"status"`
	Scope     ActionScope `json:"scope"`
	Triggers  []string    `json:"triggers"`
	Nodes     int         `json:"nodes"`
	Problems  []string    `json:"problems,omitempty"`
	CreatedAt string      `json:"created_at"`
}

type RunNode struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	Result     any    `json:"result,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

type ActionRun struct {
	ID             string           `json:"id"`
	Action         string           `json:"action"`
	Name           string           `json:"name"`
	Trigger        string           `json:"trigger"`
	Dry            bool             `json:"dry"`
	Status         string           `json:"status"`
	Error          string           `json:"error,omitempty"`
	StartedAt      string           `json:"started_at"`
	EndedAt        string           `json:"ended_at"`
	Total          int              `json:"total"`
	Succeeded      int              `json:"succeeded"`
	Failed         int              `json:"failed"`
	Data           map[string]any   `json:"data,omitempty"`
	Nodes          []RunNode        `json:"nodes"`
	ExecutionGraph map[string]any   `json:"execution_graph,omitempty"`
	Effects        []map[string]any `json:"effects"`
}

type TriggerCall struct {
	Trigger string
	Data    map[string]any
	Dry     bool
	Action  string
	Scope   string
	Silent  bool
	depth   int
}

type TriggerResult struct {
	Trigger string           `json:"trigger"`
	Summary string           `json:"summary"`
	Runs    []ActionRun      `json:"runs"`
	Effects []map[string]any `json:"effects"`
}

type TriggerEntry struct {
	Action string `json:"action"`
	URL    string `json:"url,omitempty"`
}

var schemaText = func(description string, required bool) map[string]any {
	field := map[string]any{"type": "string", "description": description}
	if required {
		field["required"] = true
	}
	return field
}

var builtinNodes = []CatalogueNode{
	{Key: NodeTrigger, Name: "Trigger", Kind: string(graph.KindProvider), Icon: "bolt", Description: "Starts the action with what the browser sent for the event.", InputSchema: map[string]any{
		"trigger": schemaText("The event that starts the action", true),
		"every":   map[string]any{"type": "number", "description": "Minutes between runs of an interval trigger, 0 for never"},
		"url":     schemaText("Only start for this page address", false),
	}},
	{Key: NodePagePlace, Name: "Place the page", Kind: string(graph.KindAction), Icon: "folder", Description: "Places the page in a group and section, or in the workspace of the action when no group is set.", Code: "return place(data.url, input.group, input.section)", InputSchema: map[string]any{
		"group":   schemaText("The id of the group the page lands in", false),
		"section": schemaText("The section path inside the group", false),
	}},
	{Key: NodePageContent, Name: "Scrape the page", Kind: string(graph.KindTransformer), Icon: "document", Description: "Reads the text of the open tab the event is about.", Code: "return content(data.id)"},
	{Key: NodeGitHubItems, Name: "GitHub pull requests and issues", Kind: string(graph.KindTransformer), Icon: "github", Description: "Lists the open pull requests and issues of a repository with gh.", Code: "return github(input.repo, \"pr\") + github(input.repo, \"issue\")", InputSchema: map[string]any{
		"repo": schemaText("The repository as owner/name", true),
	}},
	{Key: NodeLivePut, Name: "Show as live", Kind: string(graph.KindAction), Icon: "notification", Description: "Shows the items it receives as the count and the subpages of a live page.", Code: "return live(input.url, len(data), data)", InputSchema: map[string]any{
		"url": schemaText("The page that is live", true),
	}},
	{Key: NodeLiveTitle, Name: "Count from the tab title", Kind: string(graph.KindAction), Icon: "notification", Description: "Keeps a page live with the count the browser reads from its tab title.", Code: "return live(input.url, 0, [])", InputSchema: map[string]any{
		"url": schemaText("The page that is live", true),
	}},
	{Key: NodeEventEmit, Name: "Emit an event", Kind: string(graph.KindAction), Icon: "megaphone", Description: "Starts every action whose trigger is the event name, with what this node received.", Code: "return emit(input.name, data)", InputSchema: map[string]any{
		"name": schemaText("The name of the event", true),
	}},
	{Key: NodeGroupLoose, Name: "Group what is left", Kind: string(graph.KindAction), Icon: "layers", Description: "Groups the pages nothing else placed by site, in a space of the workspace.", Code: "sites := {}\nfor entry in data.unplaced {\n  if is_undefined(sites[entry.site]) { sites[entry.site] = [] }\n  sites[entry.site] = append(sites[entry.site], entry.url)\n}\nmade := []\nfor site, urls in sites {\n  made = append(made, group(text.title(site), input.space, urls))\n}\nreturn made", InputSchema: map[string]any{
		"space": schemaText("The name of the space the groups are made in", true),
	}},
	{Key: NodeGroupOrder, Name: "Order a group", Kind: string(graph.KindAction), Icon: "sort", Description: "Orders the pages of a group by the list of addresses it receives.", Code: "return order(input.group, data)", InputSchema: map[string]any{
		"group": schemaText("The id of the group to order", true),
	}},
}

func actionsDir() (string, error) {
	return homeFile("actions")
}

func GetCatalogue() (map[string]CatalogueNode, error) {
	catalogue := map[string]CatalogueNode{}
	for _, entry := range builtinNodes {
		catalogue[entry.Key] = entry
	}
	dir, err := actionsDir()
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "catalogue", "*.json"))
	for _, file := range files {
		entry := CatalogueNode{}
		if err := readJSONFile(file, &entry); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if entry.Key == "" {
			entry.Key = strings.TrimSuffix(filepath.Base(file), ".json")
		}
		catalogue[entry.Key] = entry
	}
	return catalogue, nil
}

func getAction(dir string) (Action, error) {
	action := Action{}
	if err := readJSONFile(filepath.Join(dir, "action.json"), &action); err != nil {
		return Action{}, err
	}
	action.Nodes, action.Links = []ActionNode{}, []ActionLink{}
	files, _ := filepath.Glob(filepath.Join(dir, "nodes", "*.json"))
	sort.Strings(files)
	for _, file := range files {
		placed := ActionNode{}
		if err := readJSONFile(file, &placed); err != nil {
			return Action{}, fmt.Errorf("%s: %w", file, err)
		}
		action.Nodes = append(action.Nodes, placed)
	}
	if err := readJSONFile(filepath.Join(dir, "links.json"), &action.Links); err != nil && !os.IsNotExist(err) {
		return Action{}, err
	}
	return action, nil
}

func GetActions() ([]Action, error) {
	dir, err := actionsDir()
	if err != nil {
		return nil, err
	}
	catalogue, err := GetCatalogue()
	if err != nil {
		return nil, err
	}
	actions := []Action{}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "action.json"))
	for _, file := range files {
		action, err := getAction(filepath.Dir(file))
		if err != nil {
			return nil, err
		}
		action.Problems = action.getProblems(catalogue)
		actions = append(actions, action)
	}
	sort.SliceStable(actions, func(a, b int) bool { return actions[a].CreatedAt < actions[b].CreatedAt })
	return actions, nil
}

func GetAction(id string) (Action, error) {
	actions, err := GetActions()
	if err != nil {
		return Action{}, err
	}
	for _, action := range actions {
		if action.ID == id {
			return action, nil
		}
	}
	return Action{}, cli.Rejected("no action with id "+id, "read the ids with `slyb action get`")
}

func putTriggerIndex(dir string) error {
	actions, err := GetActions()
	if err != nil {
		return err
	}
	index := map[string][]TriggerEntry{}
	for _, action := range actions {
		if action.Status != ActionActive {
			continue
		}
		for _, placed := range action.Nodes {
			if placed.Node != NodeTrigger {
				continue
			}
			trigger, _ := placed.Input["trigger"].(string)
			address, _ := placed.Input["url"].(string)
			if trigger != "" {
				index[trigger] = append(index[trigger], TriggerEntry{Action: action.ID, URL: address})
			}
		}
	}
	return writeJSONFile(filepath.Join(dir, "triggers.json"), map[string]any{"triggers": index})
}

func PutAction(ctx context.Context, action Action) error {
	root, err := actionsDir()
	if err != nil {
		return err
	}
	if err := putActionFiles(root, action); err != nil {
		return err
	}
	return putActionsChanged(ctx, root)
}

func putActionFiles(root string, action Action) error {
	dir := filepath.Join(root, action.ID)
	kept := map[string]bool{}
	for _, placed := range action.Nodes {
		kept[placed.ID+".json"] = true
		if err := writeJSONFile(filepath.Join(dir, "nodes", placed.ID+".json"), placed); err != nil {
			return err
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "nodes", "*.json"))
	for _, file := range files {
		if !kept[filepath.Base(file)] {
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	links := action.Links
	if links == nil {
		links = []ActionLink{}
	}
	if err := writeJSONFile(filepath.Join(dir, "links.json"), links); err != nil {
		return err
	}
	action.Nodes, action.Links, action.Problems = nil, nil, nil
	return writeJSONFile(filepath.Join(dir, "action.json"), action)
}

func DeleteAction(ctx context.Context, id string) error {
	root, err := actionsDir()
	if err != nil {
		return err
	}
	if _, err := GetAction(id); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(root, id)); err != nil {
		return err
	}
	return putActionsChanged(ctx, root)
}

func putActionsChanged(ctx context.Context, root string) error {
	if err := putTriggerIndex(root); err != nil {
		return err
	}
	if _, err := Call(ctx, "actions.reload", nil); err != nil && !notRunning(err) {
		return err
	}
	return nil
}

func (action Action) Node(id string) *ActionNode {
	for i := range action.Nodes {
		if action.Nodes[i].ID == id {
			return &action.Nodes[i]
		}
	}
	return nil
}

func (action Action) NodeOf(key string) *ActionNode {
	for i := range action.Nodes {
		if action.Nodes[i].Node == key {
			return &action.Nodes[i]
		}
	}
	return nil
}

func (action Action) GetTriggers() []string {
	triggers := []string{}
	for _, placed := range action.Nodes {
		if trigger, _ := placed.Input["trigger"].(string); placed.Node == NodeTrigger && trigger != "" {
			triggers = append(triggers, trigger)
		}
	}
	return triggers
}

func (action Action) summarize() ActionSummary {
	return ActionSummary{ID: action.ID, Name: action.Name, Status: action.Status, Scope: action.Scope, Triggers: action.GetTriggers(), Nodes: len(action.Nodes), Problems: action.Problems, CreatedAt: action.CreatedAt}
}

func (placed ActionNode) definition(catalogue map[string]CatalogueNode) (CatalogueNode, error) {
	if placed.Node == "" {
		return CatalogueNode{Name: placed.Name, Kind: placed.Kind, Code: placed.Code}, nil
	}
	entry, ok := catalogue[placed.Node]
	if !ok {
		return CatalogueNode{}, fmt.Errorf("node %s names the catalogue node %s, which does not exist", placed.Name, placed.Node)
	}
	return entry, nil
}

func (action Action) getGraph(catalogue map[string]CatalogueNode, trigger string) (graph.Graph, error) {
	built := graph.Graph{}
	dropped := map[string]bool{}
	for _, placed := range action.Nodes {
		entry, err := placed.definition(catalogue)
		if err != nil {
			return built, err
		}
		id, err := uuid.Parse(placed.ID)
		if err != nil {
			return built, fmt.Errorf("node %s has an id that is not a uuid", placed.Name)
		}
		kind := graph.Kind(entry.Kind)
		if !graph.ValidKind(kind) {
			return built, fmt.Errorf("node %s has the unknown kind %q", placed.Name, entry.Kind)
		}
		if started, _ := placed.Input["trigger"].(string); kind == graph.KindProvider && started != trigger {
			dropped[placed.ID] = true
			continue
		}
		made := graph.Node{ID: id, Name: placed.Name, Kind: kind, Input: placed.Input, InputSchema: entry.InputSchema, LinksSchema: entry.LinksSchema}
		if made.Input == nil {
			made.Input = map[string]any{}
		}
		if entry.Code != "" {
			code := entry.Code
			made.Code = &code
		}
		built.Nodes = append(built.Nodes, made)
	}
	for _, link := range action.Links {
		if dropped[link.From] || dropped[link.To] {
			continue
		}
		id, err1 := uuid.Parse(link.ID)
		from, err2 := uuid.Parse(link.From)
		to, err3 := uuid.Parse(link.To)
		if err1 != nil || err2 != nil || err3 != nil {
			return built, fmt.Errorf("link %s has an id that is not a uuid", link.ID)
		}
		made := graph.Link{ID: id, FromID: from, ToID: &to, Type: graph.LinkType(link.Type)}
		if link.ValuePath != "" {
			path := link.ValuePath
			made.ValuePath = &path
		}
		if err := made.Validate(); err != nil {
			return built, fmt.Errorf("link %s: %w", link.ID, err)
		}
		built.Links = append(built.Links, made)
	}
	return built, nil
}

func (action Action) getProblems(catalogue map[string]CatalogueNode) []string {
	triggers := action.GetTriggers()
	if len(triggers) == 0 {
		return []string{"the action has no trigger"}
	}
	problems := []string{}
	seen := map[string]bool{}
	for _, trigger := range triggers {
		built, err := action.getGraph(catalogue, trigger)
		if err == nil {
			_, err = built.Validate()
		}
		if err != nil && !seen[err.Error()] {
			seen[err.Error()] = true
			problems = append(problems, err.Error())
		}
	}
	return problems
}

type actionHost struct {
	ctx     context.Context
	action  Action
	data    map[string]any
	titles  map[string]string
	mu      sync.Mutex
	effects []map[string]any
	emitted []TriggerCall
}

func toObject(value any) (tengo.Object, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var plain any
	if err := json.Unmarshal(raw, &plain); err != nil {
		return nil, err
	}
	return script.FromGo(plain)
}

func textArg(args []tengo.Object, index int) string {
	if index >= len(args) || args[index] == tengo.UndefinedValue {
		return ""
	}
	value, _ := tengo.ToString(args[index])
	return value
}

func listArg(args []tengo.Object, index int) []any {
	if index >= len(args) {
		return nil
	}
	list, _ := script.ToGo(args[index]).([]any)
	return list
}

func textList(values []any) []string {
	out := []string{}
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			out = append(out, typed)
		case map[string]any:
			if address, _ := typed["url"].(string); address != "" {
				out = append(out, address)
			}
		}
	}
	return out
}

func (host *actionHost) workspace() string {
	if workspace, _ := host.data["workspace"].(string); workspace != "" {
		return workspace
	}
	if host.action.Scope.Kind == ScopeWorkspace {
		return host.action.Scope.ID
	}
	return ""
}

func (host *actionHost) effect(kind string, fields map[string]any) (tengo.Object, error) {
	fields["kind"] = kind
	fields["action"] = host.action.ID
	fields["name"] = host.action.Name
	host.effects = append(host.effects, fields)
	return toObject(fields)
}

func (host *actionHost) helpers() script.Helpers {
	locked := func(fn script.Fn) script.Fn {
		return func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			host.mu.Lock()
			defer host.mu.Unlock()
			return fn(ctx, args...)
		}
	}
	return script.Helpers{
		{Name: "tabs", Signature: "tabs() => array", Group: "browser", Description: "The open tabs with their id, title, url, workspace and group.", Fn: func(ctx context.Context, _ ...tengo.Object) (tengo.Object, error) {
			var tabs []any
			if err := CallInto(ctx, "tab.get", nil, &tabs); err != nil {
				return nil, err
			}
			return toObject(tabs)
		}},
		{Name: "page", Signature: "page(url string) => object", Group: "browser", Description: "The metadata stored for a page: title, description, site, type and heading; undefined when the page was never loaded.", Fn: func(_ context.Context, args ...tengo.Object) (tengo.Object, error) {
			records, err := GetPageRecords()
			if err != nil {
				return nil, err
			}
			record, ok := records[textArg(args, 0)]
			if !ok {
				return tengo.UndefinedValue, nil
			}
			return toObject(record)
		}},
		{Name: "content", Signature: "content(tab string) => object", Group: "browser", Description: "The text of an open tab, by tab id, with its title and url.", Fn: func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			var content any
			if err := CallInto(ctx, "tab.content", map[string]any{"id": textArg(args, 0)}, &content); err != nil {
				return nil, err
			}
			return toObject(content)
		}},
		{Name: "github", Signature: "github(repo string, kind string) => array", Group: "sources", Description: "The open pull requests (kind pr) or issues (kind issue) of a repository, read with gh.", Fn: func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			kind := textArg(args, 1)
			if kind != "pr" && kind != "issue" {
				return nil, fmt.Errorf("github takes the kind pr or issue")
			}
			items, err := getGitHubItems(ctx, textArg(args, 0), kind)
			if err != nil {
				return nil, err
			}
			return toObject(items)
		}},
		{Name: "place", Signature: "place(url string, group string, section string) => object", Group: "effects", Description: "Places a page in a group and section; with no group, in the workspace of the action.", Effect: true, Stub: map[string]any{"kind": EffectPlace}, Fn: locked(func(_ context.Context, args ...tengo.Object) (tengo.Object, error) {
			placed := map[string]any{"url": textArg(args, 0), "group": textArg(args, 1), "section": textArg(args, 2), "space": host.workspace(), "scope": ScopeSpace}
			if placed["group"] != "" {
				store, err := GetGroups()
				if err != nil {
					return nil, err
				}
				group, err := store.Group(placed["group"].(string))
				if err != nil {
					return nil, err
				}
				placed["space"], placed["scope"] = group.Space, ScopeGroup
				if placed["section"] != "" {
					placed["scope"] = ScopeSection
				}
			}
			return host.effect(EffectPlace, placed)
		})},
		{Name: "group", Signature: "group(title string, space string, urls array) => object", Group: "effects", Description: "Adds pages to the group with that title in a space of the workspace, by space id or name, making the space and the group when they do not exist.", Effect: true, Stub: map[string]any{"kind": EffectGroup}, Fn: locked(func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			title, named, pages := textArg(args, 0), textArg(args, 1), textList(listArg(args, 2))
			workspace := host.workspace()
			if title == "" || named == "" || workspace == "" {
				return nil, fmt.Errorf("group needs a title, a space and an event that carries a workspace")
			}
			store, err := GetGroups()
			if err != nil {
				return nil, err
			}
			space := store.SpaceOf(named)
			if space == nil {
				space = store.SpaceNamed(workspace, named)
			}
			if space == nil {
				space = store.PostSpace(workspace, named)
			}
			spaceID := space.ID
			var target *Group
			for i := range store.Groups {
				if store.Groups[i].SpaceID == spaceID && strings.EqualFold(store.Groups[i].Title, title) {
					target = &store.Groups[i]
				}
			}
			if target == nil {
				target = store.PostGroup(workspace, title, false)
				target.SpaceID = spaceID
			}
			added := 0
			for _, address := range pages {
				if store.Contains(address) {
					continue
				}
				if target.Add("", KindSection, GroupPage{URL: address, Title: host.titles[address], Source: SourceAction, Verified: true}) {
					added++
				}
			}
			if err := PutGroups(ctx, store); err != nil {
				return nil, err
			}
			return host.effect(EffectGroup, map[string]any{"group": target.ID, "title": target.Title, "space": spaceID, "pages": added})
		})},
		{Name: "order", Signature: "order(group string, urls array) => object", Group: "effects", Description: "Orders the pages of a group by the list of addresses; pages not in the list keep their order after them.", Effect: true, Stub: map[string]any{"kind": EffectOrder}, Fn: locked(func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			store, err := GetGroups()
			if err != nil {
				return nil, err
			}
			target, err := store.Group(textArg(args, 0))
			if err != nil {
				return nil, err
			}
			rank := map[string]int{}
			for index, address := range textList(listArg(args, 1)) {
				if _, seen := rank[address]; !seen {
					rank[address] = index
				}
			}
			at := func(page GroupPage) int {
				if index, ok := rank[page.URL]; ok {
					return index
				}
				return len(rank)
			}
			for i := range target.Sections {
				pages := target.Sections[i].Pages
				sort.SliceStable(pages, func(a, b int) bool { return at(pages[a]) < at(pages[b]) })
			}
			if err := PutGroups(ctx, store); err != nil {
				return nil, err
			}
			return host.effect(EffectOrder, map[string]any{"group": target.ID, "pages": len(rank)})
		})},
		{Name: "live", Signature: "live(url string, count int, children array) => object", Group: "effects", Description: "Shows a count on a live page and its items as subpages; each child carries url, title, kind and number.", Effect: true, Stub: map[string]any{"kind": EffectLive}, Fn: locked(func(ctx context.Context, args ...tengo.Object) (tengo.Object, error) {
			address := textArg(args, 0)
			count := 0
			if len(args) > 1 {
				count, _ = tengo.ToInt(args[1])
			}
			children := []LiveChild{}
			if raw, err := json.Marshal(listArg(args, 2)); err == nil {
				_ = json.Unmarshal(raw, &children)
			}
			store, err := GetLive()
			if err != nil {
				return nil, err
			}
			item, err := store.Item(address)
			if err != nil {
				store.Items = append(store.Items, LiveItem{URL: address, Source: getLiveSource(address), CreatedAt: now()})
				item = &store.Items[len(store.Items)-1]
			}
			item.Count, item.Children, item.SyncedAt, item.Error = count, children, now(), ""
			if err := PutLive(ctx, store); err != nil {
				return nil, err
			}
			return host.effect(EffectLive, map[string]any{"url": address, "count": count})
		})},
		{Name: "emit", Signature: "emit(name string, data) => object", Group: "effects", Description: "Starts every action whose trigger is the event name, with the data, once this action has finished.", Effect: true, Stub: map[string]any{"kind": EffectEmit}, Fn: locked(func(_ context.Context, args ...tengo.Object) (tengo.Object, error) {
			name := textArg(args, 0)
			if name == "" {
				return nil, fmt.Errorf("emit needs the name of the event")
			}
			data := map[string]any{}
			if len(args) > 1 {
				if object, ok := script.ToGo(args[1]).(map[string]any); ok {
					data = object
				} else {
					data["value"] = script.ToGo(args[1])
				}
			}
			host.emitted = append(host.emitted, TriggerCall{Trigger: name, Data: data})
			return host.effect(EffectEmit, map[string]any{"event": name})
		})},
	}
}

func getTitles(value any, into map[string]string) {
	switch typed := value.(type) {
	case map[string]any:
		address, _ := typed["url"].(string)
		title, _ := typed["title"].(string)
		if address != "" && title != "" {
			into[address] = title
		}
		for _, inner := range typed {
			getTitles(inner, into)
		}
	case []any:
		for _, inner := range typed {
			getTitles(inner, into)
		}
	}
}

func getRunNodes(executed exec.ExecutionGraph, into *[]RunNode, seen map[string]bool) {
	for id, entry := range executed {
		if entry == nil {
			continue
		}
		_, ended := entry.Meta["ended_at"]
		if entry.Type == exec.ExecutionNodeTypeNode && !seen[id.String()] && (ended || entry.Error != nil) {
			seen[id.String()] = true
			made := RunNode{ID: id.String(), Status: "ok", Result: entry.Result}
			if seconds, ok := entry.Meta["run_time"].(float64); ok {
				made.DurationMs = int64(seconds * 1000)
			}
			if entry.Error != nil {
				made.Status, made.Error = "failed", entry.Error.Message
			}
			*into = append(*into, made)
		}
		if entry.Graph != nil {
			getRunNodes(*entry.Graph, into, seen)
		}
	}
}

func postActionRun(ctx context.Context, action Action, catalogue map[string]CatalogueNode, call TriggerCall) (ActionRun, []TriggerCall) {
	started := time.Now()
	run := ActionRun{ID: newID(), Action: action.ID, Name: action.Name, Trigger: call.Trigger, Dry: call.Dry, Status: RunSucceeded, StartedAt: started.UTC().Format(time.RFC3339Nano), Data: call.Data, Nodes: []RunNode{}, Effects: []map[string]any{}}
	finish := func(err error) (ActionRun, []TriggerCall) {
		run.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err != nil {
			run.Status, run.Error = RunRefused, err.Error()
		}
		return run, nil
	}
	built, err := action.getGraph(catalogue, call.Trigger)
	if err != nil {
		return finish(err)
	}
	if _, err := built.Validate(); err != nil {
		return finish(err)
	}
	executed, err := exec.Build(built, nil)
	if err != nil {
		return finish(err)
	}
	host := &actionHost{ctx: ctx, action: action, data: call.Data, titles: map[string]string{}}
	getTitles(call.Data, host.titles)
	nodes := map[uuid.UUID]graph.Node{}
	parents := map[uuid.UUID][]uuid.UUID{}
	for _, made := range built.Nodes {
		nodes[made.ID] = made
	}
	for _, link := range built.Links {
		parents[*link.ToID] = append(parents[*link.ToID], link.FromID)
	}
	var mu sync.Mutex
	inputs := map[uuid.UUID]any{}
	passed := func(parent uuid.UUID, fallback any) any {
		if nodes[parent].Kind != graph.KindCondition {
			return fallback
		}
		if input, ok := inputs[parent]; ok {
			return input
		}
		return fallback
	}
	shape := func(id uuid.UUID, data any) any {
		mu.Lock()
		defer mu.Unlock()
		from := parents[id]
		shaped := data
		if joined, ok := data.(map[string]any); ok && len(from) > 1 {
			merged := map[string]any{}
			for key, value := range joined {
				merged[key] = value
				if parent, err := uuid.Parse(key); err == nil {
					merged[key] = passed(parent, value)
				}
			}
			shaped = merged
		} else if len(from) == 1 {
			shaped = passed(from[0], data)
		}
		inputs[id] = shaped
		return shaped
	}
	nctx := node.Context{Context: ctx, Helpers: host.helpers(), Dry: call.Dry}
	runner := func(runCtx context.Context, made graph.Node, entry *exec.ExecutionNode, data any) error {
		scoped := nctx
		scoped.Context = runCtx
		result, err := node.Run(scoped, *entry, made, shape(made.ID, data), nil)
		if result != nil {
			entry.Result, entry.Error, entry.Trace = result.Result, result.Error, result.Trace
			for key, value := range result.Meta {
				entry.Meta[key] = value
			}
		}
		if err != nil && entry.Error == nil {
			entry.Error = &exec.GraphError{Message: err.Error(), Type: exec.GraphErrorTypeNode}
		}
		return nil
	}
	if err := exec.Run(ctx, exec.Session{Graph: executed, Nodes: nodes, Data: call.Data, Runner: runner}); err != nil {
		return finish(err)
	}
	getRunNodes(executed, &run.Nodes, map[string]bool{})
	sort.SliceStable(run.Nodes, func(a, b int) bool { return run.Nodes[a].ID < run.Nodes[b].ID })
	run.Total = len(run.Nodes)
	for _, entry := range run.Nodes {
		if entry.Status == "failed" {
			run.Failed++
			run.Status = RunFailed
			if run.Error == "" {
				run.Error = entry.Error
			}
		} else {
			run.Succeeded++
		}
	}
	run.ExecutionGraph = exec.GraphToMap(executed)
	run.Effects = append(run.Effects, host.effects...)
	run.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return run, host.emitted
}

func runsDir(action string) (string, error) {
	root, err := actionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, action, "runs"), nil
}

func GetActionRuns(action string) ([]ActionRun, error) {
	dir, err := runsDir(action)
	if err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	runs := []ActionRun{}
	for _, file := range files {
		run := ActionRun{}
		if err := readJSONFile(file, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func putActionRun(run ActionRun) error {
	dir, err := runsDir(run.Action)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(dir, fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), run.ID)), run); err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	for len(files) > actionRunsKept {
		if err := os.Remove(files[0]); err != nil {
			return err
		}
		files = files[1:]
	}
	return nil
}

func lastRun(action string) time.Time {
	runs, err := GetActionRuns(action)
	if err != nil || len(runs) == 0 {
		return time.Time{}
	}
	at, _ := time.Parse(time.RFC3339Nano, runs[0].StartedAt)
	return at
}

func (action Action) starts(call TriggerCall) bool {
	if call.Action != "" {
		return action.ID == call.Action
	}
	if action.Status != ActionActive || (call.Scope != "" && action.Scope.Kind != call.Scope) {
		return false
	}
	if at, _ := call.Data[action.Scope.Kind].(string); at != "" && action.Scope.ID != "" && at != action.Scope.ID {
		return false
	}
	for _, placed := range action.Nodes {
		if trigger, _ := placed.Input["trigger"].(string); placed.Node != NodeTrigger || trigger != call.Trigger {
			continue
		}
		address, _ := placed.Input["url"].(string)
		if at, _ := call.Data["url"].(string); address != "" && strings.TrimRight(address, "/") != strings.TrimRight(at, "/") {
			continue
		}
		if address != "" && time.Since(lastRun(action.ID)) < actionFocusGap {
			continue
		}
		if call.Trigger == TriggerInterval {
			every, _ := placed.Input["every"].(float64)
			if every <= 0 || time.Since(lastRun(action.ID)) < time.Duration(every*float64(time.Minute)) {
				continue
			}
		}
		return true
	}
	return false
}

func putPageFields(data map[string]any) {
	address, _ := data["url"].(string)
	if address == "" {
		return
	}
	host, path := "", ""
	if parsed, err := url.Parse(address); err == nil {
		host, path = strings.ToLower(parsed.Hostname()), parsed.EscapedPath()
	}
	for key, value := range map[string]string{"host": host, "path": path, "site": siteOf(host), "title": "", "folder": "", "remote": "", "email": ""} {
		if _, set := data[key]; !set {
			data[key] = value
		}
	}
}

func siteOf(host string) string {
	base, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimPrefix(host, "www."))
	if err != nil {
		base = host
	}
	return strings.Split(base, ".")[0]
}

var placeRank = map[string]int{ScopeSection: 0, ScopeGroup: 1, ScopeSpace: 2}

func PostTrigger(ctx context.Context, call TriggerCall) (TriggerResult, error) {
	result := TriggerResult{Trigger: call.Trigger, Runs: []ActionRun{}, Effects: []map[string]any{}}
	if call.Data == nil {
		call.Data = map[string]any{}
	}
	if raw, err := json.Marshal(call.Data); err != nil {
		return result, err
	} else if err := json.Unmarshal(raw, &call.Data); err != nil {
		return result, err
	}
	call.Data["trigger"] = call.Trigger
	putPageFields(call.Data)
	if unplaced, ok := call.Data["unplaced"].([]any); ok {
		for _, entry := range unplaced {
			if page, ok := entry.(map[string]any); ok {
				putPageFields(page)
			}
		}
	}
	actions, err := GetActions()
	if err != nil {
		return result, err
	}
	catalogue, err := GetCatalogue()
	if err != nil {
		return result, err
	}
	if call.Action != "" {
		if _, err := GetAction(call.Action); err != nil {
			return result, err
		}
	}
	for _, action := range actions {
		if !action.starts(call) {
			continue
		}
		run, emitted := postActionRun(ctx, action, catalogue, call)
		if !call.Silent && !call.Dry {
			if err := putActionRun(run); err != nil {
				return result, err
			}
		}
		result.Runs = append(result.Runs, run)
		result.Effects = append(result.Effects, run.Effects...)
		for _, next := range emitted {
			if call.depth >= actionEmitMax {
				return result, fmt.Errorf("the event %s was emitted more than %d actions deep", next.Trigger, actionEmitMax)
			}
			next.Silent, next.depth = call.Silent, call.depth+1
			chained, err := PostTrigger(ctx, next)
			if err != nil {
				return result, err
			}
			result.Runs = append(result.Runs, chained.Runs...)
			result.Effects = append(result.Effects, chained.Effects...)
		}
	}
	sort.SliceStable(result.Effects, func(a, b int) bool {
		rank := func(effect map[string]any) int {
			if effect["kind"] != EffectPlace {
				return len(placeRank)
			}
			scope, _ := effect["scope"].(string)
			return placeRank[scope]
		}
		return rank(result.Effects[a]) < rank(result.Effects[b])
	})
	failed := 0
	for _, run := range result.Runs {
		if run.Status != RunSucceeded {
			failed++
		}
	}
	result.Summary = fmt.Sprintf("%d actions ran, %d failed, %d effects", len(result.Runs), failed, len(result.Effects))
	return result, nil
}

func getActionScope(workspace, space, group string) (ActionScope, error) {
	scope := ActionScope{}
	for kind, id := range map[string]string{ScopeWorkspace: workspace, ScopeSpace: space, ScopeGroup: group} {
		if id == "" {
			continue
		}
		if scope.Kind != "" {
			return scope, cli.Rejected("pass one of --workspace, --space and --group", "")
		}
		scope = ActionScope{Kind: kind, ID: id}
	}
	return scope, nil
}

func getJSONFlag(raw string) (map[string]any, error) {
	parsed := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return parsed, nil
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, cli.Rejected("that is not a json object: "+err.Error(), "")
	}
	return parsed, nil
}

func checkNodeCode(kind, code string) error {
	if !graph.ValidKind(graph.Kind(kind)) {
		return cli.Rejected("the kind "+kind+" does not exist", "the kinds are provider, receiver, formatter, transformer, filter, static, condition, conclusion, fetch and action")
	}
	if !graph.CarriesCode(graph.Kind(kind)) {
		return cli.Rejected("a "+kind+" node carries no code", "")
	}
	host := &actionHost{data: map[string]any{}, titles: map[string]string{}}
	if problem := node.ValidateCode(node.Context{Context: context.Background(), Helpers: host.helpers()}, code, graph.Kind(kind)); problem != nil {
		return cli.Rejected(fmt.Sprintf("the code does not compile, line %d: %s", problem.Position.Line, problem.Message), "")
	}
	return nil
}

func PostActionImport() error {
	root, err := actionsDir()
	if err != nil {
		return err
	}
	actions, err := GetActions()
	if err != nil {
		return err
	}
	held := map[string]bool{}
	loose := false
	for _, action := range actions {
		held[action.ID] = true
		loose = loose || action.NodeOf(NodeGroupLoose) != nil
	}
	stored := RuleStore{}
	if path, err := homeFile("rules.json"); err != nil {
		return err
	} else if err := readJSONFile(path, &stored); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, rule := range stored.Rules {
		if !held[rule.ID] {
			if err := putActionFiles(root, rule.action()); err != nil {
				return err
			}
		}
	}
	live, err := GetLive()
	if err != nil {
		return err
	}
	for _, item := range live.Items {
		if _, exists, err := getLiveAction(item.URL); err != nil {
			return err
		} else if !exists {
			if err := putActionFiles(root, item.action()); err != nil {
				return err
			}
		}
	}
	if loose {
		return putTriggerIndex(root)
	}
	done, left, grouped := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := putActionFiles(root, Action{
		ID:        newID(),
		Name:      "Loose pages",
		Status:    ActionActive,
		CreatedAt: now(),
		Nodes: []ActionNode{
			{ID: done, Node: NodeTrigger, Name: "After organize", Input: map[string]any{"trigger": TriggerOrganizeDone}, Position: &NodePosition{X: 0, Y: 0}},
			{ID: left, Name: "Something is left", Kind: string(graph.KindCondition), Code: "return len(data.unplaced) > 0", Position: &NodePosition{X: 280, Y: 0}},
			{ID: grouped, Node: NodeGroupLoose, Name: "Group what is left", Input: map[string]any{"space": "Loose"}, Position: &NodePosition{X: 560, Y: 0}},
		},
		Links: []ActionLink{
			{ID: uuid.NewString(), From: done, To: left, Type: string(graph.LinkTypeObject)},
			{ID: uuid.NewString(), From: left, To: grouped, Type: string(graph.LinkTypeTrue)},
		},
	}); err != nil {
		return err
	}
	return putTriggerIndex(root)
}

func NewActionCommand() *cobra.Command {
	action := &cobra.Command{Use: "action", Short: "Actions: node graphs on nimflow that run on a browser event to watch, scrape, group, order and emit events"}

	var getWorkspace, getSpace, getGroup string
	var getRuns bool
	get := &cobra.Command{
		Use:         "get [action_id]",
		Short:       "List the actions with their scope, triggers and problems, or show one with its nodes, links and last runs",
		Example:     "slyb action get --group 3f9a2c1b7d4e",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "read"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				found, err := GetAction(args[0])
				if err != nil {
					return err
				}
				if !getRuns {
					return printValue(cmd, found)
				}
				runs, err := GetActionRuns(found.ID)
				if err != nil {
					return err
				}
				return printValue(cmd, map[string]any{"action": found, "runs": runs})
			}
			scope, err := getActionScope(getWorkspace, getSpace, getGroup)
			if err != nil {
				return err
			}
			actions, err := GetActions()
			if err != nil {
				return err
			}
			listed := []ActionSummary{}
			for _, entry := range actions {
				if scope.Kind == "" || (entry.Scope.Kind == scope.Kind && entry.Scope.ID == scope.ID) {
					listed = append(listed, entry.summarize())
				}
			}
			return printValue(cmd, listed)
		},
	}
	get.Flags().StringVar(&getWorkspace, "workspace", "", "Only the actions of this workspace")
	get.Flags().StringVar(&getSpace, "space", "", "Only the actions of this space")
	get.Flags().StringVar(&getGroup, "group", "", "Only the actions of this group")
	get.Flags().BoolVar(&getRuns, "runs", false, "With an action id, also its last runs")

	var workspace, space, group, trigger string
	create := &cobra.Command{
		Use:         "create <name>",
		Short:       "Make an action for a workspace, a space or a group, or for the whole browser, with a trigger node when --trigger names one",
		Long:        "The triggers the browser sends are " + strings.Join(Triggers, ", ") + ". Any other name is an event an action emits with emit(name, data).",
		Example:     "slyb action create \"Watch the board\" --group 3f9a2c1b7d4e --trigger page.loaded",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := getActionScope(workspace, space, group)
			if err != nil {
				return err
			}
			if strings.TrimSpace(args[0]) == "" {
				return cli.Rejected("an action needs a name", "")
			}
			made := Action{ID: newID(), Name: strings.TrimSpace(args[0]), Status: ActionActive, Scope: scope, CreatedAt: now(), Nodes: []ActionNode{}, Links: []ActionLink{}}
			if trigger != "" {
				made.Nodes = append(made.Nodes, ActionNode{ID: uuid.NewString(), Node: NodeTrigger, Name: trigger, Input: map[string]any{"trigger": trigger}, Position: &NodePosition{}})
			}
			if err := PutAction(cmd.Context(), made); err != nil {
				return err
			}
			return printValue(cmd, made)
		},
	}
	create.Flags().StringVar(&workspace, "workspace", "", "Make it an action of this workspace")
	create.Flags().StringVar(&space, "space", "", "Make it an action of this space")
	create.Flags().StringVar(&group, "group", "", "Make it an action of this group")
	create.Flags().StringVar(&trigger, "trigger", "", "Start it on this event")

	var updateName, updateStatus string
	update := &cobra.Command{
		Use:         "update <action_id>",
		Short:       "Rename an action, or pause it and set it active again",
		Example:     "slyb action update 3f9a2c1b7d4e --status paused",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			if strings.TrimSpace(updateName) == "" && updateStatus == "" {
				return cli.Rejected("nothing to change", "pass --name or --status")
			}
			if updateStatus != "" && updateStatus != ActionActive && updateStatus != ActionPaused {
				return cli.Rejected("--status is active or paused", "")
			}
			if name := strings.TrimSpace(updateName); name != "" {
				found.Name = name
			}
			if updateStatus != "" {
				found.Status = updateStatus
			}
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, found)
		},
	}
	update.Flags().StringVar(&updateName, "name", "", "New name")
	update.Flags().StringVar(&updateStatus, "status", "", "active or paused")

	remove := &cobra.Command{
		Use:         "delete <action_id>",
		Short:       "Delete an action with its nodes, links and runs",
		Example:     "slyb action delete 3f9a2c1b7d4e",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "destructive"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := DeleteAction(cmd.Context(), args[0]); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "deleted", "id": args[0]})
		},
	}

	var runAction, runData string
	var runDry bool
	run := &cobra.Command{
		Use:   "run <trigger>",
		Short: "Run every active action that starts on the event, or the one named with --action, and print the runs and their effects",
		Long: "The browser calls this for each event an active action starts on. --data is the event as a json object; a\n" +
			"page event carries url and title, and host, path and site are added from the url. --dry runs the nodes and\n" +
			"records what each effect would do without doing it. A place effect is printed for the browser to carry out.",
		Example:     "slyb action run page.outside --data '{\"url\":\"https://git.example.com/platform\"}'",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := getJSONFlag(runData)
			if err != nil {
				return err
			}
			result, err := PostTrigger(cmd.Context(), TriggerCall{Trigger: args[0], Data: data, Dry: runDry, Action: runAction})
			if err != nil {
				return err
			}
			return printValue(cmd, result)
		},
	}
	run.Flags().StringVar(&runAction, "action", "", "Run only this action, active or paused")
	run.Flags().StringVar(&runData, "data", "", "The event as a json object")
	run.Flags().BoolVar(&runDry, "dry", false, "Run without carrying out any effect and without recording the run")

	nodes := &cobra.Command{Use: "node", Short: "The nodes of an action and the catalogue of nodes to place"}
	nodes.AddCommand(&cobra.Command{
		Use:         "get [action_id]",
		Short:       "List the catalogue of nodes with their kind, code and input, or the nodes placed in an action",
		Example:     "slyb action node get",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "read"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				found, err := GetAction(args[0])
				if err != nil {
					return err
				}
				return printValue(cmd, found.Nodes)
			}
			catalogue, err := GetCatalogue()
			if err != nil {
				return err
			}
			listed := []CatalogueNode{}
			for _, entry := range catalogue {
				listed = append(listed, entry)
			}
			sort.Slice(listed, func(a, b int) bool { return listed[a].Key < listed[b].Key })
			return printValue(cmd, listed)
		},
	})
	var nodeKey, nodeKind, nodeCode, nodeFile, nodeName, nodeInput string
	var nodeX, nodeY float64
	readCode := func() error {
		if nodeFile == "" {
			return nil
		}
		content, err := os.ReadFile(nodeFile)
		nodeCode = string(content)
		return err
	}
	nodeCreate := &cobra.Command{
		Use:   "create <action_id>",
		Short: "Place a node in an action: one from the catalogue with --node, or one of its own with --kind and --code",
		Long: "A catalogue node keeps its code in the catalogue and takes its values as --input. A node of its own carries\n" +
			"Tengo code that reads input and data and returns a value; a condition returns true or false. The helpers are\n" +
			"tabs(), page(url), content(tab), github(repo, kind), place(url, group, section), group(title, space, urls),\n" +
			"order(group, urls), live(url, count, children) and emit(name, data).",
		Example:     "slyb action node create 3f9a2c1b7d4e --node event.emit --input '{\"name\":\"board.changed\"}'",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			if err := readCode(); err != nil {
				return err
			}
			input, err := getJSONFlag(nodeInput)
			if err != nil {
				return err
			}
			placed := ActionNode{ID: uuid.NewString(), Name: strings.TrimSpace(nodeName), Input: input, Position: &NodePosition{X: nodeX, Y: nodeY}}
			switch {
			case nodeKey != "" && nodeKind == "" && nodeCode == "":
				catalogue, err := GetCatalogue()
				if err != nil {
					return err
				}
				entry, ok := catalogue[nodeKey]
				if !ok {
					return cli.Rejected("no catalogue node with the key "+nodeKey, "read the keys with `slyb action node get`")
				}
				placed.Node = nodeKey
				if placed.Name == "" {
					placed.Name = entry.Name
				}
			case nodeKey == "" && nodeKind != "" && strings.TrimSpace(nodeCode) != "":
				if err := checkNodeCode(nodeKind, nodeCode); err != nil {
					return err
				}
				placed.Kind, placed.Code = nodeKind, nodeCode
				if placed.Name == "" {
					placed.Name = nodeKind
				}
			default:
				return cli.Rejected("pass --node, or --kind with --code or --file", "")
			}
			found.Nodes = append(found.Nodes, placed)
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, placed)
		},
	}
	nodeUpdate := &cobra.Command{
		Use:         "update <action_id> <node_id>",
		Short:       "Rename a placed node, set its input, move it, or replace its code; --code on a catalogue node makes it a node of its own",
		Example:     "slyb action node update 3f9a2c1b7d4e 6f1c0b1e-8a55-4a3e-9d0b-2f4f6f0c9a11 --input '{\"repo\":\"acme/platform\"}'",
		Args:        cobra.ExactArgs(2),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			placed := found.Node(args[1])
			if placed == nil {
				return cli.Rejected("no node with id "+args[1], "read them with `slyb action node get "+args[0]+"`")
			}
			if err := readCode(); err != nil {
				return err
			}
			changed := false
			if name := strings.TrimSpace(nodeName); name != "" {
				placed.Name, changed = name, true
			}
			if cmd.Flags().Changed("input") {
				input, err := getJSONFlag(nodeInput)
				if err != nil {
					return err
				}
				placed.Input, changed = input, true
			}
			if cmd.Flags().Changed("x") || cmd.Flags().Changed("y") {
				placed.Position, changed = &NodePosition{X: nodeX, Y: nodeY}, true
			}
			if strings.TrimSpace(nodeCode) != "" {
				catalogue, err := GetCatalogue()
				if err != nil {
					return err
				}
				entry, err := placed.definition(catalogue)
				if err != nil {
					return err
				}
				if err := checkNodeCode(entry.Kind, nodeCode); err != nil {
					return err
				}
				placed.Node, placed.Kind, placed.Code, changed = "", entry.Kind, nodeCode, true
			}
			if !changed {
				return cli.Rejected("nothing to change", "pass --name, --input, --code, --file, or --x and --y")
			}
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, placed)
		},
	}
	for _, command := range []*cobra.Command{nodeCreate, nodeUpdate} {
		command.Flags().StringVar(&nodeName, "name", "", "What the node is called in the action")
		command.Flags().StringVar(&nodeInput, "input", "", "The node's input as a json object")
		command.Flags().StringVar(&nodeCode, "code", "", "The Tengo code of a node of its own")
		command.Flags().StringVar(&nodeFile, "file", "", "Read the Tengo code from a file")
		command.Flags().Float64Var(&nodeX, "x", 0, "Where the node sits in the editor, across")
		command.Flags().Float64Var(&nodeY, "y", 0, "Where the node sits in the editor, down")
	}
	nodeCreate.Flags().StringVar(&nodeKey, "node", "", "The key of the catalogue node to place")
	nodeCreate.Flags().StringVar(&nodeKind, "kind", "", "The kind of a node of its own, such as condition, transformer or action")
	nodes.AddCommand(nodeCreate, nodeUpdate, &cobra.Command{
		Use:         "delete <action_id> <node_id>",
		Short:       "Take a node out of an action, with the links into and out of it",
		Example:     "slyb action node delete 3f9a2c1b7d4e 6f1c0b1e-8a55-4a3e-9d0b-2f4f6f0c9a11",
		Args:        cobra.ExactArgs(2),
		Annotations: map[string]string{cli.AnnotationMCP: "destructive"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			if found.Node(args[1]) == nil {
				return cli.Rejected("no node with id "+args[1], "read them with `slyb action node get "+args[0]+"`")
			}
			kept, links := []ActionNode{}, []ActionLink{}
			for _, placed := range found.Nodes {
				if placed.ID != args[1] {
					kept = append(kept, placed)
				}
			}
			for _, link := range found.Links {
				if link.From != args[1] && link.To != args[1] {
					links = append(links, link)
				}
			}
			found.Nodes, found.Links = kept, links
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "deleted", "id": args[1]})
		},
	})

	links := &cobra.Command{Use: "link", Short: "The links between the nodes of an action"}
	var linkType, linkPath string
	linkCreate := &cobra.Command{
		Use:   "create <action_id> <from_node_id> <to_node_id>",
		Short: "Link one node to the next: what the first returns is the data of the second",
		Long: "The type is object unless --type says otherwise: true and false leave a condition, error is followed when the\n" +
			"node failed, and field passes one value at --path, such as $.items. A node after a condition receives what the\n" +
			"condition received.",
		Example:     "slyb action link create 3f9a2c1b7d4e 6f1c0b1e-8a55-4a3e-9d0b-2f4f6f0c9a11 0b7f1a52-5d0e-4c57-8a4e-1c2d3e4f5a6b --type true",
		Args:        cobra.ExactArgs(3),
		Annotations: map[string]string{cli.AnnotationMCP: "write"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			from, to := found.Node(args[1]), found.Node(args[2])
			if from == nil || to == nil {
				return cli.Rejected("both nodes must be nodes of the action", "read them with `slyb action node get "+args[0]+"`")
			}
			catalogue, err := GetCatalogue()
			if err != nil {
				return err
			}
			source, err := from.definition(catalogue)
			if err != nil {
				return err
			}
			if linkType == "" {
				linkType = string(graph.LinkTypeObject)
				if source.Kind == string(graph.KindCondition) {
					linkType = string(graph.LinkTypeTrue)
				}
			}
			made := ActionLink{ID: uuid.NewString(), From: from.ID, To: to.ID, Type: linkType, ValuePath: linkPath}
			for _, link := range found.Links {
				if link.From == made.From && link.To == made.To && link.Type == made.Type {
					return cli.Rejected("those nodes are already linked that way", "")
				}
			}
			found.Links = append(found.Links, made)
			plain := make([]graph.Link, 0, len(found.Links))
			for _, link := range found.Links {
				fromID, err1 := uuid.Parse(link.From)
				toID, err2 := uuid.Parse(link.To)
				if err1 != nil || err2 != nil {
					return cli.Rejected("link "+link.ID+" has an id that is not a uuid", "")
				}
				plain = append(plain, graph.Link{FromID: fromID, ToID: &toID, Type: graph.LinkType(link.Type), ValuePath: &link.ValuePath})
			}
			if err := plain[len(plain)-1].Validate(); err != nil {
				return cli.Rejected(err.Error(), "")
			}
			if graph.ClosesCycle(plain[:len(plain)-1], plain[len(plain)-1]) {
				return cli.Rejected(graph.ErrLinkCycle.Error(), "")
			}
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, made)
		},
	}
	linkCreate.Flags().StringVar(&linkType, "type", "", "object, true, false, error, array or field")
	linkCreate.Flags().StringVar(&linkPath, "path", "", "With --type field, the value to pass, such as $.items")
	links.AddCommand(linkCreate, &cobra.Command{
		Use:         "delete <action_id> <link_id>",
		Short:       "Remove a link between two nodes",
		Example:     "slyb action link delete 3f9a2c1b7d4e 9d2c4b6a-1f3e-4a5b-8c7d-6e5f4a3b2c1d",
		Args:        cobra.ExactArgs(2),
		Annotations: map[string]string{cli.AnnotationMCP: "destructive"},
		RunE: func(cmd *cobra.Command, args []string) error {
			found, err := GetAction(args[0])
			if err != nil {
				return err
			}
			kept := []ActionLink{}
			for _, link := range found.Links {
				if link.ID != args[1] {
					kept = append(kept, link)
				}
			}
			if len(kept) == len(found.Links) {
				return cli.Rejected("no link with id "+args[1], "read them with `slyb action get "+args[0]+"`")
			}
			found.Links = kept
			if err := PutAction(cmd.Context(), found); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "deleted", "id": args[1]})
		},
	})

	action.AddCommand(get, create, update, remove, run, nodes, links)
	return action
}
