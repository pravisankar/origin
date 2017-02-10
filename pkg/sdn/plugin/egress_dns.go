package plugin

import (
	"net"

	"github.com/golang/glog"

	osapi "github.com/openshift/origin/pkg/sdn/api"

	ktypes "k8s.io/kubernetes/pkg/types"
)

type EgressDNS struct {
	// Holds Egress DNS entries for each policy
	pdMap map[ktypes.UID]*DNS
}

func (e *EgressDNS) Add(policy osapi.EgressNetworkPolicy) {
	dnsInfo := &DNS{}

	for _, rule := range policy.Spec.Egress {
		if len(rule.To.DNSName) > 0 {
			if err := dnsInfo.Add(rule.To.DNSName); err != nil {
				glog.Error(err)
			}
		}
	}

	if dnsInfo.Size() > 0 {
		if len(e.pdMap) == 0 {
			e.pdMap = map[ktypes.UID]*DNS{}
		}
		e.pdMap[policy.UID] = dnsInfo
	}
}

func (e *EgressDNS) Delete(policy osapi.EgressNetworkPolicy) {
	if _, ok := e.pdMap[policy.UID]; ok {
		delete(e.pdMap, policy.UID)
	}
}

func (e *EgressDNS) GetIPs(policy osapi.EgressNetworkPolicy, dnsName string) []net.IP {
	dnsInfo, ok := e.pdMap[policy.UID]
	if !ok {
		return []net.IP{}
	}
	return dnsInfo.Get(dnsName)
}

func (e *EgressDNS) GetNetCIDRs(policy osapi.EgressNetworkPolicy, dnsName string) []net.IPNet {
	cidrs := []net.IPNet{}
	for _, ip := range e.GetIPs(policy, dnsName) {
		if ip.To4() != nil { // IPv4 addr
			cidrs = append(cidrs, net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)})
		} else if ip.To16() != nil { // IPv6 addr
			// Currently we only handle IPv4 addrs
			glog.Warningf("ignoring IPv6 addr: %s (domain: %s) for policy: %s", ip.String(), dnsName, policy.Name)
		} else {
			glog.Errorf("invalid IP: %v for policy: %s", ip, policy.Name)
		}
	}
	return cidrs
}
