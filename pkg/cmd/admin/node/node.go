package node

import (
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl"
	kcmdutil "github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/cmd/util"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/kubectl/resource"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"

	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
)

const ManageNodeCommandName = "manage-node"

type NodeConfig struct {
	CmdFactory *clientcmd.Factory
	Kclient    *client.Client
	Writer     io.Writer

	// Supported operations
	Schedulable bool
	Evacuate    bool
	ListPods    bool

	// Optional arguments
	Selector    string
	PodSelector string
	DryRun      bool
}

const manageNodeLongDesc = `Manage node operations: schedulable, evacuate and list-pods.

Examples:

	# Block the node accepting any new pods
	$ %[1]s manage-node <mynode> --schedulable=false

	# Move selected pods to a different node
	$ %[1]s manage-node <mynode> --evacuate --pod-selector="service=myapp"

	# List all pods on nodes
	$ %[1]s manage-node <mynode1> <mynode2> --list-pods

	# Mark selected nodes as schedulable
	$ %[1]s manage-node --selector="env=dev" --schedulable=true
`

func NewCommandManageNode(f *clientcmd.Factory, fullName, commandName string, out io.Writer) *cobra.Command {
	cfg := NewNodeConfig()

	cmd := &cobra.Command{
		Use:   commandName,
		Short: "Manage node operations: schedulable, evacuate, list-pods",
		Long:  fmt.Sprintf(manageNodeLongDesc, fullName),
		Run: func(c *cobra.Command, args []string) {
			err := cfg.RunManageNode(f, out, c, args)
			kcmdutil.CheckErr(err)
		},
	}
	flags := cmd.Flags()

	flags.BoolVar(&cfg.Schedulable, "schedulable", true, "Control pod schedulability on the node.")
	flags.BoolVar(&cfg.Evacuate, "evacuate", true, "Migrate all/selected pods on the node.")
	flags.BoolVar(&cfg.ListPods, "list-pods", true, "List all/selected pods on the node.")

	flags.StringVar(&cfg.PodSelector, "pod-selector", "", "Label selector to filter pods on the node. Optional parameters for --evacuate or --list-pods")
	flags.BoolVar(&cfg.DryRun, "dry-run", false, "Don't perform evacuate but show pods that will be migrated. Optional param for --evacuate")
	flags.StringVar(&cfg.Selector, "selector", "", "Label selector to filter nodes. Either pass one/more nodes as arguments or use this node selector")

	return cmd
}

// RunManageNode contains all the necessary functionality for the openshift cli manage-node command
func (cfg *NodeConfig) RunManageNode(f *clientcmd.Factory, out io.Writer, cmd *cobra.Command, args []string) error {
	errList := []error{}
	if errList = cfg.ValidateParams(cmd, args); len(errList) != 0 {
		err := fmt.Errorf("(%v)", errList)
		return kcmdutil.UsageError(cmd, err.Error())
	}
	if err := cfg.Configure(f, out); err != nil {
		return err
	}

	cmdNamespace, err := f.DefaultNamespace()
	if err != nil {
		return err
	}
	nameArgs := []string{"nodes"}
	if len(args) != 0 {
		nameArgs = append(nameArgs, args...)
	}

	mapper, typer := f.Object()
	r := resource.NewBuilder(mapper, typer, f.ClientMapperForCommand()).
		ContinueOnError().
		NamespaceParam(cmdNamespace).
		SelectorParam(cfg.Selector).
		ResourceTypeOrNameArgs(true, nameArgs...).
		Flatten().
		Do()
	if r.Err() != nil {
		return r.Err()
	}

	nodeCount := 0
	ignoreHeaders := false
	err = r.Visit(func(info *resource.Info) error {
		nodeCount++
		err = nil
		node, ok := info.Object.(*kapi.Node)
		if !ok {
			err = fmt.Errorf("cannot convert input to Node: ", reflect.TypeOf(info.Object))
			errList = append(errList, err)
			return nil
		}

		if cmd.Flag("schedulable").Changed {
			err = cfg.RunSchedulable(node, ignoreHeaders)
		} else if cmd.Flag("evacuate").Changed && cfg.Evacuate {
			if cfg.DryRun {
				err = cfg.RunListPods(node)
			} else {
				err = cfg.RunEvacuate(node)
			}
		} else if cmd.Flag("list-pods").Changed && cfg.ListPods {
			err = cfg.RunListPods(node)
		}
		ignoreHeaders = true

		if err != nil {
			errList = append(errList, err)
			return nil
		}
		return nil
	})
	if len(errList) != 0 {
		return fmt.Errorf("(%v)", errList)
	}
	if nodeCount == 0 {
		return fmt.Errorf("No nodes found")
	}
	return nil
}

