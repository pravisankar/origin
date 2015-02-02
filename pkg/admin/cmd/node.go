package cmd

import (
	"fmt"
	"io"
	
	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
)

type NodeConfig struct {
	name String
	kc kapi.Client
}

func NewNodeConfig() *NodeConfig {
	return &NodeConfig{}
}

func (n *NodeConfig) validate() error {
}

func (n *NodeConfig) Evacuate(out io.Writer) error {
	err := n.validate()
	if err != nil {
		return nil, err
	}

	var nodes *kapi.NodeList
	if nodes, err = n.kc.Nodes().List(); err != nil {
		return nil, err
	}

	for _, node := range nodes.Items {
		if node.ObjectMeta.Name != n.name {
			continue
		}
		if node.Status.Phase == kapi.NodeRunning {
		}
		evacNode = node
	}
}
