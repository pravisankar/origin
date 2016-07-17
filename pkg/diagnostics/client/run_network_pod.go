package client

import (
	"bufio"
	"errors"
	"fmt"
	"io/ioutil"
	"regexp"
	"strconv"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	kapi "k8s.io/kubernetes/pkg/api"
	kerrs "k8s.io/kubernetes/pkg/api/errors"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/util/intstr"
	"k8s.io/kubernetes/pkg/util/wait"

	osclient "github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/cmd/cli/config"
	osclientcmd "github.com/openshift/origin/pkg/cmd/util/clientcmd"
	"github.com/openshift/origin/pkg/diagnostics/types"
	"github.com/openshift/origin/pkg/diagnostics/util"
	sdnapi "github.com/openshift/origin/pkg/sdn/api"
	sdnplugin "github.com/openshift/origin/pkg/sdn/plugin"
)

const (
	NetworkDiagnosticName = "NetworkCheck"

	clientRetryCount    = 24
	clientRetryInterval = 5 * time.Second
	clientRetryFactor   = 1
	debugScript         = "https://github.com/openshift/origin/blob/master/hack/debug-network.sh --skip-tests"
)

// NetworkDiagnostic is a diagnostic that runs a network diagnostic pod and relays the results.
type NetworkDiagnostic struct {
	KubeClient          *kclient.Client
	OSClient            *osclient.Client
	ClientFlags         *flag.FlagSet
	Level               int
	Factory             *osclientcmd.Factory
	PreventModification bool
}

// Name is part of the Diagnostic interface and just returns name.
func (d *NetworkDiagnostic) Name() string {
	return NetworkDiagnosticName
}

// Description is part of the Diagnostic interface and provides a user-focused description of what the diagnostic does.
func (d *NetworkDiagnostic) Description() string {
	return "Create a pod on all schedulable nodes and run network diagnostics from the application standpoint"
}

// CanRun is part of the Diagnostic interface; it determines if the conditions are right to run this diagnostic.
func (d *NetworkDiagnostic) CanRun() (bool, error) {
	if d.PreventModification {
		return false, errors.New("running the network diagnostic pod is an API change, which is prevented as you indicated")
	} else if d.KubeClient == nil {
		return false, errors.New("must have kube client")
	} else if d.OSClient == nil {
		return false, errors.New("must have openshift client")
	} else if _, err := d.getKubeConfig(); err != nil {
		return false, err
	}
	return true, nil
}

// Check is part of the Diagnostic interface; it runs the actual diagnostic logic
func (d *NetworkDiagnostic) Check() types.DiagnosticResult {
	r := types.NewDiagnosticResult(NetworkDiagnosticName)

	pluginName, ok, err := util.GetOpenShiftNetworkPlugin(d.OSClient)
	if err != nil {
		r.Error("DNet2001", err, fmt.Sprintf("Checking network plugin failed. Error: %s", err))
		return r
	}
	if !ok {
		r.Warn("DNet2002", nil, fmt.Sprintf("Skipping network diagnostics check. Reason: Not using openshift network plugin."))
		return r
	}

	nodes, err := util.GetSchedulableNodes(d.KubeClient)
	if err != nil {
		r.Error("DNet2003", err, fmt.Sprintf("Fetching schedulable nodes failed. Error: %s", err))
		return r
	}
	if len(nodes) == 0 {
		r.Warn("DNet2004", nil, fmt.Sprint("Skipping network checks. Reason: No schedulable/ready nodes found."))
		return r
	}

	d.runNetworkDiagnostic(nodes, pluginName, r)
	return r
}

