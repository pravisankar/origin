package api

import (
	"k8s.io/kubernetes/pkg/api"
)

func init() {
	api.Scheme.AddKnownTypes("",
		&ClusterNetwork{},
		&ClusterNetworkList{},
		&HostSubnet{},
		&HostSubnetList{},
		&NetNamespace{},
		&NetNamespaceList{},
		&ProjectNetwork{},
		&ProjectNetworkList{},
	)
}

func (*ClusterNetwork) IsAnAPIObject()     {}
func (*ClusterNetworkList) IsAnAPIObject() {}
func (*HostSubnet) IsAnAPIObject()         {}
func (*HostSubnetList) IsAnAPIObject()     {}
func (*NetNamespace) IsAnAPIObject()       {}
func (*NetNamespaceList) IsAnAPIObject()   {}
func (*ProjectNetwork) IsAnAPIObject()     {}
func (*ProjectNetworkList) IsAnAPIObject() {}
