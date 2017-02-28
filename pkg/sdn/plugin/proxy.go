package plugin

import (
	"fmt"
	"net"
	"sync"

	"github.com/golang/glog"

	osclient "github.com/openshift/origin/pkg/client"
	osapi "github.com/openshift/origin/pkg/sdn/api"

	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	kclientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	pconfig "k8s.io/kubernetes/pkg/proxy/config"
	ktypes "k8s.io/kubernetes/pkg/types"
	utilwait "k8s.io/kubernetes/pkg/util/wait"
)

type firewallItem struct {
	ruleType osapi.EgressNetworkPolicyRuleType
	net      *net.IPNet
}

type policyFirewallItem struct {
	uid      ktypes.UID
	firewall []firewallItem
}

type proxyFirewallItem struct {
	namespaceFirewalls []policyFirewallItem
	activeFirewall     *policyFirewallItem
}

type OsdnProxy struct {
	kClient              *kclientset.Clientset
	osClient             *osclient.Client
	networkInfo          *NetworkInfo
	baseEndpointsHandler pconfig.EndpointsConfigHandler

	lock         sync.Mutex
	firewall     map[string]*proxyFirewallItem
	allEndpoints []kapi.Endpoints

	idLock sync.Mutex
	ids    map[string]uint32
}

// Called by higher layers to create the proxy plugin instance; only used by nodes
func NewProxyPlugin(pluginName string, osClient *osclient.Client, kClient *kclientset.Clientset) (*OsdnProxy, error) {
	if !osapi.IsOpenShiftMultitenantNetworkPlugin(pluginName) {
		return nil, nil
	}

	return &OsdnProxy{
		kClient:  kClient,
		osClient: osClient,
		ids:      make(map[string]uint32),
		firewall: make(map[string]*proxyFirewallItem),
	}, nil
}

func (proxy *OsdnProxy) Start(baseHandler pconfig.EndpointsConfigHandler) error {
	glog.Infof("Starting multitenant SDN proxy endpoint filter")

	var err error
	proxy.networkInfo, err = getNetworkInfo(proxy.osClient)
	if err != nil {
		return fmt.Errorf("could not get network info: %s", err)
	}
	proxy.baseEndpointsHandler = baseHandler

	policies, err := proxy.osClient.EgressNetworkPolicies(kapi.NamespaceAll).List(kapi.ListOptions{})
	if err != nil {
		return fmt.Errorf("Could not get EgressNetworkPolicies: %s", err)
	}
	for _, policy := range policies.Items {
		proxy.updateEgressNetworkPolicy(policy)
	}

	go utilwait.Forever(proxy.watchEgressNetworkPolicies, 0)
	go utilwait.Forever(proxy.watchNetNamespaces, 0)
	return nil
}

func (proxy *OsdnProxy) watchEgressNetworkPolicies() {
	RunEventQueue(proxy.osClient, EgressNetworkPolicies, func(delta cache.Delta) error {
		policy := delta.Object.(*osapi.EgressNetworkPolicy)
		if delta.Type == cache.Deleted {
			policy.Spec.Egress = nil
		}

		func() {
			proxy.lock.Lock()
			defer proxy.lock.Unlock()
			proxy.updateEgressNetworkPolicy(*policy)
			if proxy.allEndpoints != nil {
				proxy.updateEndpoints()
			}
		}()
		return nil
	})
}

