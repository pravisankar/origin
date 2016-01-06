package netnamespace

import (
	"fmt"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/registry/generic"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/fielderrors"

	"github.com/openshift/origin/pkg/sdn/api"
	"github.com/openshift/origin/pkg/sdn/api/validation"
)

// strategy implements behavior for ProjectNetworks
type strategy struct {
	runtime.ObjectTyper
}

// Strategy is the default logic that applies when updating ProjectNetwork
// objects via the REST API.
var Strategy = strategy{kapi.Scheme}

func (strategy) PrepareForUpdate(obj, old runtime.Object) {}

// NamespaceScoped is true for project network
func (strategy) NamespaceScoped() bool {
	return true
}

func (strategy) GenerateName(base string) string {
	return base
}

func (strategy) PrepareForCreate(obj runtime.Object) {
}

// Validate validates a new ProjectNetwork
func (strategy) Validate(ctx kapi.Context, obj runtime.Object) fielderrors.ValidationErrorList {
	return validation.ValidateProjectNetwork(obj.(*api.NetNamespace))
}

// AllowCreateOnUpdate is false for ProjectNetwork
func (strategy) AllowCreateOnUpdate() bool {
	return false
}

func (strategy) AllowUnconditionalUpdate() bool {
	return false
}

// ValidateUpdate is the default update validation for a ProjectNetwork
func (strategy) ValidateUpdate(ctx kapi.Context, obj, old runtime.Object) fielderrors.ValidationErrorList {
	return validation.ValidateProjectNetworkUpdate(obj.(*api.ProjectNetwork), old.(*api.ProjectNetwork))
}

// Matcher returns a generic matcher for a given label and field selector.
func Matcher(label labels.Selector, field fields.Selector) generic.Matcher {
	return generic.MatcherFunc(func(obj runtime.Object) (bool, error) {
		ns, ok := obj.(*api.ProjectNetwork)
		if !ok {
			return false, fmt.Errorf("not a project network")
		}
		return label.Matches(labels.Set(ns.Labels)) && field.Matches(api.ProjectNetworkToSelectableFields(ns)), nil
	})
}
