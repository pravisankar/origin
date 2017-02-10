package plugin

import (
	"fmt"
	"net"
	"sync"

	kerrors "k8s.io/kubernetes/pkg/util/errors"
)

type DNS struct {
	lock     sync.Mutex
	dnsIPMap map[string][]net.IP
}

func (d *DNS) Size() int {
	d.lock.Lock()
	defer d.lock.Unlock()

	return len(d.dnsIPMap)
}

func (d *DNS) Get(dns string) []net.IP {
	d.lock.Lock()
	defer d.lock.Unlock()

	var data []net.IP
	if ips, ok := d.dnsIPMap[dns]; ok {
		data = make([]net.IP, len(ips))
		copy(data, ips)
	}
	return data
}

func (d *DNS) Add(dns string) error {
	d.lock.Lock()
	defer d.lock.Unlock()

	return d.updateIPs(dns)
}

func (d *DNS) Update() (bool, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	changed := false
	errs := []error{}
	if len(d.dnsIPMap) == 0 {
		return changed, kerrors.NewAggregate(errs)
	}

	for dns, oldips := range d.dnsIPMap {
		if err := d.updateIPs(dns); err != nil {
			errs = append(errs, err)
			continue
		}
		if !equal(oldips, d.dnsIPMap[dns]) {
			changed = true
		}
	}
	return changed, kerrors.NewAggregate(errs)
}

func (d *DNS) updateIPs(dns string) error {
	ips, err := net.LookupIP(dns)
	if err != nil {
		return fmt.Errorf("failed to lookup IP for domain %q: %v", dns, err)
	}

	if len(d.dnsIPMap) == 0 {
		d.dnsIPMap = map[string][]net.IP{}
	}
	for _, ip := range ips {
		if ip.To4() != nil { // IPv4 addr
			// Currently we only handle IPv4 addrs
			d.dnsIPMap[dns] = append(d.dnsIPMap[dns], ip)
		}
	}
	return nil
}

func equal(oldips, newips []net.IP) bool {
	if len(oldips) != len(newips) {
		return false
	}

	for _, oldip := range oldips {
		found := false
		for _, newip := range newips {
			if oldip.Equal(newip) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
