package slyb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/d5/tengo/v2"
	"github.com/d5/tengo/v2/stdlib"
	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

const (
	ScopeSpace   = "space"
	ScopeGroup   = "group"
	ScopeSection = "section"
)

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

func GetRules() (RuleStore, error) {
	path, err := homeFile("rules.json")
	if err != nil {
		return RuleStore{}, err
	}
	store := RuleStore{Rules: []Rule{}}
	if err := readJSONFile(path, &store); err != nil && !os.IsNotExist(err) {
		return RuleStore{}, err
	}
	return store, nil
}

func PutRules(store RuleStore) error {
	path, err := homeFile("rules.json")
	if err != nil {
		return err
	}
	return writeJSONFile(path, store)
}

type RuleInput struct {
	URL    string
	Title  string
	Folder string
	Remote string
	Email  string
}

func runRule(ctx context.Context, rule Rule, page RuleInput) (bool, error) {
	parsed, _ := url.Parse(page.URL)
	host, path := "", ""
	if parsed != nil {
		host, path = strings.ToLower(parsed.Hostname()), parsed.EscapedPath()
	}
	script := tengo.NewScript([]byte(rule.Script))
	script.SetImports(stdlib.GetModuleMap("text", "fmt", "strings"))
	for name, value := range map[string]any{"url": page.URL, "host": host, "path": path, "title": page.Title, "folder": page.Folder, "remote": page.Remote, "email": page.Email, "result": false} {
		if err := script.Add(name, value); err != nil {
			return false, err
		}
	}
	compiled, err := script.Compile()
	if err != nil {
		return false, fmt.Errorf("rule %s does not compile: %w", rule.Name, err)
	}
	run, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := compiled.RunContext(run); err != nil {
		return false, fmt.Errorf("rule %s failed: %w", rule.Name, err)
	}
	return compiled.Get("result").Bool(), nil
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
	"and host set from the remote. It may import the text, fmt and strings modules, and sets result = true when the page or " +
	"project belongs. Example: text := import(\"text\"); " +
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
			"fmt and strings modules importable. Section rules run first, then group rules, then space rules; the first\n" +
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
			if err := PutRules(store); err != nil {
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
			if err := PutRules(store); err != nil {
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
			if err := PutRules(store); err != nil {
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
