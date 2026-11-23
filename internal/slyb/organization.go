package slyb

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/nimling/nimbody/cli"
	"github.com/spf13/cobra"
)

const organizationSeed = `# How the tabs are organized

These rules steer every Organize. Claude Code rewrites them as it learns how the user wants the browser organized; slyb organization get shows them with the pages the user kept or dismissed since.

1. Groups are named for what the work is, in one to three words, never for the site.
2. A tab that belongs to a project goes in the group for that project, in a section per environment when it has several.
3. Reference pages, docs and specs sit apart from tasks in progress.
4. Shopping, news and entertainment never share a group with work.
5. Pages Claude adds stay unverified until the user opens them; a dismissed page never goes back into that group.
`

type Learned struct {
	At      string `json:"at"`
	Action  string `json:"action"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Group   string `json:"group"`
	Section string `json:"section"`
}

type Organization struct {
	Path    string    `json:"path"`
	Rules   string    `json:"rules"`
	Learned []Learned `json:"learned"`
}

func organizationPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "organization.md"), nil
}

func GetOrganization(limit int) (Organization, error) {
	path, err := organizationPath()
	if err != nil {
		return Organization{}, err
	}
	rules, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Organization{}, err
	}
	organization := Organization{Path: path, Rules: string(rules), Learned: []Learned{}}
	home, err := Home()
	if err != nil {
		return Organization{}, err
	}
	file, err := os.Open(filepath.Join(home, "learned.jsonl"))
	if os.IsNotExist(err) {
		return organization, nil
	}
	if err != nil {
		return Organization{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		var entry Learned
		if json.Unmarshal(scanner.Bytes(), &entry) == nil {
			organization.Learned = append(organization.Learned, entry)
		}
	}
	if len(organization.Learned) > limit {
		organization.Learned = organization.Learned[len(organization.Learned)-limit:]
	}
	return organization, scanner.Err()
}

func NewOrganizationCommand() *cobra.Command {
	organization := &cobra.Command{Use: "organization", Short: "Read and rewrite the rules Organize follows, and the pages the user kept or dismissed"}

	var limit int
	get := &cobra.Command{
		Use:     "get",
		Short:   "Show the organization rules and the newest pages the user kept or dismissed from a group",
		Example: "slyb organization get --limit 50",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := GetOrganization(limit)
			if err != nil {
				return err
			}
			return printValue(cmd, value)
		},
	}
	get.Flags().IntVar(&limit, "limit", 200, "Most kept or dismissed pages to show")

	var rules, file string
	update := &cobra.Command{
		Use:     "update",
		Short:   "Replace the organization rules, so every later Organize follows them",
		Example: "slyb organization update --file rules.md",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file != "" {
				content, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				rules = string(content)
			}
			if rules == "" {
				return cli.Rejected("pass --rules or --file", "slyb organization update --file rules.md")
			}
			path, err := organizationPath()
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(rules), 0o600); err != nil {
				return err
			}
			value, err := GetOrganization(0)
			if err != nil {
				return err
			}
			return printValue(cmd, value)
		},
	}
	update.Flags().StringVar(&rules, "rules", "", "The rules as markdown")
	update.Flags().StringVar(&file, "file", "", "Read the rules from a file")

	organization.AddCommand(get, update)
	return organization
}
