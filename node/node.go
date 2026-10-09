package node

import (
	"fmt"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/conf"
	vCore "github.com/InazumaV/V2bX/core"
)

type Node struct {
	controllers []*Controller
}

func New() *Node {
	return &Node{}
}

func (n *Node) Start(nodes []conf.NodeConfig, core vCore.Core) error {
	// Two controllers for the same panel node would share a durable queue.
	// Reject duplicates before starting any controller.
	seen := make(map[string]bool)
	for _, config := range nodes {
		typeName := strings.ToLower(config.ApiConfig.NodeType)
		if typeName == "v2ray" {
			typeName = "vmess"
		}
		key := fmt.Sprintf("%s\n%s\n%d", strings.TrimRight(config.ApiConfig.APIHost, "/"), typeName, config.ApiConfig.NodeID)
		if seen[key] {
			return fmt.Errorf("duplicate panel node: %s %s %d", config.ApiConfig.APIHost, typeName, config.ApiConfig.NodeID)
		}
		seen[key] = true
	}
	n.controllers = make([]*Controller, len(nodes))
	for i := range nodes {
		p, err := panel.New(&nodes[i].ApiConfig)
		if err != nil {
			return err
		}
		// Register controller service
		n.controllers[i] = NewController(core, p, &nodes[i].Options)
		err = n.controllers[i].Start()
		if err != nil {
			return fmt.Errorf("start node controller [%s-%s-%d] error: %s",
				nodes[i].ApiConfig.APIHost,
				nodes[i].ApiConfig.NodeType,
				nodes[i].ApiConfig.NodeID,
				err)
		}
	}
	return nil
}

func (n *Node) Close() {
	for _, c := range n.controllers {
		err := c.Close()
		if err != nil {
			panic(err)
		}
	}
	n.controllers = nil
}
