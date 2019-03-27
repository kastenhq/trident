// Copyright 2018 NetApp, Inc. All Rights Reserved.

package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/netapp/trident/cli/api"
	"github.com/netapp/trident/frontend/rest"
	"github.com/netapp/trident/storage"
	drivers "github.com/netapp/trident/storage_drivers"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

func init() {
	getCmd.AddCommand(getBackendCmd)
}

var getBackendCmd = &cobra.Command{
	Use:     "backend [<name>...]",
	Short:   "Get one or more storage backends from Trident",
	Aliases: []string{"b", "backends"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if OperatingMode == ModeTunnel {
			command := []string{"get", "backend"}
			TunnelCommand(append(command, args...))
			return nil
		} else {
			return backendList(args)
		}
	},
}

func backendList(backendNames []string) error {

	baseURL, err := GetBaseURL()
	if err != nil {
		return err
	}

	// If no backends were specified, we'll get all of them
	if len(backendNames) == 0 {
		backendNames, err = GetBackends(baseURL)
		if err != nil {
			return err
		}
	}

	backends := make([]storage.BackendExternal, 0, 10)

	// Get the actual backend objects
	for _, backendName := range backendNames {

		backend, err := GetBackend(baseURL, backendName)
		if err != nil {
			return err
		}
		backends = append(backends, backend)
	}

	WriteBackends(backends)

	return nil
}

func GetBackends(baseURL string) ([]string, error) {

	url := baseURL + "/backend"

	response, responseBody, err := api.InvokeRESTAPI("GET", url, nil, Debug)
	if err != nil {
		return nil, err
	} else if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not get backends: %v",
			GetErrorFromHTTPResponse(response, responseBody))
	}

	var listBackendsResponse rest.ListBackendsResponse
	err = json.Unmarshal(responseBody, &listBackendsResponse)
	if err != nil {
		return nil, err
	}

	return listBackendsResponse.Backends, nil
}

func GetBackend(baseURL, backendName string) (storage.BackendExternal, error) {

	url := baseURL + "/backend/" + backendName

	response, responseBody, err := api.InvokeRESTAPI("GET", url, nil, Debug)
	if err != nil {
		return storage.BackendExternal{}, err
	} else if response.StatusCode != http.StatusOK {
		return storage.BackendExternal{}, fmt.Errorf("could not get backend %s: %v", backendName,
			GetErrorFromHTTPResponse(response, responseBody))
	}

	var getBackendResponse api.GetBackendResponse
	err = json.Unmarshal(responseBody, &getBackendResponse)
	if err != nil {
		return storage.BackendExternal{}, err
	}

	return getBackendResponse.Backend, nil
}

func WriteBackends(backends []storage.BackendExternal) {
	switch OutputFormat {
	case FormatJSON:
		WriteJSON(api.MultipleBackendResponse{Items: backends})
	case FormatYAML:
		WriteYAML(api.MultipleBackendResponse{Items: backends})
	case FormatName:
		writeBackendNames(backends)
	default:
		writeBackendTable(backends)
	}
}

func getESeriesStorageDriverConfig(configAsMap map[string]interface{}) (*drivers.ESeriesStorageDriverConfig, error) {
	jsonBytes, marshalError := json.MarshalIndent(configAsMap, "", "  ")
	if marshalError != nil {
		return nil, marshalError
	}

	var result drivers.ESeriesStorageDriverConfig
	err := json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func getFakeStorageDriverConfig(configAsMap map[string]interface{}) (*drivers.FakeStorageDriverConfig, error) {
	jsonBytes, marshalError := json.MarshalIndent(configAsMap, "", "  ")
	if marshalError != nil {
		return nil, marshalError
	}

	var result drivers.FakeStorageDriverConfig
	err := json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func getOntapStorageDriverConfig(configAsMap map[string]interface{}) (*drivers.OntapStorageDriverConfig, error) {
	jsonBytes, marshalError := json.MarshalIndent(configAsMap, "", "  ")
	if marshalError != nil {
		return nil, marshalError
	}

	var result drivers.OntapStorageDriverConfig
	err := json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func getSolidfireStorageDriverConfig(configAsMap map[string]interface{}) (*drivers.SolidfireStorageDriverConfig, error) {
	jsonBytes, marshalError := json.MarshalIndent(configAsMap, "", "  ")
	if marshalError != nil {
		return nil, marshalError
	}

	var result drivers.SolidfireStorageDriverConfig
	err := json.Unmarshal(jsonBytes, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func writeBackendTable(backends []storage.BackendExternal) {

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Storage Driver", "State", "Volumes"})

	for _, b := range backends {
		if b.Config == nil {
			continue
		}

		if configAsMap, ok := b.Config.(map[string]interface{}); ok {
			storageDriverName := configAsMap["storageDriverName"].(string)
			table.Append([]string{
				b.Name,
				storageDriverName,
				b.State.String(),
				strconv.Itoa(len(b.Volumes)),
			})
		}
	}

	table.Render()
}

func writeBackendNames(backends []storage.BackendExternal) {

	for _, b := range backends {
		fmt.Println(b.Name)
	}
}
