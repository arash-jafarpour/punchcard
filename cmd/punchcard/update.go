package main

import (
	"github.com/spf13/cobra"

	"github.com/arash-jafarpour/punchcard/internal/update"
)

// repoSlug is the "owner/name" GitHub repository that hosts the releases.
const repoSlug = "arash-jafarpour/punchcard"

// newUpdateCommand replaces the running binary with the latest release.
func newUpdateCommand() *cobra.Command {
	var check bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update punchcard to the latest release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return update.Run(cmd.Context(), update.Options{
				CurrentVersion: buildVersion,
				RepoSlug:       repoSlug,
				CheckOnly:      check,
				Out:            cmd.OutOrStdout(),
			})
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only check for a newer version")

	return cmd
}
