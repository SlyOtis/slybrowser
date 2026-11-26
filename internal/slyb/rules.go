package slyb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/nimling/nimbody/cli"
	"github.com/nimling/nimflow/graph"
	"github.com/spf13/cobra"
)

const (
	ScopeSpace   = "space"
	ScopeGroup   = "group"
	ScopeSection = "section"

	ruleStart = "url := data.url; host := data.host; path := data.path; title := data.title; folder := data.folder; remote := data.remote; email := data.email; result := false\n"
	ruleEnd   = "\nreturn result"
)

var ruleImport = regexp.MustCompile(`\w+[ \t]*:=[ \t]*import\("\w+"\)[ \t]*;?[ \t]*\n?`)

type Rule struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Space     string `json:"space"`
	Group     string `json:"group,omitempty"`
	Section   string `json:"section,omitempty"`
	Script    string `json:"script"`
	CreatedAt string `json:"created_at"`
}

type RuleStore struct {
	Rules []Rule `json:"rules"`
}

type RuleMatch struct {
	URL     string `json:"url"`
	Scope   string `json:"scope"`
	Space   string `json:"space"`
	Group   string `json:"group"`
	Section string `json:"section"`
	Rule    string `json:"rule"`
	ID      string `json:"id"`
}

func (rule Rule) placement() (ActionScope, map[string]any) {
	if rule.Group == "" {
		return ActionScope{Kind: ScopeWorkspace, ID: rule.Space}, map[string]any{"group": "", "section": ""}
	}
	return ActionScope{Kind: ScopeGroup, ID: rule.Group, Section: rule.Section}, map[string]any{"group": rule.Group, "section": rule.Section, "workspace": rule.Space}
}

func (rule Rule) code() string {
	return ruleStart + ruleImport.ReplaceAllString(rule.Script, "") + ruleEnd
}

func (rule Rule) action() Action {
	scope, input := rule.placement()
	trigger, matches, place := uuid.NewString(), uuid.NewString(), uuid.NewString()
	return Action{
		ID:        rule.ID,
		Name:      rule.Name,
		Status:    ActionActive,
		Scope:     scope,
		CreatedAt: rule.CreatedAt,
		Nodes: []ActionNode{
			{ID: trigger, Node: NodeTrigger, Name: "Page opened from outside", Input: map[string]any{"trigger": TriggerPageOutside}, Position: &NodePosition{X: 0, Y: 0}},
			{ID: matches, Name: "Matches", Kind: string(graph.KindCondition), Code: rule.code(), Position: &NodePosition{X: 280, Y: 0}},
			{ID: place, Node: NodePagePlace, Name: "Place the page", Input: input, Position: &NodePosition{X: 560, Y: 0}},
		},
		Links: []ActionLink{
			{ID: uuid.NewString(), From: trigger, To: matches, Type: string(graph.LinkTypeObject)},
			{ID: uuid.NewString(), From: matches, To: place, Type: string(graph.LinkTypeTrue)},
		},
	}
}

func (action Action) condition() *ActionNode {
	for i := range action.Nodes {
		if action.Nodes[i].Kind == string(graph.KindCondition) && strings.HasPrefix(action.Nodes[i].Code, ruleStart) {
			return &action.Nodes[i]
		}
	}
	return nil
}

func (action Action) rule() (Rule, bool) {
	place, matches := action.NodeOf(NodePagePlace), action.condition()
	if place == nil || matches == nil {
		return Rule{}, false
	}
	rule := Rule{ID: action.ID, Name: action.Name, Scope: ScopeSpace, Space: action.Scope.ID, CreatedAt: action.CreatedAt}
	rule.Script = strings.TrimSuffix(strings.TrimPrefix(matches.Code, ruleStart), ruleEnd)
	if action.Scope.Kind == ScopeGroup {
		rule.Scope, rule.Group, rule.Section = ScopeGroup, action.Scope.ID, action.Scope.Section
		rule.Space, _ = place.Input["workspace"].(string)
		if rule.Section != "" {
			rule.Scope = ScopeSection
		}
	}
	return rule, true
}