func (d *NetworkDiagnostic) runNetworkDiagnostic(nodes []kapi.Node, pluginName string, r types.DiagnosticResult) {
	nsName := util.NetworkDiagnosticNamespace
	globalNsName := util.NetworkDiagnosticGlobalNamespace

	nsList := []string{nsName}
	if sdnplugin.IsOpenShiftMultitenantNetworkPlugin(pluginName) {
		nsList = append(nsList, globalNsName)
	}

	for _, name := range nsList {
		// Delete old network diagnostics namespace if exists
		d.KubeClient.Namespaces().Delete(name)

		// Create a new namespace for network diagnostics
		_, err := d.KubeClient.Namespaces().Create(&kapi.Namespace{ObjectMeta: kapi.ObjectMeta{Name: name}})
		if err != nil && !kerrs.IsAlreadyExists(err) {
			r.Error("DNet2005", err, fmt.Sprintf("Creating namespace %q failed. Error: %v", name, err))
			return
		}
		if name == util.NetworkDiagnosticGlobalNamespace {
			if err := d.makeNamespaceGlobal(name); err != nil {
				r.Error("DNet2006", err, fmt.Sprintf("Making namespace %q global failed. Error: %v", name, err))
				return
			}
		}
	}
	defer func() {
		// Delete what we created, or notify that we couldn't
		// Corresponding service accounts/pods in the namespace will be automatically deleted
		d.KubeClient.Namespaces().Delete(nsName)
		d.KubeClient.Namespaces().Delete(globalNsName)
	}()

	// Create service account for network diagnostics
	saName := util.NetworkDiagnosticServiceAccountName
	_, err := d.KubeClient.ServiceAccounts(nsName).Create(&kapi.ServiceAccount{ObjectMeta: kapi.ObjectMeta{Name: saName}})
	if err != nil && !kerrs.IsAlreadyExists(err) {
		r.Error("DNet2007", err, fmt.Sprintf("Creating service account %q failed. Error: %s", saName, err))
		return
	}

	// Create SCC needed for network diagnostics
	// Need privileged scc + some more network capabilities
	scc, err := d.KubeClient.SecurityContextConstraints().Get("privileged")
	if err != nil {
		r.Error("DNet2008", err, fmt.Sprintf("Fetching privileged scc failed. Error: %s", err))
		return
	}

	sccName := util.NetworkDiagnosticSCCName
	scc.ObjectMeta = kapi.ObjectMeta{Name: sccName}
	scc.AllowedCapabilities = []kapi.Capability{"NET_ADMIN"}
	scc.Users = []string{fmt.Sprintf("system:serviceaccount:%s:%s", nsName, saName)}
	if _, err = d.KubeClient.SecurityContextConstraints().Create(scc); err != nil && !kerrs.IsAlreadyExists(err) {
		r.Error("DNet2009", err, fmt.Sprintf("Creating security context constraint %q failed. Error: %s", sccName, err))
		return
	}
	defer func() {
		if err := d.KubeClient.SecurityContextConstraints().Delete(sccName); err != nil {
			r.Error("DNet2010", err, fmt.Sprintf("Deleting security context constraint %q failed. Error: %s", sccName, err))
		}
	}()

	// Store kubeconfig as secret, used by network diagnostic pod
	kconfigData, err := d.getKubeConfig()
	if err != nil {
		r.Error("DNet2011", err, fmt.Sprintf("Fetching kube config for network pod failed. Error: %s", err))
		return
	}
	secret := &kapi.Secret{}
	secret.Name = util.NetworkDiagnosticSecretName
	secret.Data = map[string][]byte{strings.ToLower(kclientcmd.RecommendedConfigPathEnvVar): kconfigData}
	if _, err = d.KubeClient.Secrets(nsName).Create(secret); err != nil && !kerrs.IsAlreadyExists(err) {
		r.Error("DNet2012", err, fmt.Sprintf("Creating secret %q failed. Error: %s", util.NetworkDiagnosticSecretName, err))
		return
	}

	// Create test pods and services on all valid nodes
	for _, node := range nodes {
		err := d.createTestPodAndService(&node, nsList)
		if err != nil {
			r.Error("DNet2013", err, fmt.Sprintf("Failed to create network diags test pod and service on node %q, %v", node.Name, err))
			continue
		}
	}
	// Wait for test pods and services to be up and running on all valid nodes
	err = d.waitForTestPodAndService(nsList)
	if err != nil {
		r.Error("DNet2014", err, fmt.Sprintf("Failed to run network diags test pod and service, %v", err))
		return
	}

	// Run network diagnostic pod on all valid nodes
	nerrs := 0
	for _, node := range nodes {
		podName, err := d.runNetworkPod(&node)
		if err != nil {
			r.Error("DNet2015", err, err.Error())
			continue
		}
		r.Debug("DNet2016", fmt.Sprintf("Created network diagnostic pod on node %q.", node.Name))

		// Gather logs from network diagnostic pod
		nerrs += d.collectNetworkPodLogs(&node, podName, r)
	}

	if nerrs > 0 {
		r.Info("DNet2017", fmt.Sprintf("Retry network diagnostics, if the errors persist then run %q for further analysis.", debugScript))
	}
}

