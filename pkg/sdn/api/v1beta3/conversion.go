package v1beta3

import (
	kapi "k8s.io/kubernetes/pkg/api"
	conversion "k8s.io/kubernetes/pkg/conversion"

	newer "github.com/openshift/origin/pkg/sdn/api"
)

const (
	KnownInvalidVNID = uint((1 << 32) - 1)
)

func init() {
	err := kapi.Scheme.AddConversionFuncs(
		convert_v1beta3_NetNamespace_To_api_NetNamespace,
		convert_api_NetNamespace_To_v1beta3_NetNamespace,
	)
	if err != nil {
		panic(err)
	}
}

func convert_v1beta3_NetNamespace_To_api_NetNamespace(in *NetNamespace, out *newer.NetNamespace, s conversion.Scope) error {
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

func convert_api_NetNamespace_To_v1beta3_NetNamespace(in *newer.NetNamespace, out *NetNamespace, s conversion.Scope) error {
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
