package testclient

import (
	"k8s.io/kubernetes/pkg/api/unversioned"
	ktestclient "k8s.io/kubernetes/pkg/client/unversioned/testclient"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"

	projectapi "github.com/openshift/origin/pkg/project/api"
)

// FakeProjectNetworks implements ProjectInterface. Meant to be embedded into a struct to get a default
// implementation. This makes faking out just the methods you want to test easier.
type FakeProjectNetworks struct {
	Fake *Fake
}

func (c *FakeProjectNetworks) Update(inObj *projectapi.ProjectNetwork) (*projectapi.ProjectNetwork, error) {
	obj, err := c.Fake.Invokes(ktestclient.NewRootCreateAction("projectNetworks", inObj), &projectapi.Project{})
	if obj == nil {
		return nil, err
	}

	return obj.(*projectapi.ProjectNetwork), err
}

func (c *FakeProjectNetworks) Get(name string) (*projectapi.ProjectNetwork, error) {
	obj, err := c.Fake.Invokes(ktestclient.NewGetAction("projectNetworks", c.Namespace, name), &projectapi.ProjectNetwork{})
	if obj == nil {
		return nil, err
	}

	return obj.(*projectapi.ProjectNetwork), err
}

func (c *FakeProjectNetworks) List(label labels.Selector, field fields.Selector) (*unversioned.Status, error) {
	obj, err := c.Fake.Invokes(ktestclient.NewRootListAction("projectNetworks", label, field), &unversioned.Status{})
	if obj == nil {
		return nil, err
	}

	return obj.(*unversioned.Status), err
}