func (d *NetworkDiagnostic) waitForTestPodAndService(nsList []string) error {
	backoff := wait.Backoff{
		Steps:    clientRetryCount,
		Duration: clientRetryInterval,
		Factor:   clientRetryFactor,
	}
	for _, nsName := range nsList {
		status_err := wait.ExponentialBackoff(backoff, func() (bool, error) {
			podList, err := d.KubeClient.Pods(nsName).List(kapi.ListOptions{})
			if err != nil {
				return false, err
			}

			for _, pod := range podList.Items {
				if pod.Status.Phase == kapi.PodRunning {
					continue
				}
				return false, nil
			}
			return true, nil
		})
		if status_err != nil {
			return status_err
		}
	}
	return nil
}

func (d *NetworkDiagnostic) createTestPodAndService(node *kapi.Node, nsList []string) error {
	// Create 2 pods and 2 services in global and non-global network diagnostic namespaces
	for i := 0; i <= 2; i++ {
		testPodName := kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", util.NetworkDiagnosticTestPodName))
		testServiceName := kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", util.NetworkDiagnosticTestServiceName))
		for _, nsName := range nsList {
			// Create network diags test pod on the given node for the given namespace
			_, err := d.KubeClient.Pods(nsName).Create(d.getTestPod(testPodName, node.Name))
			if err != nil {
				return fmt.Errorf("Creating network diagnostic test pod '%s/%s' on node %q failed. Error: %v", nsName, testPodName, node.Name, err)
			}

			// Create network diags test service on the given node for the given namespace
			_, err = d.KubeClient.Services(nsName).Create(d.getTestService(testServiceName, testPodName, node.Name))
			if err != nil {
				return fmt.Errorf("Creating network diagnostic test service '%s/%s' on node %q failed. Error: %v", nsName, testServiceName, node.Name, err)
			}
		}
	}
	return nil
}

func (d *NetworkDiagnostic) runNetworkPod(node *kapi.Node) (string, error) {
	podName := kapi.SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-", util.NetworkDiagnosticPodName))
	_, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Create(d.getNetworkDiagnosticsPod(podName, node.Name))
	if err != nil {
		return podName, fmt.Errorf("Creating network diagnostic pod %q on node %q failed. Error: %v", podName, node.Name, err)
	}
	return podName, nil
}

