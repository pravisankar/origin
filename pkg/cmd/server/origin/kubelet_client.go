package origin

import (
	"net/http"
	"strconv"

	kapi "k8s.io/kubernetes/pkg/api"
	kubeletClient "k8s.io/kubernetes/pkg/kubelet/client"
	ktypes "k8s.io/kubernetes/pkg/types"
	nodeutil "k8s.io/kubernetes/pkg/util/node"
)

type OriginNodeConnectionInfoGetter struct {
	// nodes is used to look up Node objects
	nodes kubeletClient.NodeGetter
	// scheme is the scheme to use to connect to all kubelets
	scheme string
	// defaultPort is the port to use if no Kubelet endpoint port is recorded in the node status
	defaultPort int
	// transport is the transport to use to send a request to all kubelets
	transport http.RoundTripper
	// preferredAddressTypes specifies the preferred order to use to find a node address
	preferredAddressTypes []kapi.NodeAddressType
}

func NewOriginNodeConnectionInfoGetter(nodes kubeletClient.NodeGetter, config kubeletClient.KubeletClientConfig) (kubeletClient.ConnectionInfoGetter, error) {
	scheme := "http"
	if config.EnableHttps {
		scheme = "https"
	}

	transport, err := kubeletClient.MakeTransport(&config)
	if err != nil {
		return nil, err
	}

	types := []kapi.NodeAddressType{}
	for _, t := range config.PreferredAddressTypes {
		types = append(types, kapi.NodeAddressType(t))
	}

	return &OriginNodeConnectionInfoGetter{
		nodes:       nodes,
		scheme:      scheme,
		defaultPort: int(config.Port),
		transport:   transport,

		preferredAddressTypes: types,
	}, nil
}

func (c *OriginNodeConnectionInfoGetter) GetConnectionInfo(ctx kapi.Context, nodeName ktypes.NodeName) (*kubeletClient.ConnectionInfo, error) {
	node, err := c.nodes.Get(string(nodeName))
	if err != nil {
		return nil, err
	}

	// Find a kubelet-reported address, using preferred address type
	host, err := nodeutil.GetPreferredNodeAddress(node, c.preferredAddressTypes)
	if err != nil {
		return nil, err
	}

	// Use the kubelet-reported port, if present
	port := int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)
	if port <= 0 {
		port = c.defaultPort
	}

	return &kubeletClient.ConnectionInfo{
		Scheme:    c.scheme,
		Hostname:  host,
		Port:      strconv.Itoa(port),
		Transport: c.transport,
	}, nil
}
