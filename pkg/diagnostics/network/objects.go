package network

// Resource objects used by network diagnostics
import (
	"fmt"
	"strconv"
	"strings"

	kapi "k8s.io/kubernetes/pkg/api"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/util/intstr"

	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
)

const (
	NetworkDiagnosticContainerMountPath = "/host"

	busyboxImage = "docker.io/busybox"
	pauseImage   = "docker.io/kubernetes/pause"

	testPodImage   = "docker.io/openshift/hello-openshift"
	testPodPort    = 9876
	testTargetPort = 8080
)

func GetNetworkDiagnosticsPod(podName, nodeName, image string, loglevel int) *kapi.Pod {
	if loglevel > 2 {
		loglevel = 2 // need to show summary at least
	}

	privileged := true
	hostRootVolName := "host-root-dir"
	secretVolName := "kconfig-secret"
	secretDirBaseName := "secrets"

	pod := &kapi.Pod{
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
					Image:           image,
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
							MountPath: NetworkDiagnosticContainerMountPath,
						},
						{
							Name:      secretVolName,
							MountPath: fmt.Sprintf("%s/%s", NetworkDiagnosticContainerMountPath, secretDirBaseName),
							ReadOnly:  true,
						},
					},
					Command: []string{"chroot", NetworkDiagnosticContainerMountPath, "openshift", "infra", "network-diagnostic-pod", "-l", strconv.Itoa(loglevel)},
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

	/*
		if image == pauseImage {
			pod.Spec.Containers[0].Stdin = false
			pod.Spec.Containers[0].StdinOnce = false
			pod.Spec.Containers[0].TTY = false
		}
	*/
	return pod
}

func GetNetworkDiagnosticsPausePod(podName, nodeName, image string, loglevel int) *kapi.Pod {
	if loglevel > 2 {
		loglevel = 2 // need to show summary at least
	}

	privileged := true
	hostRootVolName := "host-root-dir"

	pod := &kapi.Pod{
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
					Image:           busyboxImage,
					ImagePullPolicy: kapi.PullIfNotPresent,
					SecurityContext: &kapi.SecurityContext{
						Privileged: &privileged,
					},
					VolumeMounts: []kapi.VolumeMount{
						{
							Name:      hostRootVolName,
							MountPath: NetworkDiagnosticContainerMountPath,
						},
					},
					Command: []string{"chroot", NetworkDiagnosticContainerMountPath, "sleep", "1000"},
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
			},
		},
	}
	return pod
}

func GetTestPod(podName, nodeName string) *kapi.Pod {
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
				util.NetworkDiagnosticTestPodSelector: podName,
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
