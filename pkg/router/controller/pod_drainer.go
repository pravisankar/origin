package controller

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/controller/endpoint"

	"github.com/golang/glog"
	routeapi "github.com/openshift/origin/pkg/route/apis/route"
	"github.com/openshift/origin/pkg/router"
)

// PodDrainer implements the router.Plugin interface to provide
// extended pod draining for template based, backend-agnostic routers.
// Needs:
// - "service.alpha.kubernetes.io/tolerate-unready-endpoints": "true"
type PodDrainer struct {
	// plugin is the next plugin in the chain.
	plugin    router.Plugin
	lookupSvc ServiceLookup

	draining map[string]types.UID
}

// ServiceLookup is an interface for fetching the service associated
// with the given endpoints.  It is duplicated here to avoid import
// loops, the implementation is in
// pkg/router/template/service_lookup.go
type ServiceLookup interface {
	LookupService(*kapi.Endpoints) (*kapi.Service, error)
}

// NewPodDrainer creates a plugin wrapper that ensures only routes that
// pass extended validation are relayed to the next plugin in the chain.
// Recorder is an interface for indicating why a route was rejected.
func NewPodDrainer(plugin router.Plugin, lookupSvc ServiceLookup) *PodDrainer {
	return &PodDrainer{
		plugin:    plugin,
		lookupSvc: lookupSvc,
		draining:  make(map[string]types.UID),
	}
}

// HandleNode processes watch events on the node resource
func (p *PodDrainer) HandleNode(eventType watch.EventType, node *kapi.Node) error {
	return p.plugin.HandleNode(eventType, node)
}

// HandlePod processes watch events on the pod resource
func (p *PodDrainer) HandlePod(eventType watch.EventType, pod *kapi.Pod) error {
	if pod.DeletionTimestamp != nil && pod.Status.Phase == "Running" /* && pod.Status *types.PodRunning*/ {
		p.draining[pod.Status.PodIP] = pod.UID
	} else {
		delete(p.draining, pod.Status.PodIP)
	}
	glog.Infof("%v: Pod state: %v DeletionTimestamp=%v phase=%v", eventType, pod.Name, pod.DeletionTimestamp, pod.Status.Phase)

	// TODO: Do we need to fake a new HandleEndpoints event for each
	// endpoint this pod is a part of?  In practice, it seems we get a
	// real HandleEndpoints event right after the pod event, but that
	// *might* be filtered out in a later version

	return p.plugin.HandlePod(eventType, pod)
}

// HandleEndpoints processes watch events on the Endpoints resource.
// For services annotated with TolerateUnreadyEndpointsAnnotation, we
// move all endpoints belonging to Terminating pods into
// NotReadyAddresses
func (p *PodDrainer) HandleEndpoints(eventType watch.EventType, endpoints *kapi.Endpoints) error {
	service, err := p.lookupSvc.LookupService(endpoints)
	if err != nil {
		glog.Errorf("Failed to lookup service: endpoints %v: %v", endpoints.Name, err)
	} else if service.Annotations[endpoint.TolerateUnreadyEndpointsAnnotation] == "true" {
		glog.Infof("Has TolerateUnready: %v", endpoints.Name)
		// TODO: Are we allowed to modify this list in place?

		for i := range endpoints.Subsets {
			s := &endpoints.Subsets[i]
			s.NotReadyAddresses = nil

			// Filter Adresses in-place, moving any ready and draining adresses
			outidx := 0
			for _, addr := range s.Addresses {
				if uid, ok := p.draining[addr.IP]; ok {
					glog.Infof("Moving IP to draining: %v (%v %v)", addr.IP, uid, addr.TargetRef.UID)
					s.NotReadyAddresses = append(s.NotReadyAddresses, addr)
				} else {
					s.Addresses[outidx] = addr
					outidx++
				}
			}
			s.Addresses = s.Addresses[:outidx]
		}
	}

	return p.plugin.HandleEndpoints(eventType, endpoints)
}

// HandleRoute processes watch events on the Route resource.
func (p *PodDrainer) HandleRoute(eventType watch.EventType, route *routeapi.Route) error {
	return p.plugin.HandleRoute(eventType, route)
}

// HandleNamespaces limits the scope of valid routes to only those that match
// the provided namespace list.
func (p *PodDrainer) HandleNamespaces(namespaces sets.String) error {
	return p.plugin.HandleNamespaces(namespaces)
}

func (p *PodDrainer) Commit() error {
	return p.plugin.Commit()
}