func NewNodeConfig() *NodeConfig {
	return &NodeConfig{}
}

func (cfg *NodeConfig) ValidateParams(c *cobra.Command, args []string) []error {
	errList := []error{}
	if _, err := labels.Parse(cfg.Selector); err != nil {
		errList = append(errList, errors.New("--selector must be a valid label selector"))
	}
	if !c.Flag("selector").Changed && len(args) == 0 {
		errList = append(errList, errors.New("either specify --selector or nodes"))
	} else if c.Flag("selector").Changed && len(args) != 0 {
		errList = append(errList, errors.New("either specify --selector or nodes but not both"))
	}

	numOps := 0
	if c.Flag("schedulable").Changed {
		numOps += 1
	}
	if c.Flag("evacuate").Changed {
		numOps += 1
	}
	if c.Flag("list-pods").Changed {
		numOps += 1
	}

	if numOps == 0 {
		errList = append(errList, errors.New("must provide a node operation. Supported operations: --schedulable, --evacuate and --list-pods"))
	} else if numOps != 1 {
		errList = append(errList, errors.New("must provide only one node operation at a time"))
	}

	if cfg.DryRun && !cfg.Evacuate {
		errList = append(errList, errors.New("--dry-run is only applicable for --evacuate"))
	}
	if _, err := labels.Parse(cfg.PodSelector); err != nil {
		errList = append(errList, errors.New("--pod-selector must be a valid label selector"))
	}
	return errList
}

func (cfg *NodeConfig) Configure(f *clientcmd.Factory, out io.Writer) error {
	_, kc, err := f.Clients()
	if err != nil {
		return err
	}
	cfg.Kclient = kc
	cfg.CmdFactory = f
	cfg.Writer = out
	return nil
}

func (cfg *NodeConfig) RunSchedulable(node *kapi.Node, ignoreHeaders bool) error {
	if cfg.Schedulable {
		if node.Spec.Unschedulable {
			node.Spec.Unschedulable = false
		}
	} else {
		if !node.Spec.Unschedulable {
			node.Spec.Unschedulable = true
		}
	}

	updatedNode, err := cfg.Kclient.Nodes().Update(node)
	if err != nil {
		return err
	}

	printerWithHeaders, printerNoHeaders, err := cfg.getPrinters("", updatedNode)
	if err != nil {
		return err
	}
	if ignoreHeaders {
		printerNoHeaders.PrintObj(updatedNode, cfg.Writer)
	} else {
		printerWithHeaders.PrintObj(updatedNode, cfg.Writer)
	}
	return nil
}

