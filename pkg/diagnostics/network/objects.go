package network

// Resource objects used by network diagnostics
import (
	"fmt"
	"strings"

	kapi "k8s.io/kubernetes/pkg/api"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/util/intstr"

	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
)

const (
	diagnosticsImage           = "openshift/diagnostics-deployer"
	networkDiagTestPodSelector = "network-diag-pod-name"

	testPodImage   = "docker.io/openshift/hello-openshift"
	testPodPort    = 9876
	testTargetPort = 8080
)

func GetNetworkDiagnosticsPod(command, podName, nodeName string) *kapi.Pod {
	privileged := true
	hostRootVolName := "host-root-dir"
	secretVolName := "kconfig-secret"
	secretDirBaseName := "secrets"
	gracePeriod := int64(0)

	cmd := fmt.Sprintf("openshift-network-debug %s %s", util.NetworkDiagContainerMountPath, command)

	pod := &kapi.Pod{
		ObjectMeta: kapi.ObjectMeta{Name: podName},
		Spec: kapi.PodSpec{
			RestartPolicy:                 kapi.RestartPolicyNever,
			TerminationGracePeriodSeconds: &gracePeriod,
			SecurityContext: &kapi.PodSecurityContext{
				HostPID:     true,
				HostIPC:     true,
				HostNetwork: true,
			},
			NodeName: nodeName,
			Containers: []kapi.Container{
				{
					Name:            podName,
					Image:           diagnosticsImage,
					ImagePullPolicy: kapi.PullIfNotPresent,
					SecurityContext: &kapi.SecurityContext{
						Privileged: &privileged,
					},
					Env: []kapi.EnvVar{
						{
							Name:  kclientcmd.RecommendedConfigPathEnvVar,
							Value: fmt.Sprintf("/%s/%s", secretDirBaseName, strings.ToLower(kclientcmd.RecommendedConfigPathEnvVar)),
						},
						{
							// Propagate OPENSHIFT_CONTAINERIZED env from node to pod
							// openshift-network-debug script uses this env to setup the environment for the pod
							Name: "OPENSHIFT_CONTAINERIZED",
						},
					},
					VolumeMounts: []kapi.VolumeMount{
						{
							Name:      hostRootVolName,
							MountPath: util.NetworkDiagContainerMountPath,
						},
						{
							Name:      secretVolName,
							MountPath: fmt.Sprintf("%s/%s", util.NetworkDiagContainerMountPath, secretDirBaseName),
							ReadOnly:  true,
						},
					},
					Command: []string{"sh", "-c", cmd},
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
							SecretName: util.NetworkDiagSecretName,
						},
					},
				},
			},
		},
	}
	return pod
}

func GetTestPod(podName, nodeName string) *kapi.Pod {
	gracePeriod := int64(0)

	return &kapi.Pod{
		ObjectMeta: kapi.ObjectMeta{
			Name: podName,
			Labels: map[string]string{
				networkDiagTestPodSelector: podName,
			},
		},
		Spec: kapi.PodSpec{
			RestartPolicy:                 kapi.RestartPolicyNever,
			TerminationGracePeriodSeconds: &gracePeriod,
			NodeName:                      nodeName,
			Containers: []kapi.Container{
				{
					Name:            podName,
					Image:           testPodImage,
					ImagePullPolicy: kapi.PullIfNotPresent,
				},
			},
		},
	}
}

func GetTestService(serviceName, podName, nodeName string) *kapi.Service {
	return &kapi.Service{
		ObjectMeta: kapi.ObjectMeta{Name: serviceName},
		Spec: kapi.ServiceSpec{
			Type: kapi.ServiceTypeClusterIP,
			Selector: map[string]string{
				networkDiagTestPodSelector: podName,
			},
			Ports: []kapi.ServicePort{
				{
					Protocol:   kapi.ProtocolTCP,
					Port:       testPodPort,
					TargetPort: intstr.FromInt(testTargetPort),
				},
			},
		},
	}
}
