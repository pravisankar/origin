package scheduler

import (
	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	kscheduler "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"

	"github.com/openshift/origin/pkg/project/cache"
)

func NewProjectNodeSelectorMatchPredicate(info kscheduler.NodeInfo) kscheduler.FitPredicate {
	selector := projectNodeSelector{
		info: info,
	}
	return selector.ProjectSelectorMatches
}

type projectNodeSelector struct {
	info kscheduler.NodeInfo
}

func (p *projectNodeSelector) ProjectSelectorMatches(pod kapi.Pod, existingPods []kapi.Pod, node string) (bool, error) {
	minion, err := p.info.GetNodeInfo(node)
	if err != nil {
		return false, err
	}
	return p.projectMatchesNodeLabels(&pod, minion)
}

func (p *projectNodeSelector) projectMatchesNodeLabels(pod *kapi.Pod, node *kapi.Node) (bool, error) {
	projects, err := cache.GetProjectCache()
	if err != nil {
		return false, err
	}
	namespace, err := projects.GetNamespaceObject(pod.ObjectMeta.Namespace)
	if err != nil {
		return false, err
	}
	selector, err := projects.GetNodeSelector(namespace)
	if err != nil {
		return false, err
	}
	return selector.Matches(labels.Set(node.Labels)), nil
}
