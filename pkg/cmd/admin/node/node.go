package node

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

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
	"github.com/GoogleCloudPlatform/kubernetes/pkg/util"

	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
)

const ManageNodeCommandName = "manage-node"

type NodeConfig struct {
	DefaultNamespace string
	Kclient          *client.Client
	Writer           io.Writer

	Mapper            meta.RESTMapper
	Typer             runtime.ObjectTyper
	RESTClientFactory func(mapping *meta.RESTMapping) (resource.RESTClient, error)
	Printer           func(mapping *meta.RESTMapping, noHeaders bool) (kubectl.ResourcePrinter, error)

	Operations []string
	NodeNames  []string

	// Supported operations
	Schedulable bool
	Evacuate    bool
	ListPods    bool

	// Optional arguments
	Selector    util.StringFlag
	PodSelector util.StringFlag
	DryRun      bool
}

const manageNodeLongDesc = `Manage node operations: schedulable, evacuate and list-pods.

Examples:

	# Block accepting any new pods on nodes
	$ %[1]s <mynode> --schedulable=false

	# Move selected pods to a different node
	$ %[1]s <mynode> --evacuate --pod-selector="<service=myapp>"

	# List all pods on nodes
	$ %[1]s <mynode1> <mynode2> --list-pods

	# Mark selected nodes as schedulable
	$ %[1]s --selector="<env=dev>" --schedulable=true
`

func NewCommandManageNode(f *clientcmd.Factory, commandName, fullName string, out io.Writer) *cobra.Command {
	cfg := &NodeConfig{}

	cmd := &cobra.Command{
		Use:   commandName,
		Short: "Manage node operations: schedulable, evacuate, list-pods",
		Long:  fmt.Sprintf(manageNodeLongDesc, fullName),
		Run: func(c *cobra.Command, args []string) {

			defaultNamespace, err := f.DefaultNamespace()
			kcmdutil.CheckErr(err)

			_, kc, err := f.Clients()
			kcmdutil.CheckErr(err)

			mapper, typer := f.Object()

			cfg.DefaultNamespace = defaultNamespace
			cfg.Kclient = kc
			cfg.Writer = out
			cfg.Mapper = mapper
			cfg.Typer = typer
			cfg.RESTClientFactory = f.Factory.RESTClient
			cfg.Printer = f.Printer
			cfg.Operations = []string{}
			cfg.NodeNames = []string{}

			if c.Flag("schedulable").Changed {
				cfg.Operations = append(cfg.Operations, "schedulable")
			}
			if c.Flag("evacuate").Changed {
				cfg.Operations = append(cfg.Operations, "evacuate")
			}
			if c.Flag("list-pods").Changed {
				cfg.Operations = append(cfg.Operations, "list-pods")
			}

			if len(args) != 0 {
				cfg.NodeNames = append(cfg.NodeNames, args...)
			}

			errList := []error{}
			if errList = cfg.ValidateParams(); len(errList) != 0 {
				err := fmt.Errorf("%v", errList)
				kcmdutil.CheckErr(kcmdutil.UsageError(c, err.Error()))
			}

			err = cfg.RunManageNode()
			kcmdutil.CheckErr(err)
		},
	}
	flags := cmd.Flags()

	flags.BoolVar(&cfg.Schedulable, "schedulable", false, "Control pod schedulability on the node.")
	flags.BoolVar(&cfg.Evacuate, "evacuate", false, "Migrate all/selected pods on the node.")
	flags.BoolVar(&cfg.ListPods, "list-pods", false, "List all/selected pods on the node.")

	flags.BoolVar(&cfg.DryRun, "dry-run", false, "Show pods that will be migrated. Optional param for --evacuate")
	flags.Var(&cfg.PodSelector, "pod-selector", "Label selector to filter pods on the node. Optional parameters for --evacuate or --list-pods")
	flags.Var(&cfg.Selector, "selector", "Label selector to filter nodes. Either pass one/more nodes as arguments or use this node selector")

	return cmd
}

