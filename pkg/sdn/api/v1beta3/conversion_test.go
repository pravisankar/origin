package v1beta3

import (
	"testing"

	kapi "k8s.io/kubernetes/pkg/api"

	"github.com/openshift/origin/pkg/sdn/api"
)

func TestNetNamespaceConversions(t *testing.T) {
	scheme := kapi.Scheme.Raw()

	// NetID not assigned
	var netns api.NetNamespace
	versionedObj, err := scheme.ConvertToVersion(&netns, "v1beta3")
	if err != nil {
		t.Fatalf("Conversion error: %v", err)
	}
	obj, err := scheme.ConvertToVersion(versionedObj, scheme.InternalVersion)
	if err != nil {
		t.Fatalf("Conversion error: %v", err)
	}
	result := obj.(*api.NetNamespace)
	if !kapi.Semantic.DeepDerivative(netns, *result) {
		t.Fatalf("Incorrect conversion: expected %v, got %v", netns, *result)
	}

	// NetID assigned
	netns.NetID = new(uint)
	*netns.NetID = 100
	versionedObj, err = scheme.ConvertToVersion(&netns, "v1beta3")
	if err != nil {
		t.Fatalf("Conversion error: %v", err)
	}
	obj, err = scheme.ConvertToVersion(versionedObj, scheme.InternalVersion)
	if err != nil {
		t.Fatalf("Conversion error: %v", err)
	}
	result = obj.(*api.NetNamespace)
	if *netns.NetID != *result.NetID {
		t.Fatalf("Incorrect conversion: expected %v, got %v", netns, *result)
	}
}
