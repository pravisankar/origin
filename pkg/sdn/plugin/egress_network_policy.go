package plugin

import (
	"fmt"
	"time"

	"github.com/golang/glog"

	osapi "github.com/openshift/origin/pkg/sdn/api"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	utilwait "k8s.io/kubernetes/pkg/util/wait"
)

func (plugin *OsdnNode) SetupEgressNetworkPolicy() error {
	policies, err := plugin.osClient.EgressNetworkPolicies(kapi.NamespaceAll).List(kapi.ListOptions{})
	if err != nil {
		return fmt.Errorf("could not get EgressNetworkPolicies: %s", err)
	}

	for _, policy := range policies.Items {
		vnid, err := plugin.policy.GetVNID(policy.Namespace)
		if err != nil {
			glog.Warningf("Could not find netid for namespace %q: %v", policy.Namespace, err)
			continue
		}
		plugin.egressPolicies[vnid] = append(plugin.egressPolicies[vnid], policy)

		plugin.egressDNS.Add(policy)
	}

	for vnid := range plugin.egressPolicies {
		plugin.updateEgressNetworkPolicyRules(vnid)
	}

	go utilwait.Forever(plugin.syncEgressDNSPolicyRules, 0)
	go utilwait.Forever(plugin.watchEgressNetworkPolicies, 0)
	return nil
}

func (plugin *OsdnNode) watchEgressNetworkPolicies() {
	RunEventQueue(plugin.osClient, EgressNetworkPolicies, func(delta cache.Delta) error {
		policy := delta.Object.(*osapi.EgressNetworkPolicy)

		vnid, err := plugin.policy.GetVNID(policy.Namespace)
		if err != nil {
			return fmt.Errorf("could not find netid for namespace %q: %v", policy.Namespace, err)
		}

		plugin.egressPoliciesLock.Lock()
		defer plugin.egressPoliciesLock.Unlock()

		policies := plugin.egressPolicies[vnid]
		for i, oldPolicy := range policies {
			if oldPolicy.UID == policy.UID {
				policies = append(policies[:i], policies[i+1:]...)
				break
			}
		}
		plugin.egressDNS.Delete(*policy)

		if delta.Type != cache.Deleted && len(policy.Spec.Egress) > 0 {
			policies = append(policies, *policy)
			plugin.egressDNS.Add(*policy)
		}
		plugin.egressPolicies[vnid] = policies

		plugin.updateEgressNetworkPolicyRules(vnid)
		return nil
	})
}

func (plugin *OsdnNode) UpdateEgressNetworkPolicyVNID(namespace string, oldVnid, newVnid uint32) {
	var policy *osapi.EgressNetworkPolicy

	plugin.egressPoliciesLock.Lock()
	defer plugin.egressPoliciesLock.Unlock()

	policies := plugin.egressPolicies[oldVnid]
	for i, oldPolicy := range policies {
		if oldPolicy.Namespace == namespace {
			policy = &oldPolicy
			plugin.egressPolicies[oldVnid] = append(policies[:i], policies[i+1:]...)
			plugin.updateEgressNetworkPolicyRules(oldVnid)
			break
		}
	}

	if policy != nil {
		plugin.egressPolicies[newVnid] = append(plugin.egressPolicies[newVnid], *policy)
		plugin.updateEgressNetworkPolicyRules(newVnid)
	}
}

func (plugin *OsdnNode) syncEgressDNSPolicyRules() {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()

	for {
		<-t.C
		glog.V(5).Infof("periodic egress dns sync: update policy rules")

		policies, err := plugin.osClient.EgressNetworkPolicies(kapi.NamespaceAll).List(kapi.ListOptions{})
		if err != nil {
			glog.Errorf("could not get EgressNetworkPolicies: %v", err)
			continue
		}

		for _, policy := range policies.Items {
			dnsInfo, ok := plugin.egressDNS.pdMap[policy.UID]
			if !ok {
				continue
			}

			changed, err := dnsInfo.Update()
			if err != nil {
				glog.Error(err)
				continue
			}

			if changed {
				vnid, err := plugin.policy.GetVNID(policy.Namespace)
				if err != nil {
					glog.Warningf("could not find netid for namespace %q: %v", policy.Namespace, err)
					break
				}

				plugin.egressPoliciesLock.Lock()
				defer plugin.egressPoliciesLock.Unlock()
				plugin.updateEgressNetworkPolicyRules(vnid)
			}
		}
	}
}
