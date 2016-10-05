package util

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/openshift/origin/pkg/diagnostics/types"
)

type LogInterface struct {
	Result         types.DiagnosticResult
	ChRootDir      string
	JournalOptions string
}

func (l *LogInterface) LogNode(logdir string) {
	p := logdir

	l.LogSystem(p)
	l.LogServices(p)

	l.Run([]string{"brctl", "show"}, "/bridges", p)
	l.Run([]string{"docker", "ps", "-a"}, "/docker-ps", p)
	l.Run([]string{"ovs-ofctl", "-O", "OpenFlow13", "dump-flows", "br0"}, "/flows", p) // DON't hard code br0
	l.Run([]string{"ovs-ofctl", "-O", "OpenFlow13", "show", "br0"}, "/ovs-show", p)
	l.Run([]string{"tc", "qdisc", "show"}, "/tc-qdisc", p)
	l.Run([]string{"tc", "class", "show"}, "/tc-class", p)
	l.Run([]string{"tc", "filter", "show"}, "/tc-filter", p)
	l.Run([]string{"system", "cat", "docker.service"}, "/docker-unit-file", p)
	//TODO: grab docker network-file
	// echo_and_eval  cat "$(systemctl cat docker.service | grep EnvironmentFile.\*openshift-sdn | awk -F=- '{print $2}')" \
	//                                                           &> "${lognode}/docker-network-file"
	// TODO: grab each pod address and routes
}

func (l *LogInterface) LogMaster(logdir string) {
	p := logdir

	l.LogSystem(p)
	l.LogServices(p)

	l.Run([]string{"oc", "get", "nodes", "-o", "yaml"}, "/nodes", p)
	l.Run([]string{"oc", "get", "pods", "--all-namespaces", "-o", "yaml"}, "/pods", p)
	l.Run([]string{"oc", "get", "services", "--all-namespaces", "-o", "yaml"}, "/services", p)
	l.Run([]string{"oc", "get", "endpoints", "--all-namespaces", "-o", "yaml"}, "/endpoints", p)
	l.Run([]string{"oc", "get", "routes", "--all-namespaces", "-o", "yaml"}, "/routes", p)
	l.Run([]string{"oc", "get", "clusternetwork", "-o", "yaml"}, "/clusternetwork", p)
	l.Run([]string{"oc", "get", "hostsubnets", "-o", "yaml"}, "/hostsubnets", p)
	l.Run([]string{"oc", "get", "netnamespaces", "-o", "yaml"}, "/netnamespaces", p)
}

func (l *LogInterface) LogServices(logdir string) {
	p := logdir

	type Service struct {
		name string
		args string
	}
	allServices := []Service{
		{name: "master", args: "master"},
		{name: "master-controllers", args: "master controllers"},
		{name: "api", args: "master api"},
		{name: "node", args: "node"},
	}
	foundServices := []Service{}

	for _, sysDir := range []string{"/etc/systemd/system", "/usr/lib/systemd/system"} {
		for _, name := range []string{"openshift", "origin", "atomic-openshift"} {
			for _, service := range allServices {
				servicePath := fmt.Sprintf("%s/%s-%s.service", sysDir, name, service.name)
				if _, err := os.Stat(servicePath); err == nil {
					foundServices = append(foundServices, service)
				}
			}
		}
	}

	for _, service := range foundServices {
		l.Run([]string{"journalctl", "-u", service.name, l.JournalOptions}, fmt.Sprintf("/journal-%s", service.name), p)
		l.Run([]string{"systemctl", "show", service.name}, fmt.Sprintf("/systemctl-show-%s", service.name), p)

		// TODO(grab config)
		configFile := l.getConfigFileForService(service.name, service.args)
		if len(configFile) > 0 {
			l.Run([]string{"cat", configFile}, fmt.Sprintf("/config-%s", service.name), p)
		}
	}
}

func (l *LogInterface) LogSystem(logdir string) {
	p := logdir

	l.Run([]string{"journalctl", "--boot", l.JournalOptions}, "/journal-boot", p)
	l.Run([]string{"nmcli", "--nocheck", "-f", "all", "dev", "show"}, "/nmcli-dev", p)
	l.Run([]string{"nmcli", "--nocheck", "-f", "all", "con", "show"}, "/nmcli-con", p)
	l.Run([]string{"head", "-1000", "/etc/sysconfig/network-scripts/ifcfg-*"}, "/ifcfg", p)
	l.Run([]string{"ip", "addr", "show"}, "/addresses", p)
	l.Run([]string{"ip", "route", "show"}, "/routes", p)
	l.Run([]string{"ip", "neighbor", "show"}, "/arp", p)
	l.Run([]string{"iptables-save"}, "/iptables", p)
	l.Run([]string{"cat", "/etc/hosts"}, "/hosts", p)
	l.Run([]string{"cat", "/etc/resolv.conf"}, "/resolv.conf", p)
	l.Run([]string{"lsmod"}, "/modules", p)
	l.Run([]string{"sysctl -a"}, "/sysctl", p)
	l.Run([]string{"oc version"}, "/version", p)
	l.Run([]string{"docker version"}, "/version", p)
	l.Run([]string{"cat /etc/system-release-cpe"}, "/version", p)
}

func (l *LogInterface) Run(cmd []string, outfile, logdir string) {
	if len(cmd) == 0 {
		return
	}

	if _, err := os.Stat(logdir); err != nil {
		if err = os.MkdirAll(logdir, 0700); err != nil {
			l.Result.Error("DLogNet1001", err, fmt.Sprintf("Creating log directory %q failed: %s", logdir, err))
			return
		}
	}
	outPath := filepath.Join(logdir, outfile)
	out, err := os.OpenFile(outPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		l.Result.Error("DLogNet1002", err, fmt.Sprintf("Opening file %q failed: %s", outPath, err))
		return
	}
	defer out.Close()

	if len(l.ChRootDir) != 0 {
		cmd = append([]string{"chroot", l.ChRootDir}, cmd...)
	}
	first := cmd[0]
	rest := cmd[1:]
	c := exec.Command(first, rest...)
	c.Stdout = out
	c.Stderr = out
	if err = c.Run(); err != nil {
		l.Result.Error("DLogNet1003", err, fmt.Sprintf("CMD %q failed: %s", strings.Join(cmd, " "), err))
		return
	}
}

//FIXME
func (l *LogInterface) getConfigFileForService(serviceName, serviceArgs string) string {
	var err error
	l.Result.Error("DLogNet1004", err, fmt.Sprintf("Failed to fetch Config for %q: %s", serviceName, err))
	return ""
}