func (d *NetworkDiagnostic) collectNetworkPodLogs(node *kapi.Node, podName string, r types.DiagnosticResult) int {
	pod, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Get(podName) // status is filled in post-create
	if err != nil {
		r.Error("DNet2018", err, fmt.Sprintf("Retrieving network diagnostic pod %q on node %q failed. Error: %v", podName, node.Name, err))
		return 1
	}

	// Wait for network pod operation to complete
	podClient := d.KubeClient.Pods(util.NetworkDiagnosticNamespace)
	if err := wait.PollImmediate(500*time.Millisecond, 30*time.Second, networkPodComplete(podClient, podName, node.Name)); err != nil && err == wait.ErrWaitTimeout {
		err = fmt.Errorf("pod %q on node %q timedout(30 secs)", podName, node.Name)
		r.Error("DNet2019", err, err.Error())
	}

	bytelim := int64(1024000)
	opts := &kapi.PodLogOptions{
		TypeMeta:   pod.TypeMeta,
		Container:  podName,
		Follow:     true,
		LimitBytes: &bytelim,
	}

	req, err := d.Factory.LogsForObject(pod, opts)
	if err != nil {
		r.Error("DNet2020", err, fmt.Sprintf("The request for network diagnostic pod failed unexpectedly on node %q. Error: %v", node.Name, err))
		return 1
	}

	readCloser, err := req.Stream()
	if err != nil {
		r.Error("DNet2021", err, fmt.Sprintf("Logs for network diagnostic pod failed on node %q. Error: %v", node.Name, err))
		return 1
	}
	defer readCloser.Close()

	scanner := bufio.NewScanner(readCloser)
	podLogs, nwarnings, nerrors := "", 0, 0
	errorRegex := regexp.MustCompile(`^\[Note\]\s+Errors\s+seen:\s+(\d+)`)
	warnRegex := regexp.MustCompile(`^\[Note\]\s+Warnings\s+seen:\s+(\d+)`)

	for scanner.Scan() {
		line := scanner.Text()
		podLogs += line + "\n"
		if matches := errorRegex.FindStringSubmatch(line); matches != nil {
			nerrors, _ = strconv.Atoi(matches[1])
		} else if matches := warnRegex.FindStringSubmatch(line); matches != nil {
			nwarnings, _ = strconv.Atoi(matches[1])
		}
	}

	if err := scanner.Err(); err != nil { // Scan terminated abnormally
		r.Error("DNet2022", err, fmt.Sprintf("Unexpected error reading network diagnostic pod logs on node %q: (%T) %[1]v\nLogs are:\n%[2]s", node.Name, err, podLogs))
	} else {
		if nerrors > 0 {
			r.Error("DNet2023", nil, fmt.Sprintf("See the errors below in the output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		} else if nwarnings > 0 {
			r.Warn("DNet2024", nil, fmt.Sprintf("See the warnings below in the output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		} else {
			r.Info("DNet2025", fmt.Sprintf("Output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		}
	}
	return nerrors
}

func networkPodComplete(c kclient.PodInterface, podName, nodeName string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.Get(podName)
		if err != nil {
			if kerrs.IsNotFound(err) {
				return false, fmt.Errorf("pod %q was deleted on node %q; unable to determine whether it completed successfully", podName, nodeName)
			}
			return false, nil
		}
		switch pod.Status.Phase {
		case kapi.PodSucceeded:
			return true, nil
		case kapi.PodFailed:
			return true, fmt.Errorf("pod %q on node %q did not complete successfully", podName, nodeName)
		default:
			return false, nil
		}
	}
}

func (d *NetworkDiagnostic) getNetworkDiagnosticsPod(podName, nodeName string) *kapi.Pod {
	loglevel := d.Level
	if loglevel > 2 {
		loglevel = 2 // need to show summary at least
	}

	privileged := true
	hostRootVolName := "host-root-dir"
	containerMountPath := "/host"
	secretVolName := "kconfig-secret"
	secretDirBaseName := "secrets"

	return &kapi.Pod{
		ObjectMeta: kapi.ObjectMeta{Name: podName},
		Spec: kapi.PodSpec{
			RestartPolicy:      kapi.RestartPolicyNever,
			ServiceAccountName: util.NetworkDiagnosticServiceAccountName,
			SecurityContext: &kapi.PodSecurityContext{
				HostPID:     true,
				HostIPC:     true,
				HostNetwork: true,
			},
			NodeName: nodeName,
			Containers: []kapi.Container{
				{
					Name:            podName,
					Image:           "docker.io/busybox",
					ImagePullPolicy: kapi.PullIfNotPresent,
					SecurityContext: &kapi.SecurityContext{
						Privileged: &privileged,
						Capabilities: &kapi.Capabilities{
							Add: []kapi.Capability{
								// To run ping inside a container
								"NET_ADMIN",
							},
						},
					},
					Env: []kapi.EnvVar{
						{
							Name:  kclientcmd.RecommendedConfigPathEnvVar,
							Value: fmt.Sprintf("/%s/%s", secretDirBaseName, strings.ToLower(kclientcmd.RecommendedConfigPathEnvVar)),
						},
					},
					VolumeMounts: []kapi.VolumeMount{
						{
							Name:      hostRootVolName,
							MountPath: containerMountPath,
						},
						{
							Name:      secretVolName,
							MountPath: fmt.Sprintf("%s/%s", containerMountPath, secretDirBaseName),
							ReadOnly:  true,
						},
					},
					Command: []string{"chroot", containerMountPath, "openshift", "infra", "network-diagnostic-pod", "-l", strconv.Itoa(loglevel)},
				},
			},
			Volumes: []kapi.Volume{
				{
					Name: hostRootVolName,
					VolumeSource: kapi.VolumeSource{
						HostPath: &kapi.HostPathVolumeSource{
							Path: "/",
						},
					},
				},
				{
					Name: secretVolName,
					VolumeSource: kapi.VolumeSource{
						Secret: &kapi.SecretVolumeSource{
							SecretName: util.NetworkDiagnosticSecretName,
						},
					},
				},
			},
		},
	}
}

func (d *NetworkDiagnostic) getTestPod(podName, nodeName string) *kapi.Pod {
	return &kapi.Pod{
		ObjectMeta: kapi.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				util.NetworkDiagnosticTestPodSelector: podName,
			},
		},
		Spec: kapi.PodSpec{
			RestartPolicy: kapi.RestartPolicyNever,
			NodeName:      nodeName,
			Containers: []kapi.Container{
				{
					Name:            podName,
					Image:           "docker.io/openshift/hello-openshift",
					ImagePullPolicy: kapi.PullIfNotPresent,
				},
			},
		},
	}
}

func (d *NetworkDiagnostic) getTestService(serviceName, podName, nodeName string) *kapi.Service {
	return &kapi.Service{
		ObjectMeta: kapi.ObjectMeta{Name: serviceName},
		Spec: kapi.ServiceSpec{
			Type: kapi.ServiceTypeClusterIP,
			Selector: map[string]string{
				util.NetworkDiagnosticTestPodSelector: podName,
			},
			Ports: []kapi.ServicePort{
				{
					Protocol:   kapi.ProtocolTCP,
					Port:       util.NetworkDiagnosticTestPodPort,
					TargetPort: intstr.FromInt(8080),
				},
			},
		},
	}
}

func (d *NetworkDiagnostic) makeNamespaceGlobal(nsName string) error {
	netns, err := d.OSClient.NetNamespaces().Get(nsName)
	if err != nil {
		return err
	}
	sdnapi.SetChangePodNetworkAnnotation(netns, sdnapi.GlobalPodNetwork, "")

	if _, err = d.OSClient.NetNamespaces().Update(netns); err != nil {
		return err
	}

	backoff := wait.Backoff{
		Steps:    clientRetryCount,
		Duration: clientRetryInterval,
		Factor:   clientRetryFactor,
	}
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		updatedNetNs, err := d.OSClient.NetNamespaces().Get(netns.NetName)
		if err != nil {
			return false, err
		}

		_, _, err = sdnapi.GetChangePodNetworkAnnotation(updatedNetNs)
		if err == sdnapi.ErrorPodNetworkAnnotationNotFound {
			return true, nil
		}
		// Pod network change not applied yet
		return false, nil
	})
}

func (d *NetworkDiagnostic) getKubeConfig() ([]byte, error) {
	// KubeConfig path search order:
	// 1. User given config path
	// 2. Default admin config paths
	// 3. Default openshift client config search paths
	paths := []string{}
	paths = append(paths, d.ClientFlags.Lookup(config.OpenShiftConfigFlagName).Value.String())
	paths = append(paths, util.AdminKubeConfigPaths...)
	paths = append(paths, config.NewOpenShiftClientConfigLoadingRules().Precedence...)

	for _, path := range paths {
		if configData, err := ioutil.ReadFile(path); err == nil {
			return configData, nil
		}
	}
	return nil, fmt.Errorf("unable to find kube config")
}
