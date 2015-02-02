package cmd

import (
	"fmt"
	"io"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"

	admincmd "github.com/openshift/origin/pkg/admin/cmd"
	"github.com/openshift/origin/pkg/cmd/cli/cmd"
)

const longCommandActivateDesc = `
Activate OpenShift Node

This command enables OpenShift node to accept any new pods. // RAVI: fix me
`

// NewCommandActivateNode provides a CLI handler for node activation
func NewCommandActivateNode(name string, f *cmd.Factory, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s", name),
		Short: "Activate OpenShift node",
		Long:  longCommandActivateDesc,
		Run: func(c *cobra.Command, args []string) {
			fmt.Printf("Activated! testing...") //RAVI: fix me
		},
	}
}

const longCommandDeactivateDesc = `
Deactivate OpenShift Node

This command disables OpenShift node to accept any new pods. // RAVI: fix me
`

// NewCommandDeactivateNode provides a CLI handler for node deactivation
func NewCommandDeactivateNode(name string, f *cmd.Factory, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   fmt.Sprintf("%s", name),
		Short: "Deactivate OpenShift node",
		Long:  longCommandDeactivateDesc,
		Run: func(c *cobra.Command, args []string) {
			fmt.Printf("Deactivated! testing...") //RAVI: fix me
		},
	}
}

const longCommandEvacuateDesc = `
Evacuate all or specific pods from OpenShift node

This command evacuates all or specific pods. //RAVI: write more notes
`

// NewCommandEvacuateNode provides a CLI handler for node evacuation
func NewCommandEvacuateNode(name string, f *cmd.Factory, out io.Writer) *cobra.Command {
	config := admincmd.NewNodeConfig()

	cmd := &cobra.Command{
		Use:   fmt.Sprintf("%s", name),
		Short: "Evacuate all or specific pods from OpenShift node",
		Long:  longCommandEvacuateDesc,
		Run: func(c *cobra.Command, args []string) {
			if len(args) == 0 {
				fmt.Println("Node name not given!")
				return
			}

			_, kc, err := f.Clients(c)
			config.kc = kc
			if err != nil {
				glog.Fatalf("Error getting client: %v", err)
			}

			unknown := config.AddArguments(args)
			if len(unknown) != 0 {
				glog.Fatalf("Did not recognize the following arguments: %v", unknown)
			}

			result, err := config.Evacuate(out)
			if err != nil {
			}

			var nodes *kapi.NodeList
			nodes, err = kc.Nodes().List()
			checkErr(err)
			fmt.Println("Node List: ", nodes)

			var evacNode kapi.Node
			fmt.Printf("Current Node: ", evacNode)
			for _, node := range nodes.Items {
				if node.Name != args[0] {
					continue
				}
				if node.Status.Phase == kapi.NodeRunning {
					fmt.Printf("Node is running status and not allowed to evacuate!\n")
				}
				evacNode = node
			}
			if len(evacNode.Name) == 0 {
				fmt.Println("Given node", args[0], " not found!")
			}
			fmt.Println("Selected Node info: ", evacNode)

			var pods *kapi.PodList
			pods, err = kc.Pods(kapi.NamespaceAll).List(labels.Everything())
			checkErr(err)

			var rcs *kapi.ReplicationControllerList
			rcs, err = kc.ReplicationControllers(kapi.NamespaceAll).List(labels.Everything())

			var matchingRCs []kapi.ReplicationController
			var labelsToMatch labels.Labels
			var newPod *kapi.Pod

			for _, pod := range pods.Items {
				fmt.Println("Host: %v, Node name: %v, pod name: %v", pod.Status.Host, evacNode.Name, pod.Name)
				if pod.Status.Host != evacNode.Name {
					continue
				}

				labelsToMatch = labels.Set(pod.Labels)
				fmt.Println("LabelsToMatch: ", labelsToMatch)
				for _, controller := range rcs.Items {
					selector := labels.SelectorFromSet(controller.Spec.Selector)
					if selector.Matches(labelsToMatch) {
						matchingRCs = append(matchingRCs, controller)
					}
				}

				if len(matchingRCs) > 0 {
					fmt.Println("Matching RC: ", matchingRCs)
					err = kc.Pods(pod.Namespace).Delete(pod.Name)
					checkErr(err)
				} else {
					fmt.Println("No replcation controller found")
					fmt.Println("Old Pod: ", pod)
					err = kc.Pods(pod.Namespace).Delete(pod.Name)
					checkErr(err)
					fmt.Println("Old Pod after deletion: ", pod)
					pod.ObjectMeta.ResourceVersion = ""
					newPod, err = kc.Pods(pod.Namespace).Create(&pod)
					checkErr(err)
					fmt.Println("New Pod: ", newPod)
				}
			}
		},
	}
}

func checkErr(err error) {
	if err != nil {
		glog.FatalDepth(1, err)
	}
}
