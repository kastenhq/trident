// Copyright 2019 NetApp, Inc. All Rights Reserved.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"github.com/netapp/trident/cli/api"
	"github.com/netapp/trident/frontend/rest"
	"github.com/netapp/trident/utils"
)

func init() {
	getCmd.AddCommand(getNodeCmd)
}

var getNodeCmd = &cobra.Command{
	Use:     "node [<name>...]",
	Short:   "Get one or more CSI provider nodes from Trident",
	Aliases: []string{"n", "nodes"},
	Hidden:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if OperatingMode == ModeTunnel {
			command := []string{"get", "node"}
			TunnelCommand(append(command, args...))
			return nil
		} else {
			return nodeList(args)
		}
	},
}

func nodeList(nodeNames []string) error {

	baseURL, err := GetBaseURL()
	if err != nil {
		return err
	}

	// If no nodes were specified, we'll get all of them
	if len(nodeNames) == 0 {
		nodeNames, err = GetNodes(baseURL)
		if err != nil {
			return err
		}
	}

	nodes := make([]utils.Node, 0, 10)

	// Get the actual node objects
	for _, nodeName := range nodeNames {

		node, err := GetNode(baseURL, nodeName)
		if err != nil {
			return err
		}
		nodes = append(nodes, *node)
	}

	WriteNodes(nodes)

	return nil
}

func GetNodes(baseURL string) ([]string, error) {

	url := baseURL + "/node"

	response, responseBody, err := api.InvokeRESTAPI("GET", url, nil, Debug)
	if err != nil {
		return nil, err
	} else if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not get nodes: %v",
			GetErrorFromHTTPResponse(response, responseBody))
	}

	var listNodesResponse rest.ListNodesResponse
	err = json.Unmarshal(responseBody, &listNodesResponse)
	if err != nil {
		return nil, err
	}

	return listNodesResponse.Nodes, nil
}

func GetNode(baseURL, nodeName string) (*utils.Node, error) {

	url := baseURL + "/node/" + nodeName

	response, responseBody, err := api.InvokeRESTAPI("GET", url, nil, Debug)
	if err != nil {
		return nil, err
	} else if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not get node %s: %v", nodeName,
			GetErrorFromHTTPResponse(response, responseBody))
	}

	var getNodeResponse rest.GetNodeResponse
	err = json.Unmarshal(responseBody, &getNodeResponse)
	if err != nil {
		return nil, err
	}

	return getNodeResponse.Node, nil
}

func WriteNodes(nodes []utils.Node) {
	switch OutputFormat {
	case FormatJSON:
		WriteJSON(api.MultipleNodeResponse{Items: nodes})
	case FormatYAML:
		WriteYAML(api.MultipleNodeResponse{Items: nodes})
	case FormatName:
		writeNodeNames(nodes)
	case FormatWide:
		writeWideNodeTable(nodes)
	default:
		writeNodeTable(nodes)
	}
}

func writeNodeTable(nodes []utils.Node) {

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name"})

	for _, n := range nodes {
		table.Append([]string{
			n.Name,
		})
	}

	table.Render()
}

func writeWideNodeTable(nodes []utils.Node) {

	table := tablewriter.NewWriter(os.Stdout)
	header := []string{
		"Name",
		"IQN",
	}
	table.SetHeader(header)

	for _, node := range nodes {

		table.Append([]string{
			node.Name,
			node.IQN,
		})
	}

	table.Render()
}

func writeNodeNames(nodes []utils.Node) {

	for _, n := range nodes {
		fmt.Println(n.Name)
	}
}
