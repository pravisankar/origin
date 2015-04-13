package admission

import (
	"fmt"
	"io"

	"github.com/GoogleCloudPlatform/kubernetes/pkg/admission"
	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	apierrors "github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api/meta"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"

	projectcache "github.com/openshift/origin/pkg/project/cache"
)

func init() {
	admission.RegisterPlugin("OriginPodNodeEnvironment", func(client client.Interface, config io.Reader) (admission.Interface, error) {
		return NewPodNodeEnvironment(client)
	})
}

// podNodeEnvironment is an implementation of admission.Interface.
// It validates node selector for the pod.
type podNodeEnvironment struct {
	nodeLister *cache.StoreToNodeLister
}

func (p *podNodeEnvironment) Admit(a admission.Attributes) (err error) {
	if a.GetOperation() == "DELETE" {
		return nil
	}

	resource := a.GetResource()
	if resource != "pods" {
		return nil
	}

	obj := a.GetObject()
	name := "Unknown"
	if obj != nil {
		name, _ = meta.NewAccessor().Name(obj)
	}
	pod := obj.(*kapi.Pod)
	if len(pod.Spec.NodeSelector) == 0 {
		return nil
	}
	podNodeSelector := labels.SelectorFromSet(pod.Spec.NodeSelector)

	projects, err := projectcache.GetProjectCache()
	if err != nil {
		return err
	}
	namespace, err := projects.GetNamespaceObject(a.GetNamespace())
	if err != nil {
		return apierrors.NewForbidden(resource, name, err)
	}
	projectNodeSelector, err := projects.GetNodeSelector(namespace)
	if err != nil {
		return apierrors.NewForbidden(resource, name, err)
	}

	nodes, err := p.nodeLister.List()
	if err != nil {
		return apierrors.NewForbidden(resource, name, fmt.Errorf("Unable to list nodes in the cluster, error: %s", err))
	}

	conflict := false
	for _, node := range nodes.Items {
		nodeLabelSet := labels.Set(node.Labels)
		podSelectorMatches := podNodeSelector.Matches(nodeLabelSet)
		projectSelectorMatches := projectNodeSelector.Matches(nodeLabelSet)
		if podSelectorMatches && projectSelectorMatches {
			return nil
		}
		if !conflict && podSelectorMatches && !projectSelectorMatches {
			conflict = true
		}
	}
	if conflict {
		return apierrors.NewForbidden(resource, name, fmt.Errorf("Pod node selector conflicts with its project node selector."))
	} else {
		return apierrors.NewForbidden(resource, name, fmt.Errorf("Pod node selector doesn't match any nodes in the cluster."))
	}
}

func NewPodNodeEnvironment(c client.Interface) (admission.Interface, error) {
	nodeLister := &cache.StoreToNodeLister{cache.NewStore(cache.MetaNamespaceKeyFunc)}
	reflector := cache.NewReflector(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return c.Nodes().List(labels.Everything())
			},
			WatchFunc: func(resourceVersion string) (watch.Interface, error) {
				return c.Nodes().Watch(labels.Everything(), fields.Everything(), resourceVersion)
			},
		},
		&kapi.Node{},
		nodeLister.Store,
		0,
	)
	reflector.Run()
	return &podNodeEnvironment{
		nodeLister: nodeLister,
	}, nil
}
