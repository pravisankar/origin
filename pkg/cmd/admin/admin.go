package admin

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	admincmd "github.com/openshift/origin/pkg/cmd/admin/cmd"
	"github.com/openshift/origin/pkg/cmd/cli"
)

const longDesc = `
OpenShift Admin Client

The OpenShift admin client exposes commands for admin tasks to manage your resources in the cluster. At present, it supports node activation, node deactivation and node evacuation.
`

func NewCommandAdmin(name string) *cobra.Command {
	// Main command
	cmds := &cobra.Command{
		Use:     name,
		Short:   "Admin tools for OpenShift",
		Long:    fmt.Sprintf(longDesc, name),
		Run: func(c *cobra.Command, args []string) {
			c.Help()
		},
	}

	f := cli.OpenShiftClientFactory(cmds)
	out := os.Stdout

	// Admin commands
	cmds.AddCommand(admincmd.NewCommandActivateNode("activate-node", f, out))
	cmds.AddCommand(admincmd.NewCommandDeactivateNode("deactivate-node", f, out))
	cmds.AddCommand(admincmd.NewCommandEvacuateNode("evacuate-node", f, out))

	return cmds
}
