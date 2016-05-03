package netnamespace

/*
Only needed for backward compatibility with older clients (openshift <= 3.2 release)
*/

import (
	"fmt"
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	kerrs "k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/api/rest"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/validation/field"

	"github.com/openshift/openshift-sdn/pkg/netid"
	"github.com/openshift/origin/pkg/sdn/api"
)

// REST implements the RESTStorage interface backed by namespace and it only supports the adding or deleting VNID annotation on the namespace.
type REST struct {
	client kclient.NamespaceInterface
}

// NewREST returns a new REST.
func NewREST(client kclient.NamespaceInterface) *REST {
	return &REST{client: client}
}

// New returns a new NetNamespace
func (r *REST) New() runtime.Object {
	return &api.NetNamespace{}
}

// NewList returns a new NetNamespaceList
func (*REST) NewList() runtime.Object {
	return &api.NetNamespaceList{}
}

var _ = rest.Getter(&REST{})

func (s *REST) Get(ctx kapi.Context, name string) (runtime.Object, error) {
	return s.getNetNamespace(name)
}

var _ = rest.Creater(&REST{})

func (s *REST) Create(ctx kapi.Context, obj runtime.Object) (runtime.Object, error) {
	netns, ok := obj.(*api.NetNamespace)
	if !ok {
		return nil, kerrs.NewBadRequest("invalid type")
	}
	Strategy.PrepareForCreate(netns)
	return s.update(ctx, netns)
}

var _ = rest.Updater(&REST{})

func (s *REST) Update(ctx kapi.Context, obj runtime.Object) (runtime.Object, bool, error) {
	netns, ok := obj.(*api.NetNamespace)
	if !ok {
		return nil, false, kerrs.NewBadRequest("invalid type")
	}
	Strategy.PrepareForUpdate(netns, nil)
	updatedNetNs, err := s.update(ctx, netns)
	return updatedNetNs, false, err
}

var _ = rest.Lister(&REST{})

func (s *REST) List(ctx kapi.Context, options *kapi.ListOptions) (runtime.Object, error) {
	return s.getAllNetNamespaces(options)
}

func (s *REST) update(ctx kapi.Context, netns *api.NetNamespace) (runtime.Object, error) {
	oldNetNs, err := s.getNetNamespace(netns.NetName)
	if err != nil {
		return nil, err
	}
	// Pre-update checks
	if err := rest.BeforeUpdate(Strategy, ctx, netns, oldNetNs); err != nil {
		return nil, err
	}

	// Get corresponding namespace and update the VNID intent
	ns, err := s.client.Get(netns.NetName)
	if err != nil {
		return nil, err
	}
	netid.SetWantsVNID(ns, netns.NetID)

	_, err = s.client.Update(ns)
	if err != nil {
		return nil, err
	}
	return s.validateNetNamespace(netns.NetName, netns.NetID)
}

func (s *REST) getAllNetNamespaces(options *kapi.ListOptions) (*api.NetNamespaceList, error) {
	nsList, err := s.client.List(*options)
	if err != nil {
		return nil, err
	}

	netnsList := &api.NetNamespaceList{}
	for _, ns := range nsList.Items {
		netns, err := netNamespaceForNamespace(&ns)
		if err == nil {
			netnsList.Items = append(netnsList.Items, *netns)
		}
	}
	return netnsList, nil
}

func (s *REST) getNetNamespace(name string) (*api.NetNamespace, error) {
	// Get corresponding namespace
	ns, err := s.client.Get(name)
	if kerrs.IsNotFound(err) {
		errs := field.ErrorList{field.Invalid(field.NewPath("NetName"), name, "referenced namespace does not exist")}
		return nil, kerrs.NewInvalid(api.Kind("NetNamespace"), name, errs)
	}
	if err != nil {
		return nil, err
	}
	return netNamespaceForNamespace(ns)
}

func (s *REST) validateNetNamespace(name string, id uint) (*api.NetNamespace, error) {
	// Timeout: 10 secs
	retries := 20
	retryInterval := 500 * time.Millisecond

	for i := 0; i < retries; i++ {
		ns, err := s.client.Get(name)
		if err != nil {
			return nil, err
		}
		curID, er := netid.GetVNID(ns)
		if (er == nil) && (curID == id) {
			return netNamespaceForNamespace(ns)
		}
		time.Sleep(retryInterval)
	}
	return nil, fmt.Errorf("Failed to validate network ID %d for project %q", id, name)
}

func netNamespaceForNamespace(ns *kapi.Namespace) (*api.NetNamespace, error) {
	id, err := netid.GetVNID(ns)
	if err != nil {
		return nil, err
	}
	return &api.NetNamespace{
		ObjectMeta: kapi.ObjectMeta{
			Name:            ns.Name,
			ResourceVersion: ns.ResourceVersion,
			UID:             ns.UID,
		},
		NetName: ns.Name,
		NetID:   id,
	}, nil
}
