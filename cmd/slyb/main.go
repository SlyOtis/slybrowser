package main

import (
	"fmt"
	"os"

	"github.com/nimling/nimbody/cli"
	slybrowser "github.com/slyotis/slybrowser"
	"github.com/slyotis/slybrowser/internal/slyb"
	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:     "slyb",
		Short:   "Control the slybrowser build of Zen: tabs, folders, spaces, sidebar, history and Claude sorting",
		Version: slyb.GetVersion(),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), slybrowser.Skill)
			return err
		},
	}
	defaultHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			fmt.Fprint(cmd.OutOrStdout(), slybrowser.Skill)
			return
		}
		defaultHelp(cmd, args)
	})
	slyb.AddFlags(root)

	root.AddCommand(
		slyb.NewStatusCommand(),
		slyb.NewTabCommand(),
		slyb.NewGroupCommand(),
		slyb.NewOrganizeCommand(),
		slyb.NewFindingCommand(),
		slyb.NewAppsCommand(),
		slyb.NewProfileCommand(),
		slyb.NewWelcomeCommand(),
		slyb.NewSummaryCommand(),
		slyb.NewRuleCommand(),
		slyb.NewForgottenCommand(),
		slyb.NewWorkCommand(),
		slyb.NewPageCommand(),
		slyb.NewLiveCommand(),
		slyb.NewThemeCommand(),
		slyb.NewSuggestionCommand(),
		slyb.NewWorkspaceCommand(),
		slyb.NewSpaceCommand(),
		slyb.NewSidebarCommand(),
		slyb.NewHistoryCommand(),
		slyb.NewSearchCommand(),
		slyb.NewProjectCommand(),
		slyb.NewOrganizationCommand(),
		slyb.NewEvalCommand(),
		slyb.NewRestartCommand(),
		slyb.NewRemoteCommand(),
		slyb.NewInstallCommand(slybrowser.Browser),
	)
	root.AddCommand(cli.SkillCommand("slyb", slybrowser.Skill))
	root.AddCommand(cli.PluginCommand(cli.Plugin{
		Name:        "slyb",
		Owner:       "Slytech",
		Description: "Control the slybrowser build of Zen from Claude: tabs, folders, spaces, sidebar, history and Claude sorting",
		Skill:       slybrowser.Skill,
		Version:     slyb.GetVersion,
	}))
	root.AddCommand(cli.MCPCommand(root, "slyb", slyb.GetVersion))
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the slyb build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), slyb.GetVersion())
			return err
		},
	})

	os.Exit(cli.Execute(root))
}
