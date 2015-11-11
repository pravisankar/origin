package v1

import (
	kapi "k8s.io/kubernetes/pkg/api"
	conversion "k8s.io/kubernetes/pkg/conversion"

	oapi "github.com/openshift/origin/pkg/api"
	newer "github.com/openshift/origin/pkg/sdn/api"
)

const (
	KnownInvalidVNID = uint((1 << 32) - 1)
)

func init() {
	if err := kapi.Scheme.AddFieldLabelConversionFunc("v1", "ClusterNetwork",
		oapi.GetFieldLabelConversionFunc(newer.ClusterNetworkToSelectableFields(&newer.ClusterNetwork{}), nil),
	); err != nil {
		panic(err)
	}

	if err := kapi.Scheme.AddFieldLabelConversionFunc("v1", "HostSubnet",
		oapi.GetFieldLabelConversionFunc(newer.HostSubnetToSelectableFields(&newer.HostSubnet{}), nil),
	); err != nil {
		panic(err)
	}

	err := kapi.Scheme.AddConversionFuncs(
		convert_v1_NetNamespace_To_api_NetNamespace,
		convert_api_NetNamespace_To_v1_NetNamespace,
	)
	if err != nil {
		panic(err)
	}

	if err := kapi.Scheme.AddFieldLabelConversionFunc("v1", "NetNamespace",
		oapi.GetFieldLabelConversionFunc(newer.NetNamespaceToSelectableFields(&newer.NetNamespace{}), nil),
	); err != nil {
		panic(err)
	}
}

func convert_v1_NetNamespace_To_api_NetNamespace(in *NetNamespace, out *newer.NetNamespace, s conversion.Scope) error {
	if err := s.Convert(&in.TypeMeta, &out.TypeMeta, 0); err != nil {
		return err
	}
	if err := s.Convert(&in.ObjectMeta, &out.ObjectMeta, 0); err != nil {
		return err
	}

	out.NetName = in.NetName
	if in.NetID == KnownInvalidVNID {
		out.NetID = nil
	} else {
		out.NetID = new(uint)
		*out.NetID = in.NetID
	}
	return nil
}

func convert_api_NetNamespace_To_v1_NetNamespace(in *newer.NetNamespace, out *NetNamespace, s conversion.Scope) error {
	if err := s.Convert(&in.TypeMeta, &out.TypeMeta, 0); err != nil {
		return err
	}
	if err := s.Convert(&in.ObjectMeta, &out.ObjectMeta, 0); err != nil {
		return err
	}

	out.NetName = in.NetName
	if in.NetID != nil {
		out.NetID = *in.NetID
	} else {
		out.NetID = KnownInvalidVNID
	}
	return nil
}
