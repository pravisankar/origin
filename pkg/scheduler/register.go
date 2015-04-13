package scheduler

import (
	kscheduler "github.com/GoogleCloudPlatform/kubernetes/pkg/scheduler"
	"github.com/GoogleCloudPlatform/kubernetes/plugin/pkg/scheduler/factory"
)

func init() {
	factory.RegisterFitPredicateFactory(
		"MatchProjectNodeSelector",
		func(args factory.PluginFactoryArgs) kscheduler.FitPredicate {
			return NewProjectNodeSelectorMatchPredicate(args.NodeInfo)
		},
	)
}
