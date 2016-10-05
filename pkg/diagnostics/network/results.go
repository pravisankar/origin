package network

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	kapi "k8s.io/kubernetes/pkg/api"
	kerrs "k8s.io/kubernetes/pkg/api/errors"
	kclient "k8s.io/kubernetes/pkg/client/unversioned"
	kubecmd "k8s.io/kubernetes/pkg/kubectl/cmd"
	"k8s.io/kubernetes/pkg/util/wait"

	"github.com/openshift/origin/pkg/cmd/util/clientcmd"
	"github.com/openshift/origin/pkg/diagnostics/networkpod/util"
	"github.com/openshift/source-to-image/pkg/tar"
)

func (d *NetworkDiagnostic) CollectNetworkPodLogs(node *kapi.Node, podName string) int {
	pod, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Get(podName) // status is filled in post-create
	if err != nil {
		d.res.Error("DNet6001", err, fmt.Sprintf("Retrieving network diagnostic pod %q on node %q failed: %v", podName, node.Name, err))
		return 1
	}

	// Wait for network pod operation to complete
	podClient := d.KubeClient.Pods(util.NetworkDiagnosticNamespace)
	if err := wait.PollImmediate(500*time.Millisecond, 30*time.Second, networkPodComplete(podClient, podName, node.Name)); err != nil && err == wait.ErrWaitTimeout {
		err = fmt.Errorf("pod %q on node %q timedout(30 secs)", podName, node.Name)
		d.res.Error("DNet6002", err, err.Error())
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
		d.res.Error("DNet6003", err, fmt.Sprintf("The request for network diagnostic pod failed unexpectedly on node %q: %v", node.Name, err))
		return 1
	}

	readCloser, err := req.Stream()
	if err != nil {
		d.res.Error("DNet6004", err, fmt.Sprintf("Logs for network diagnostic pod failed on node %q: %v", node.Name, err))
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
		d.res.Error("DNet6005", err, fmt.Sprintf("Unexpected error reading network diagnostic pod logs on node %q: (%T) %[1]v\nLogs are:\n%[2]s", node.Name, err, podLogs))
	} else {
		if nerrors > 0 {
			d.res.Error("DNet6006", nil, fmt.Sprintf("See the errors below in the output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		} else if nwarnings > 0 {
			d.res.Warn("DNet6007", nil, fmt.Sprintf("See the warnings below in the output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		} else {
			d.res.Info("DNet6008", fmt.Sprintf("Output from the network diagnostic pod on node %q:\n%s", node.Name, podLogs))
		}
	}
	return nerrors
}

func (d *NetworkDiagnostic) CollectNetworkInfo(node *kapi.Node, podName string) int {
	pod, err := d.KubeClient.Pods(util.NetworkDiagnosticNamespace).Get(podName) // status is filled in post-create
	if err != nil {
		d.res.Error("DNet6001", err, fmt.Sprintf("Retrieving network diagnostic pod %q on node %q failed: %v", podName, node.Name, err))
		return 1
	}

	tarHelper := tar.New()
	tarHelper.SetExclusionPattern(nil)

	tmp, err := ioutil.TempFile("", "network-diags")
	if err != nil {
		d.res.Error("DNet", err, fmt.Sprintf("cannot create local temporary file for tar: %v", err))
		return 1
	}
	d.res.Info("DNet6010", fmt.Sprintf("Temp file name: %q\n", tmp.Name()))
	//defer os.Remove(tmp.Name())

	//glog.V(4).Infof("Creating local tar file %s from remote path %s", tmp.Name(), source.Path)
	errBuf := &bytes.Buffer{}
	// cmd := []string{"chroot", "/host", "cat", "/tmp/networklog.txt"}
	//	cmd := []string{"cp", "-r", fmt.Sprintf("%s:%s", pod.Name, "/tmp/networklog.txt")}
	nodeLogDir := filepath.Join("/tmp/nodes", node.Name)
	cmd := []string{"chroot", util.NetworkDiagnosticContainerMountPath, "tar", "-C", nodeLogDir, "-c", "."}
	err = Execute(cmd, d.Factory, pod, nil, tmp, errBuf)
	if err != nil {
		d.res.Error("DNet", err, fmt.Sprintf("Creating remote tar locally failed: %v, %s", err, errBuf.String()))
		return 1
	}

	err = tmp.Close()
	if err != nil {
		d.res.Error("Dnet", err, fmt.Sprintf("error closing temporary tar file %s: %v", tmp.Name(), err))
		return 1
	}
	tmp, err = os.Open(tmp.Name())
	if err != nil {
		d.res.Error("Dnet", err, fmt.Sprintf("can not open temporary tar file %s: %v", tmp.Name(), err))
		return 1
	}
	defer tmp.Close()

	err = tarHelper.ExtractTarStream("/tmp/copiedresult", tmp)
	//	err = Execute(cmd, d.Factory, pod, nil, tmp, errBuf)
	//    err = tarRemote(r.RemoteExecutor, fmt.Sprintf("%s:%s", pod.Name, "/tmp/networklog.txt"), tmp, errBuf)
	if err != nil {
		/*
			if checkTar(r.RemoteExecutor) != nil {
				return strategySetupError("tar not available in container")
			}
			io.Copy(errOut, errBuf)
		*/
		d.res.Error("DNet", err, fmt.Sprintf("untar local directory failed: %v, %s", err, errBuf.String()))
		return 1
	}
	return 0
}

// Execute will run a command in a pod
func Execute(command []string, f *clientcmd.Factory, pod *kapi.Pod, in io.Reader, out, errOut io.Writer) error {
	//	glog.V(3).Infof("Remote executor running command: %s", strings.Join(command, " "))
	config, err := f.ClientConfig()
	if err != nil {
		return err
	}

	client, err := f.Client()
	if err != nil {
		return err
	}

	execOptions := &kubecmd.ExecOptions{
		StreamOptions: kubecmd.StreamOptions{
			Namespace:     pod.Namespace,
			PodName:       pod.Name,
			ContainerName: pod.Name,
			In:            in,
			Out:           out,
			Err:           errOut,
			Stdin:         in != nil,
		},
		Executor: &kubecmd.DefaultRemoteExecutor{},
		Client:   client,
		Config:   config,
		Command:  command,
	}
	err = execOptions.Validate()
	if err != nil {
		//		glog.V(4).Infof("Error from remote command validation: %v", err)
		return err
	}
	err = execOptions.Run()
	if err != nil {
		//		glog.V(4).Infof("Error from remote execution: %v", err)
	}
	return err
}

/*
func tarRemote(exec executor, sourceDir string, out, errOut io.Writer) error {
	glog.V(4).Infof("Tarring %s remotely", sourceDir)
	var cmd []string
	if strings.HasSuffix(sourceDir, "/") {
		cmd = []string{"tar", "-C", sourceDir, "-c", "."}
	} else {
		cmd = []string{"tar", "-C", path.Dir(sourceDir), "-c", path.Base(sourceDir)}
	}
	glog.V(4).Infof("Remote tar command: %s", strings.Join(cmd, " "))
	return exec.Execute(cmd, nil, out, errOut)
}

func untarLocal(tar tar.Tar, destinationDir string, r io.Reader, quiet bool, logger io.Writer) error {
	glog.V(4).Infof("Extracting tar locally to %s", destinationDir)
	if quiet {
		return tar.ExtractTarStream(destinationDir, r)
	}
	return tar.ExtractTarStreamWithLogging(destinationDir, r, logger)
}
*/

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