func GetRules() (RuleStore, error) {
	actions, err := GetActions()
	if err != nil {
		return RuleStore{}, err
	}
	store := RuleStore{Rules: []Rule{}}
	for _, action := range actions {
		if rule, ok := action.rule(); ok {
			store.Rules = append(store.Rules, rule)
		}
	}
	return store, nil
}

func PutRules(ctx context.Context, store RuleStore) error {
	actions, err := GetActions()
	if err != nil {
		return err
	}
	held := map[string]Action{}
	for _, action := range actions {
		if _, ok := action.rule(); ok {
			held[action.ID] = action
		}
	}
	for _, rule := range store.Rules {
		action, exists := held[rule.ID]
		delete(held, rule.ID)
		if !exists {
			if err := PutAction(ctx, rule.action()); err != nil {
				return err
			}
			continue
		}
		if current, _ := action.rule(); current == rule {
			continue
		}
		scope, input := rule.placement()
		action.Name, action.Scope = rule.Name, scope
		action.condition().Code = rule.code()
		action.NodeOf(NodePagePlace).Input = input
		if err := PutAction(ctx, action); err != nil {
			return err
		}
	}
	for id := range held {
		if err := DeleteAction(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

type RuleInput struct {
	URL    string
	Title  string
	Folder string
	Remote string
	Email  string
}

func runRule(ctx context.Context, rule Rule, page RuleInput) (bool, error) {
	catalogue, err := GetCatalogue()
	if err != nil {
		return false, err
	}
	data := map[string]any{"trigger": TriggerPageOutside, "url": page.URL, "title": page.Title, "folder": page.Folder, "remote": page.Remote, "email": page.Email}
	putPageFields(data)
	action := rule.action()
	run, _ := postActionRun(ctx, action, catalogue, TriggerCall{Trigger: TriggerPageOutside, Data: data, Dry: true})
	if run.Status == RunRefused {
		return false, fmt.Errorf("rule %s failed: %s", rule.Name, run.Error)
	}
	for _, entry := range run.Nodes {
		if entry.ID != action.condition().ID {
			continue
		}
		if entry.Status == "failed" {
			return false, fmt.Errorf("rule %s failed: %s", rule.Name, entry.Error)
		}
		matched, _ := entry.Result.(bool)
		return matched, nil
	}
	return false, fmt.Errorf("rule %s did not run", rule.Name)
}

func MatchRules(ctx context.Context, store RuleStore, page Page) (RuleMatch, error) {
	for _, scope := range []string{ScopeSection, ScopeGroup, ScopeSpace} {
		for _, rule := range store.Rules {
			if rule.Scope != scope {
				continue
			}
			matched, err := runRule(ctx, rule, RuleInput{URL: page.URL, Title: page.Title})
			if err != nil {
				return RuleMatch{}, err
			}
			if matched {
				return RuleMatch{URL: page.URL, Scope: rule.Scope, Space: rule.Space, Group: rule.Group, Section: rule.Section, Rule: rule.Name, ID: rule.ID}, nil
			}
		}
	}
	return RuleMatch{URL: page.URL}, nil
}

const ruleScriptSchema = `{"type":"object","properties":{"name":{"type":"string"},"script":{"type":"string"}},"required":["name","script"],"additionalProperties":false}`

const ruleLanguage = "A rule is a Tengo script. For a page it reads the strings url, host, path and title; for a project of " +
	"the user's it reads folder, remote and email, the project's folder, its git remote and its git user email, with url " +
	"and host set from the remote. The text, fmt, math and times modules are there without an import, and it sets result = true when the page or " +
	"project belongs. Example: " +
	"result = text.has_suffix(host, \"git.example.com\")"

func postRuleScript(cmd *cobra.Command, target, ask, current, model string) (string, string, error) {
	var history []Visit
	if err := CallInto(cmd.Context(), "history.get", map[string]any{"limit": 120}, &history); err != nil {
		return "", "", err
	}
	historyJSON, _ := json.Marshal(history)
	want := "What the user wants it to catch: " + ask
	if current != "" {
		want = fmt.Sprintf("The rule as it is now:\n%s\n\nHow the user wants it changed: %s", current, ask)
	}
	prompt := fmt.Sprintf("Write one browser rule for %s. When the user opens a page from outside the browser, the rule decides "+
		"whether the page belongs there.\n\n%s\n\n%s\n\nKeep the script short and "+
		"precise, matching hosts and paths rather than words in titles where it can. Give it a name of under five words. "+
		"Recent history, to see which hosts and paths the user means: %s", target, want, ruleLanguage, historyJSON)
	var written struct {
		Name   string `json:"name"`
		Script string `json:"script"`
	}
	finish := step("rule", "Writing the rule")
	err := PostClaude(cmd.Context(), ClaudeCall{Prompt: prompt, Schema: ruleScriptSchema, Model: model}, &written)
	finish(err)
	return written.Name, written.Script, err
}

func NewRuleCommand() *cobra.Command {
	rule := &cobra.Command{Use: "rule", Short: "Rules in Tengo that place a page opened from outside the browser into a space, group or section"}
	var getSpace string
	get := &cobra.Command{
		Use:     "get",
		Short:   "List the rules with their scope and script",
		Example: "slyb rule get --space {0c113bd0-93b5-437b-aca0-ecb4a8006e64}",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetRules()
			if err != nil {
				return err
			}
			rules := []Rule{}
			for _, entry := range store.Rules {
				if getSpace == "" || entry.Space == getSpace {
					rules = append(rules, entry)
				}
			}
			return printValue(cmd, rules)
		},
	}
	get.Flags().StringVar(&getSpace, "workspace", "", "Only the rules of this workspace")
	var name, space, groupID, section, script, file, ask, model string
	create := &cobra.Command{
		Use:   "create",
		Short: "Add a rule for a space, a group or a section: a Tengo script that sets result = true when a page belongs",
		Long: "The script runs for a page opened from outside the browser, with url, host, path and title set and the text,\n" +
			"fmt, math and times modules there without an import. Section rules run first, then group rules, then space rules; the first\n" +
			"that sets result = true places the page: it opens in that space, and with a group it lands in the group's\n" +
			"unverified pages, in the section when the rule has one. --ask lets the assistant write the script.",
		Example: "slyb rule create --group 3f9a2c1b7d4e --section Tst --ask \"the tst hosts of the app\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			scope, target := ScopeSpace, ""
			if groupID != "" {
				groups, err := GetGroups()
				if err != nil {
					return err
				}
				group, err := groups.Group(groupID)
				if err != nil {
					return err
				}
				space, scope, target = group.Space, ScopeGroup, "the group "+group.Title
				if section != "" {
					scope, target = ScopeSection, fmt.Sprintf("the section %s of the group %s", section, group.Title)
				}
			} else {
				if section != "" {
					return cli.Rejected("--section needs --group", "")
				}
				var err error
				if space, err = activeSpace(ctx, space); err != nil {
					return err
				}
				target = "a space of the browser"
			}
			if file != "" {
				content, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				script = string(content)
			}
			if strings.TrimSpace(ask) != "" {
				written, code, err := postRuleScript(cmd, target, ask, "", model)
				if err != nil {
					return err
				}
				script = code
				if name == "" {
					name = written
				}
			}
			if name == "" || strings.TrimSpace(script) == "" {
				return cli.Rejected("pass --name with --script or --file, or --ask", "")
			}
			made := Rule{ID: newID(), Name: name, Scope: scope, Space: space, Group: groupID, Section: section, Script: script, CreatedAt: now()}
			if _, err := runRule(ctx, made, RuleInput{URL: "https://example.org/"}); err != nil {
				return cli.Rejected(err.Error(), "")
			}
			store, err := GetRules()
			if err != nil {
				return err
			}
			store.Rules = append(store.Rules, made)
			if err := PutRules(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, made)
		},
	}
	create.Flags().StringVar(&name, "name", "", "What the rule is for")
	create.Flags().StringVar(&space, "workspace", "", "Workspace id of a workspace rule, the active workspace when neither --workspace nor --group is given")
	create.Flags().StringVar(&groupID, "group", "", "Make it a rule of this group")
	create.Flags().StringVar(&section, "section", "", "With --group, make it a rule of this section path")
	create.Flags().StringVar(&script, "script", "", "The Tengo script")
	create.Flags().StringVar(&file, "file", "", "Read the Tengo script from a file")
	create.Flags().StringVar(&ask, "ask", "", "Say what the rule should catch and let the assistant write the script")
	create.Flags().StringVar(&model, "model", "", "Claude model for --ask, the claude default when empty")
	remove := &cobra.Command{
		Use:     "delete <rule_id>",
		Short:   "Remove a rule",
		Example: "slyb rule delete 3f9a2c1b7d4e",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetRules()
			if err != nil {
				return err
			}
			kept := store.Rules[:0]
			found := false
			for _, entry := range store.Rules {
				if entry.ID == args[0] {
					found = true
					continue
				}
				kept = append(kept, entry)
			}
			if !found {
				return cli.Rejected("no rule with id "+args[0], "read the ids with `slyb rule get`")
			}
			store.Rules = kept
			if err := PutRules(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, map[string]any{"summary": "removed", "id": args[0]})
		},
	}
	var updateName, updateScript, updateFile, updateAsk, updateModel string
	update := &cobra.Command{
		Use:   "update <rule_id>",
		Short: "Rename a rule, replace its script, or say how it should change and let the assistant rewrite it",
		Long: "--ask gives the assistant the rule as it is now and what to change, and it writes the new script and name.\n" +
			"--name keeps its own value over the assistant's.",
		Example: "slyb rule update 3f9a2c1b7d4e --ask \"also catch the prd hosts\"",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetRules()
			if err != nil {
				return err
			}
			var found *Rule
			for index := range store.Rules {
				if store.Rules[index].ID == args[0] {
					found = &store.Rules[index]
				}
			}
			if found == nil {
				return cli.Rejected("no rule with id "+args[0], "read the ids with `slyb rule get`")
			}
			changed := *found
			if updateFile != "" {
				content, err := os.ReadFile(updateFile)
				if err != nil {
					return err
				}
				updateScript = string(content)
			}
			if strings.TrimSpace(updateScript) != "" {
				changed.Script = updateScript
			}
			if strings.TrimSpace(updateAsk) != "" {
				target := "a space of the browser"
				if changed.Group != "" {
					groups, err := GetGroups()
					if err != nil {
						return err
					}
					if group, err := groups.Group(changed.Group); err == nil {
						target = "the group " + group.Title
						if changed.Section != "" {
							target = fmt.Sprintf("the section %s of the group %s", changed.Section, group.Title)
						}
					}
				}
				written, code, err := postRuleScript(cmd, target, updateAsk, fmt.Sprintf("name: %s\nscript: %s", changed.Name, changed.Script), updateModel)
				if err != nil {
					return err
				}
				changed.Name, changed.Script = written, code
			}
			if name := strings.TrimSpace(updateName); name != "" {
				changed.Name = name
			}
			if changed == *found {
				return cli.Rejected("nothing to change", "pass --name, --script, --file or --ask")
			}
			if _, err := runRule(cmd.Context(), changed, RuleInput{URL: "https://example.org/"}); err != nil {
				return cli.Rejected(err.Error(), "")
			}
			*found = changed
			if err := PutRules(cmd.Context(), store); err != nil {
				return err
			}
			return printValue(cmd, changed)
		},
	}
	update.Flags().StringVar(&updateName, "name", "", "New name")
	update.Flags().StringVar(&updateScript, "script", "", "The new Tengo script")
	update.Flags().StringVar(&updateFile, "file", "", "Read the new Tengo script from a file")
	update.Flags().StringVar(&updateAsk, "ask", "", "Say how the rule should change and let the assistant rewrite it")
	update.Flags().StringVar(&updateModel, "model", "", "Claude model for --ask, the claude default when empty")
	var title string
	match := &cobra.Command{
		Use:     "match <url>",
		Short:   "Run the rules for a page and print the space, group and section it belongs in, empty when no rule decides",
		Example: "slyb rule match https://git.example.com/platform/hooks",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := GetRules()
			if err != nil {
				return err
			}
			result, err := MatchRules(cmd.Context(), store, Page{URL: args[0], Title: title})
			if err != nil {
				return err
			}
			return printValue(cmd, result)
		},
	}
	match.Flags().StringVar(&title, "title", "", "Page title the rules can read")
	rule.AddCommand(get, create, update, remove, match)
	return rule
}