func (cfg *NodeConfig) ValidateParams() []error {
	errList := []error{}
	if cfg.Selector.Provided() {
		if _, err := labels.Parse(cfg.Selector.Value()); err != nil {
			errList = append(errList, errors.New("--selector=<node_selector> must be a valid label selector"))
		}
		if len(cfg.NodeNames) != 0 {
			errList = append(errList, errors.New("either specify --selector=<node_selector> or nodes but not both"))
		}
	} else if len(cfg.NodeNames) == 0 {
		errList = append(errList, errors.New("either specify --selector=<node_selector> or nodes"))
	}

	numOps := len(cfg.Operations)
	if numOps == 0 {
		errList = append(errList, errors.New("must provide a node operation. Supported operations: --schedulable, --evacuate and --list-pods"))
	} else if numOps != 1 {
		errList = append(errList, errors.New("must provide only one node operation at a time"))
	}

	if cfg.DryRun && !cfg.Evacuate {
		errList = append(errList, errors.New("--dry-run is only applicable for --evacuate"))
	}

	if cfg.PodSelector.Provided() {
		if _, err := labels.Parse(cfg.PodSelector.Value()); err != nil {
			errList = append(errList, errors.New("--pod-selector=<pod_selector> must be a valid label selector"))
		}
	}
	return errList
}

// RunManageNode contains all the necessary functionality for the openshift cli manage-node command
func (cfg *NodeConfig) RunManageNode() error {
	nameArgs := []string{"nodes"}
	if len(cfg.NodeNames) != 0 {
		nameArgs = append(nameArgs, cfg.NodeNames...)
	}

	r := resource.NewBuilder(cfg.Mapper, cfg.Typer, resource.ClientMapperFunc(cfg.RESTClientFactory)).
		ContinueOnError().
		NamespaceParam(cfg.DefaultNamespace).
		SelectorParam(cfg.Selector.Value()).
		ResourceTypeOrNameArgs(true, nameArgs...).
		Flatten().
		Do()
	if r.Err() != nil {
		return r.Err()
	}

	var err error
	errList := []error{}
	nodeCount := 0
	ignoreHeaders := false
	err = r.Visit(func(info *resource.Info) error {
		nodeCount++
		node, ok := info.Object.(*kapi.Node)
		if !ok {
			err = fmt.Errorf("cannot convert input to Node: ", reflect.TypeOf(info.Object))
			errList = append(errList, err)
			return nil
		}

		switch cfg.Operations[0] {
		case "schedulable":
			err = cfg.RunSchedulable(node, ignoreHeaders)
			ignoreHeaders = true
		case "evacuate":
			if cfg.DryRun {
				err = cfg.RunListPods(node)
			} else if cfg.Evacuate {
				err = cfg.RunEvacuate(node)
			}
		case "list-pods":
			if cfg.ListPods {
				err = cfg.RunListPods(node)
			}
		}

		if err != nil {
			errList = append(errList, err)
			return nil
		}
		return nil
	})
	if len(errList) != 0 {
		return fmt.Errorf("%v", errList)
	}
	if nodeCount == 0 {
		if len(cfg.NodeNames) > 0 {
			return fmt.Errorf("Nodes %v not found", strings.Join(cfg.NodeNames, ","))
		} else {
			return fmt.Errorf("No nodes found")
		}
	}
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

	selector, err := labels.Parse(cfg.PodSelector.Value())
	if err != nil {
		return err
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
	// grace = 0 implies delete the pod immediately
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
			// For pods without replication controller, pod.Spec.Host set by user will not be honored.
			// more details: https://github.com/GoogleCloudPlatform/kubernetes/issues/8007
			pod.Spec.Host = ""
			pod.ObjectMeta.ResourceVersion = ""
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
		return fmt.Errorf("%v", errList)
	}
	return nil
}

func (cfg *NodeConfig) RunListPods(node *kapi.Node) error {
	selector, err := labels.Parse(cfg.PodSelector.Value())
	if err != nil {
		return err
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

	if len(resource) == 0 {
		version, kind, err := kapi.Scheme.ObjectVersionAndKind(obj)
		if err != nil {
			return nil, nil, err
		}
		mapping, err = cfg.Mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, nil, err
		}
	} else {
		version, kind, err := cfg.Mapper.VersionAndKindForResource(resource)
		mapping, err = cfg.Mapper.RESTMapping(kind, version)
		if err != nil {
			return nil, nil, err
		}
	}

	printerWithHeaders, err := cfg.Printer(mapping, false)
	if err != nil {
		return nil, nil, err
	}
	printerNoHeaders, err := cfg.Printer(mapping, true)
	if err != nil {
		return nil, nil, err
	}
	return printerWithHeaders, printerNoHeaders, nil
}
