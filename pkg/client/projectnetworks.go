package client

import (
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"

	projectapi "github.com/openshift/origin/pkg/project/api"
)

// ProjectNetworksInterface has methods to work with ProjectNetwork resources in a namespace
type ProjectNetworksInterface interface {
	ProjectNetworks() ProjectNetworkInterface
}

// ProjectNetworkInterface exposes methods on projectRequest resources.
type ProjectNetworkInterface interface {
	Update(p *projectapi.ProjectNetwork) (*projectapi.ProjectNetwork, error)
	Get(name string) (*projectapi.ProjectNetwork, error)
	List(label labels.Selector, field fields.Selector) (*unversioned.Status, error)
}

type projectNetworks struct {
	r *Client
}

func newProjectNetworks(c *Client) *projectNetworks {
	return &projectNetworks{
		r: c,
	}
}

// Create creates a new Project
func (c *projectNetworks) Create(p *projectapi.ProjectNetwork) (result *projectapi.ProjectNetwork, err error) {
	result = &projectapi.Project{}
	err = c.r.Post().Resource("projectNetworks").Body(p).Do().Into(result)
	return
}

// Get returns information about a particular project network and error if one occurs.
func (c *projectNetworks) Get(name string) (result *projectapi.ProjectNetwork, err error) {
	result = &projectapi.ProjectNetwork{}
	err = c.r.Get().Namespace(c.ns).Resource("projectNetworks").Name(name).Do().Into(result)
	return
}

// List returns a status object indicating that a user can call the Create or an error indicating why not
func (c *projectNetworks) List(label labels.Selector, field fields.Selector) (result *unversioned.Status, err error) {
	result = &unversioned.Status{}
	err = c.r.Get().Resource("projectNetworks").LabelsSelectorParam(label).FieldsSelectorParam(field).Do().Into(result)
	return result, err
}
