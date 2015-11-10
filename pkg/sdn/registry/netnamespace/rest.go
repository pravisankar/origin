package netnamespace

import (
	"github.com/golang/glog"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/api/rest"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/fielderrors"
	"k8s.io/kubernetes/pkg/watch"

	"github.com/openshift/origin/pkg/sdn/api"
	"github.com/openshift/origin/pkg/sdn/api/validation"
	"github.com/openshift/origin/pkg/sdn/registry/netnamespace/vnidallocator"
)

// REST adapts a netnamespace registry into apiserver's RESTStorage model.
type REST struct {
	registry Registry
	vnids    vnidallocator.Interface
}

// NewStorage returns a new REST.
func NewStorage(registry Registry, vnids vnidallocator.Interface) *REST {
	return &REST{
		registry: registry,
		vnids:    vnids,
	}
}

func (rs *REST) Create(ctx kapi.Context, obj runtime.Object) (runtime.Object, error) {
	netns := obj.(*api.NetNamespace)

	if err := rest.BeforeCreate(Strategy, ctx, obj); err != nil {
		return nil, err
	}

	err := rs.assignNetID(netns)
	if err != nil {
		return nil, err
	}

	out, err := rs.registry.CreateNetNamespace(ctx, netns)
	if err != nil {
		er := rs.revokeNetID(netns)
		if er != nil {
			// these should be caught by an eventual reconciliation / restart
			glog.Errorf("Error releasing netnamespace %s NetID %d: %v", netns.Name, netns.NetID, er)
		}
		er = rest.CheckGeneratedNameError(Strategy, err, netns)
		if er != nil {
			return nil, er
		}
	}

	return out, err
}

func (rs *REST) Delete(ctx kapi.Context, id string) (runtime.Object, error) {
	netns, err := rs.registry.GetNetNamespace(ctx, id)
	if err != nil {
		return nil, err
	}

	err = rs.registry.DeleteNetNamespace(ctx, id)
	if err != nil {
		return nil, err
	}

	err = rs.revokeNetID(netns)
	if err != nil {
		// these should be caught by an eventual reconciliation / restart
		glog.Errorf("Error releasing netnamespace %s NetID %d: %v", netns.Name, netns.NetID, err)
	}
	return &unversioned.Status{Status: unversioned.StatusSuccess}, nil
}

func (rs *REST) Get(ctx kapi.Context, id string) (runtime.Object, error) {
	return rs.registry.GetNetNamespace(ctx, id)
}

func (rs *REST) List(ctx kapi.Context, label labels.Selector, field fields.Selector) (runtime.Object, error) {
	return rs.registry.ListNetNamespaces(ctx, label, field)
}

// Watch returns NetNamespaces events via a watch.Interface.
// It implements rest.Watcher.
func (rs *REST) Watch(ctx kapi.Context, label labels.Selector, field fields.Selector, resourceVersion string) (watch.Interface, error) {
	return rs.registry.WatchNetNamespaces(ctx, label, field, resourceVersion)
}

func (*REST) New() runtime.Object {
	return &api.NetNamespace{}
}

func (*REST) NewList() runtime.Object {
	return &api.NetNamespaceList{}
}

func (rs *REST) Update(ctx kapi.Context, obj runtime.Object) (runtime.Object, bool, error) {
	netns := obj.(*api.NetNamespace)
	oldNetns, err := rs.registry.GetNetNamespace(ctx, netns.Name)
	if err != nil {
		return nil, false, err
	}

	if errs := validation.ValidateNetNamespaceUpdate(oldNetns, netns); len(errs) > 0 {
		return nil, false, errors.NewInvalid("netNamespace", netns.Name, errs)
	}

	err = rs.updateNetID(oldNetns, netns)
	if err != nil {
		return nil, false, err
	}

	out, err := rs.registry.UpdateNetNamespace(ctx, netns)
	if err != nil {
		er := rs.updateNetID(netns, oldNetns)
		if er != nil {
			// problems should be fixed by an eventual reconciliation / restart
			glog.Errorf("error(s) committing NetID changes: %v", er)
		}
	}
	return out, false, err
}

func (rs *REST) updateNetID(oldNetns, newNetns *api.NetNamespace) error {
	if oldNetns.NetID != newNetns.NetID {
		err := rs.revokeNetID(oldNetns)
		if err != nil {
			// these should be caught by an eventual reconciliation / restart
			glog.Errorf("Error releasing netnamespace %s NetID %d: %v", oldNetns.Name, oldNetns.NetID, err)
		}
		err = rs.assignNetID(newNetns)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rs *REST) assignNetID(netns *api.NetNamespace) error {
	if isNetIDRequested(netns) {
		// Allocate next available.
		vnid, err := rs.vnids.AllocateNext()
		if err != nil {
			el := fielderrors.ValidationErrorList{fielderrors.NewFieldInvalid("NetID", netns.NetID, err.Error())}
			return errors.NewInvalid("NetNamespace", netns.Name, el)
		}
		netns.NetID = uint(vnid)
	} else {
		// Try to respect the requested Net ID.
		if err := rs.vnids.Allocate(int(netns.NetID)); err != nil {
			el := fielderrors.ValidationErrorList{fielderrors.NewFieldInvalid("NetID", netns.NetID, err.Error())}
			return errors.NewInvalid("NetNamespace", netns.Name, el)
		}
	}
	return nil
}

func (rs *REST) revokeNetID(netns *api.NetNamespace) error {
	return rs.vnids.Release(int(netns.NetID))
}

func isNetIDRequested(netns *api.NetNamespace) bool {
	//TODO: fix me
	return (netns.NetID == 0)
}
