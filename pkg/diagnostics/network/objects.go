package network

// Resource objects used by network diagnostics
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kapi "k8s.io/kubernetes/pkg/api"
	kclientcmd "k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/util/intstr"

	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
)

const (
	busyboxImage = "docker.io/busybox"
	originImage  = "docker.io/openshift/origin"

	networkDiagTestPodSelector = "network-diag-pod-name"

	testPodImage   = "docker.io/openshift/hello-openshift"
	testPodPort    = 9876
	testTargetPort = 8080
)

func GetPrivilegedPod(command []string, podName, nodeName string) *kapi.Pod {
	privileged := true
	gracePeriod := int64(0)
	logDirName := "network-logdir"

	nodeLogDir := filepath.Join(util.NetworkDiagDefaultLogDir, util.NetworkDiagNodeLogDirPrefix, nodeName)
	return &kapi.Pod{
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
					Image:           busyboxImage,
					ImagePullPolicy: kapi.PullIfNotPresent,
					SecurityContext: &kapi.SecurityContext{
						Privileged: &privileged,
					},
					VolumeMounts: []kapi.VolumeMount{
						{
							Name:      logDirName,
							MountPath: nodeLogDir,
						},
					},
					Command: command,
				},
			},
			Volumes: []kapi.Volume{
				{
					Name: logDirName,
					VolumeSource: kapi.VolumeSource{
						HostPath: &kapi.HostPathVolumeSource{
							Path: nodeLogDir,
						},
					},
				},
			},
		},
	}
}

func GetNetworkDiagnosticsPod(command []string, podName, nodeName string, useOpenShiftImage bool) *kapi.Pod {
	secretVolName := "kconfig-secret"
	secretDirBaseName := "secrets"
	var secretDirMountPath string

	pod := GetPrivilegedPod(command, podName, nodeName)
	if useOpenShiftImage {
		pod.Spec.Containers[0].Image = originImage
		secretDirMountPath = filepath.Join("/", secretDirBaseName)

		osPath := fmt.Sprintf("$PATH:%s", os.Getenv("PATH"))
		// Mount /usr and /bin to access ovs-ofctl, docker and other binaries
		for i, path := range []string{"/usr", "/bin"} {
			name := fmt.Sprintf("vol%d", i)
			volume := kapi.Volume{
				Name: name,
				VolumeSource: kapi.VolumeSource{
					HostPath: &kapi.HostPathVolumeSource{
						Path: path,
					},
				},
			}
			mountPath := filepath.Join("/netdiag", path)
			volumeMount := kapi.VolumeMount{
				Name:      name,
				MountPath: mountPath,
			}

			pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
			pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, volumeMount)
			osPath = fmt.Sprintf("%s:%s", osPath, mountPath)
		}

		env := kapi.EnvVar{
			Name:  "PATH",
			Value: osPath,
		}
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, env)

	} else {
		// Mount root fs to reuse existing openshift binary on the node
		hostRootVolName := "host-root-dir"
		secretDirMountPath = filepath.Join(util.NetworkDiagContainerMountPath, secretDirBaseName)

		volume := kapi.Volume{
			Name: hostRootVolName,
			VolumeSource: kapi.VolumeSource{
				HostPath: &kapi.HostPathVolumeSource{
					Path: "/",
				},
			},
		}
		volumeMount := kapi.VolumeMount{
			Name:      hostRootVolName,
			MountPath: util.NetworkDiagContainerMountPath,
		}

		pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, volumeMount)
	}

	volume := kapi.Volume{
		Name: secretVolName,
		VolumeSource: kapi.VolumeSource{
			Secret: &kapi.SecretVolumeSource{
				SecretName: util.NetworkDiagSecretName,
			},
		},
	}
	volumeMount := kapi.VolumeMount{
		Name:      secretVolName,
		MountPath: secretDirMountPath,
		ReadOnly:  true,
	}
	env := kapi.EnvVar{
		Name:  kclientcmd.RecommendedConfigPathEnvVar,
		Value: filepath.Join("/", secretDirBaseName, strings.ToLower(kclientcmd.RecommendedConfigPathEnvVar)),
	}

	pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, volumeMount)
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, env)
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
