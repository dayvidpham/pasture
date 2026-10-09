package main

import (
	"github.com/spf13/cobra"

	"github.com/dayvidpham/pasture/internal/handlers"
)

// newTaskAgentsRegisterCmd builds the `pasture task agents register` verb.
//
// Registration is the only way to obtain an author identity for
// `pasture task comment add --author`; that command never creates identities
// implicitly. The created ID is printed so it can be copied into --author.
//
// The project namespace comes from the global --namespace flag and defaults
// exactly like task creation (derived from the git remote, then the working
// directory), so an author and its tasks share one namespace by default.
func newTaskAgentsRegisterCmd() *cobra.Command {
	registerCmd := &cobra.Command{
		Use:   "register",
		Short: "Register a new author identity for task comments",
		Long: `Register a new author identity and print its ID.

Registration is the only way to obtain an author for ` + "`pasture task comment add`" + `,
which requires an explicit --author and never creates identities implicitly.
The printed ID can be copied straight into --author.

Subcommands:
  human     register a person (--name, optional --contact)
  software  register a program (--name, --version, optional --source)
  ml        register a machine-learning model (--role, --provider, --model)

The project namespace comes from the global --namespace flag. When omitted it
is derived exactly like task creation (the git remote, then the working
directory).`,
	}

	registerCmd.AddCommand(newTaskAgentsRegisterHumanCmd())
	registerCmd.AddCommand(newTaskAgentsRegisterSoftwareCmd())
	registerCmd.AddCommand(newTaskAgentsRegisterMLCmd())
	return registerCmd
}

func newTaskAgentsRegisterHumanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "human",
		Short: "Register a human author identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			contact, _ := cmd.Flags().GetString("contact")
			code, err := handlers.TaskAgentsRegister(cmd.OutOrStdout(), handlers.TaskAgentRegisterInput{
				DBPath:    flagDBPath,
				Namespace: flagNamespace,
				Kind:      handlers.AgentRegisterHuman,
				Name:      name,
				Contact:   contact,
			}, resolveFormat())
			return finishTaskCommand(code, err)
		},
	}
	cmd.Flags().String("name", "", "Display name for the person")
	cmd.Flags().String("contact", "", "Optional contact details (email, handle, etc.)")
	return cmd
}

func newTaskAgentsRegisterSoftwareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "software",
		Short: "Register a software author identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			version, _ := cmd.Flags().GetString("version")
			source, _ := cmd.Flags().GetString("source")
			code, err := handlers.TaskAgentsRegister(cmd.OutOrStdout(), handlers.TaskAgentRegisterInput{
				DBPath:    flagDBPath,
				Namespace: flagNamespace,
				Kind:      handlers.AgentRegisterSoftware,
				Name:      name,
				Version:   version,
				Source:    source,
			}, resolveFormat())
			return finishTaskCommand(code, err)
		},
	}
	cmd.Flags().String("name", "", "Name of the software")
	cmd.Flags().String("version", "", "Version of the software build")
	cmd.Flags().String("source", "", "Optional source location (repository or URL)")
	return cmd
}

func newTaskAgentsRegisterMLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ml",
		Short: "Register a machine-learning author identity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			role, _ := cmd.Flags().GetString("role")
			provider, _ := cmd.Flags().GetString("provider")
			model, _ := cmd.Flags().GetString("model")
			code, err := handlers.TaskAgentsRegister(cmd.OutOrStdout(), handlers.TaskAgentRegisterInput{
				DBPath:    flagDBPath,
				Namespace: flagNamespace,
				Kind:      handlers.AgentRegisterML,
				Role:      role,
				Provider:  provider,
				Model:     model,
			}, resolveFormat())
			return finishTaskCommand(code, err)
		},
	}
	cmd.Flags().String("role", "", "Role the model plays: human, architect, supervisor, worker, reviewer")
	cmd.Flags().String("provider", "", "Model provider (e.g. anthropic, openai, google)")
	cmd.Flags().String("model", "", "Model identifier (e.g. claude-opus-4-6)")
	return cmd
}
