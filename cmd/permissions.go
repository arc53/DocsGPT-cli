package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/arc53/DocsGPT-cli/internal/display"
	"github.com/arc53/DocsGPT-cli/internal/tools"
	"github.com/arc53/DocsGPT-cli/internal/ui"

	"github.com/spf13/cobra"
)

var permissionsCmd = &cobra.Command{
	Use:   "permissions",
	Short: "The tool calls approved for good in each project",
	Long: `"Always allow" and "Always approve" are kept for the project you choose them
in (the git root, else the directory), in ~/.docsgpt/permissions.json: later
chats and questions there run those calls without asking. Nothing a
repository holds can approve anything.`,
	Example: `  docsgpt-cli permissions list
  docsgpt-cli permissions reset
  docsgpt-cli permissions list --all`,
}

var permissionsAll bool

var permissionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show what runs without asking in this project",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := tools.AllApprovals()
		if err != nil {
			return err
		}
		projects := []string{currentProject()}
		if permissionsAll {
			projects = projects[:0]
			for p := range all {
				projects = append(projects, p)
			}
			slices.Sort(projects)
		}
		shown := false
		for _, p := range projects {
			a := all[p]
			if a.Empty() {
				continue
			}
			shown = true
			fmt.Println(display.Safe(homePath(p)) + display.Dim(" · "+display.Ago(a.Updated)))
			for _, line := range a.Describe() {
				fmt.Println("  " + display.Safe(line))
			}
		}
		if !shown {
			fmt.Println("Nothing runs without asking in " + where() + ".")
		}
		return nil
	},
}

var permissionsResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Ask again before every tool call in this project",
	Args:  usageArgs(cobra.NoArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, err := tools.AllApprovals()
		if err != nil {
			return err
		}
		projects := []string{currentProject()}
		if permissionsAll {
			projects = projects[:0]
			for p := range all {
				projects = append(projects, p)
			}
		}
		n := 0
		for _, p := range projects {
			if all[p].Empty() {
				continue
			}
			if err := tools.UpdateApprovals(p, func(a *tools.Approvals) { *a = tools.Approvals{} }); err != nil {
				return err
			}
			n++
		}
		if n == 0 {
			fmt.Println("Nothing runs without asking in " + where() + ".")
			return nil
		}
		fmt.Printf("Forgot the approvals of %d %s.\n", n, plural(n, "project", "projects"))
		return nil
	},
}

func init() {
	groupCommand(permissionsCmd)
	permissionsCmd.AddCommand(permissionsListCmd, permissionsResetCmd)
	for _, c := range []*cobra.Command{permissionsListCmd, permissionsResetCmd} {
		c.Flags().BoolVar(&permissionsAll, "all", false, "every project, not only this one")
	}
}

// currentProject is the project of the working directory.
func currentProject() string {
	cwd, _ := os.Getwd()
	return tools.ProjectRoot(cwd)
}

// homePath shows a project's path in full, ~ for the home directory.
func homePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && filepath.IsLocal(rel) {
			return filepath.Join("~", rel)
		}
	}
	return p
}

func where() string {
	if permissionsAll {
		return "any project"
	}
	return "this project (" + display.Safe(homePath(currentProject())) + ")"
}

// permissions shows what runs without asking in the chat's project
// (/permissions): pick one to ask about it again, or all of them.
func (s *chatSession) permissions(string) {
	if s.tools.Project == "" {
		s.note("Tools are off (--no-tools).")
		return
	}
	for {
		a, err := tools.ProjectApprovals(s.tools.Project)
		if err != nil {
			s.fail(err.Error())
			return
		}
		lines := a.Describe()
		if len(lines) == 0 {
			s.note("Nothing runs without asking in this project (" + homePath(s.tools.Project) + "). \"Always allow\" and \"Always approve\" are kept here.")
			return
		}
		items := []ui.Item{{Label: "Ask about all of them again", Value: "\x00all", Description: "forget every approval of this project"}}
		for _, l := range lines {
			items = append(items, ui.Item{Label: display.Safe(l), Value: l, Description: "ask about it again"})
		}
		v, err := s.scr.Select(ui.Select{Title: "Runs without asking in " + display.Safe(homePath(s.tools.Project)), Items: items, Filter: len(items) > 8})
		if err != nil {
			return
		}
		if v == "\x00all" {
			err = s.tools.ForgetApprovals(globalAutoApprove)
		} else {
			err = s.tools.Forget(v)
		}
		if err != nil {
			s.fail(err.Error())
			return
		}
		s.footer()
		if v == "\x00all" {
			s.scr.Add(display.Done("Every tool call of this project asks again."))
			return
		}
		s.scr.Add(display.Done("Asks again: " + strings.TrimSpace(v)))
	}
}