func (proxy *OsdnProxy) watchNetNamespaces() {
	RunEventQueue(proxy.osClient, NetNamespaces, func(delta cache.Delta) error {
		netns := delta.Object.(*osapi.NetNamespace)
		name := netns.ObjectMeta.Name

		glog.V(5).Infof("Watch %s event for NetNamespace %q", delta.Type, name)
		proxy.idLock.Lock()
		defer proxy.idLock.Unlock()
		switch delta.Type {
		case cache.Sync, cache.Added, cache.Updated:
			proxy.ids[name] = netns.NetID
		case cache.Deleted:
			delete(proxy.ids, name)
		}
		return nil
	})
}
func (proxy *OsdnProxy) updateEgressNetworkPolicy(policy osapi.EgressNetworkPolicy) {
	ns := policy.Namespace
	ignorePolicy := false
	func() {
		proxy.idLock.Lock()
		defer proxy.idLock.Unlock()
		if proxy.ids[ns] == osapi.GlobalVNID {
			// Firewall not allowed for global namespaces
			glog.Errorf("EgressNetworkPolicy in global network namespace (%s) is not allowed (%s); ignoring firewall rules", ns, policy.Name)
			ignorePolicy = true
		}
	}()
	if ignorePolicy {
		return
	}

	firewall := make([]firewallItem, len(policy.Spec.Egress))
	for i, rule := range policy.Spec.Egress {
		_, cidr, err := net.ParseCIDR(rule.To.CIDRSelector)
		if err != nil {
			// should have been caught by validation
			glog.Errorf("Illegal CIDR value %q in EgressNetworkPolicy rule for policy: %v", rule.To.CIDRSelector, policy.UID)
			return
		}
		firewall[i] = firewallItem{rule.Type, cidr}
	}

	// Add/Update/Delete firwall rules for the namespace
	if len(firewall) > 0 {
		updated := false
		if ref, ok := proxy.firewall[ns]; ok {
			for i, item := range ref.namespaceFirewalls {
				if item.uid == policy.UID {
					proxy.firewall[ns].namespaceFirewalls[i].firewall = firewall
					updated = true
					break
				}
			}
		}
		if !updated {
			if _, ok := proxy.firewall[ns]; !ok {
				proxy.firewall[ns] = &proxyFirewallItem{}
			}
			policyFirewall := policyFirewallItem{uid: policy.UID, firewall: firewall}
			proxy.firewall[ns].namespaceFirewalls = append(proxy.firewall[ns].namespaceFirewalls, policyFirewall)
		}
	} else {
		if ref, ok := proxy.firewall[ns]; ok {
			for i, item := range ref.namespaceFirewalls {
				if item.uid == policy.UID {
					proxy.firewall[ns].namespaceFirewalls = append(proxy.firewall[ns].namespaceFirewalls[:i], proxy.firewall[ns].namespaceFirewalls[i+1:]...)
				}
			}
		}
	}

	// Set active firewall for the namespace
	if len(proxy.firewall[ns].namespaceFirewalls) == 1 {
		proxy.firewall[ns].activeFirewall = &proxy.firewall[ns].namespaceFirewalls[0]
	} else {
		// We only allow one policy per namespace otherwise it's hard to determine which policy to apply first
		glog.Errorf("found multiple egress policies, dropping all firewall rules for namespace: %s", ns)
		proxy.firewall[ns].activeFirewall = nil
	}
}

func (proxy *OsdnProxy) firewallBlocksIP(namespace string, ip net.IP) bool {
	if ref, ok := proxy.firewall[namespace]; ok {
		if ref.activeFirewall == nil {
			return false
		}

		for _, item := range ref.activeFirewall.firewall {
			if item.net.Contains(ip) {
				return item.ruleType == osapi.EgressNetworkPolicyRuleDeny
			}
		}
	}
	return false
}

func (proxy *OsdnProxy) OnEndpointsUpdate(allEndpoints []kapi.Endpoints) {
	proxy.lock.Lock()
	defer proxy.lock.Unlock()
	proxy.allEndpoints = allEndpoints
	proxy.updateEndpoints()
}

func (proxy *OsdnProxy) updateEndpoints() {
	if len(proxy.firewall) == 0 {
		proxy.baseEndpointsHandler.OnEndpointsUpdate(proxy.allEndpoints)
		return
	}

	filteredEndpoints := make([]kapi.Endpoints, 0, len(proxy.allEndpoints))

EndpointLoop:
	for _, ep := range proxy.allEndpoints {
		ns := ep.ObjectMeta.Namespace
		for _, ss := range ep.Subsets {
			for _, addr := range ss.Addresses {
				IP := net.ParseIP(addr.IP)
				if !proxy.networkInfo.ClusterNetwork.Contains(IP) && !proxy.networkInfo.ServiceNetwork.Contains(IP) {
					if proxy.firewallBlocksIP(ns, IP) {
						glog.Warningf("Service '%s' in namespace '%s' has an Endpoint pointing to firewalled destination (%s)", ep.ObjectMeta.Name, ns, addr.IP)
						continue EndpointLoop
					}
				}
			}
		}
		filteredEndpoints = append(filteredEndpoints, ep)
	}

	proxy.baseEndpointsHandler.OnEndpointsUpdate(filteredEndpoints)
}