func (cfg *NodeConfig) RunEvacuate(node *kapi.Node) error {
	if !node.Spec.Unschedulable {
		return fmt.Errorf("Node '%s' must be unschedulable to perform evacuation.\nYou can mark the node unschedulable with openshift admin manage-node %s --schedulable=false", node.ObjectMeta.Name, node.ObjectMeta.Name)
	}

	var selector labels.Selector
	if len(cfg.PodSelector) == 0 {
		selector = labels.Everything()
	} else {
		var err error
		selector, err = labels.Parse(cfg.PodSelector)
		if err != nil {
			return err
		}
	}

	pods, err := cfg.Kclient.Pods(kapi.NamespaceAll).List(selector, fields.Everything())
	if err != nil {
		return err
	}
	rcs, err := cfg.Kclient.ReplicationControllers(kapi.NamespaceAll).List(labels.Everything())
	if err != nil {
		return err
	}

	printerWithHeaders, printerNoHeaders, err := cfg.getPrinters("pod", nil)
	if err != nil {
		return err
	}
	errList := []error{}
	firstPod := true
	grace := int64(0)
	deleteOptions := &kapi.DeleteOptions{GracePeriodSeconds: &grace}

	for _, pod := range pods.Items {
		if pod.Spec.Host != node.ObjectMeta.Name {
			continue
		}

		foundrc := false
		for _, rc := range rcs.Items {
			selector := labels.SelectorFromSet(rc.Spec.Selector)
			if selector.Matches(labels.Set(pod.Labels)) {
				foundrc = true
				break
			}
		}

		if firstPod {
			fmt.Fprintln(cfg.Writer, "\nMigrating these pods on node: ", node.ObjectMeta.Name, "\n")
			firstPod = false
			printerWithHeaders.PrintObj(&pod, cfg.Writer)
		} else {
			printerNoHeaders.PrintObj(&pod, cfg.Writer)
		}

		if err := cfg.Kclient.Pods(pod.Namespace).Delete(pod.Name, deleteOptions); err != nil {
			glog.Errorf("Unable to delete a pod: %+v, error: %v", pod, err)
			errList = append(errList, err)
			continue
		}
		if !foundrc {
			pod.ObjectMeta.ResourceVersion = ""
			pod.Spec.Host = ""
			pod.Status = kapi.PodStatus{}
			_, err := cfg.Kclient.Pods(pod.Namespace).Create(&pod)
			if err != nil {
				glog.Errorf("Unable to create a pod: %+v, error: %v", pod, err)
				errList = append(errList, err)
				continue
			}
		}
	}
	if len(errList) != 0 {
		return fmt.Errorf("(%v)", errList)
	}
	return nil
}

func (cfg *NodeConfig) RunListPods(node *kapi.Node) error {
	var selector labels.Selector
	if len(cfg.PodSelector) == 0 {
		selector = labels.Everything()
	} else {
		var err error
		selector, err = labels.Parse(cfg.PodSelector)
		if err != nil {
			return err
		}
	}

	pods, err := cfg.Kclient.Pods(kapi.NamespaceAll).List(selector, fields.Everything())
	if err != nil {
		return err
	}

	printerWithHeaders, printerNoHeaders, err := cfg.getPrinters("pod", nil)
	if err != nil {
		return err
	}
	firstPod := true

	for _, pod := range pods.Items {
		if pod.Spec.Host != node.ObjectMeta.Name {
			continue
		}

		if firstPod {
			fmt.Fprintln(cfg.Writer, "\nListing matched pods on node: ", node.ObjectMeta.Name, "\n")
			printerWithHeaders.PrintObj(&pod, cfg.Writer)
			firstPod = false
		} else {
			printerNoHeaders.PrintObj(&pod, cfg.Writer)
		}
	}
	return err
}

func (cfg *NodeConfig) getPrinters(resource string, obj runtime.Object) (kubectl.ResourcePrinter, kubectl.ResourcePrinter, error) {
	var mapping *meta.RESTMapping
	mapper, _ := cfg.CmdFactory.Object()

	if len(resource) == 0 {
		version, kind, err := kapi.Scheme.ObjectVersionAndKind(obj)
		if err != nil {
			return nil, nil, err
		}
		mapping, err = mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, nil, err
		}
	} else {
		version, kind, err := mapper.VersionAndKindForResource(resource)
		mapping, err = mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, nil, err
		}
	}

	printerWithHeaders, err := cfg.CmdFactory.Printer(mapping, false)
	if err != nil {
		return nil, nil, err
	}
	printerNoHeaders, err := cfg.CmdFactory.Printer(mapping, true)
	if err != nil {
		return nil, nil, err
	}
	return printerWithHeaders, printerNoHeaders, nil
}
