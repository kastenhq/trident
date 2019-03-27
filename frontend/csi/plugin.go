// Copyright 2019 NetApp, Inc. All Rights Reserved.

package csi

import (
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tridentconfig "github.com/netapp/trident/config"
	"github.com/netapp/trident/core"
	"github.com/netapp/trident/frontend/rest"
)

const (
	CSIController = "controller"
	CSINode       = "node"
	CSIAllInOne   = "allInOne"
)

type Plugin struct {
	orchestrator core.Orchestrator

	name     string
	nodeName string
	version  string
	endpoint string
	role     string

	restClient *RestClient

	grpc NonBlockingGRPCServer

	csCap []*csi.ControllerServiceCapability
	nsCap []*csi.NodeServiceCapability
	vCap  []*csi.VolumeCapability_AccessMode
}

func NewControllerPlugin(nodeName, endpoint string, orchestrator core.Orchestrator) (*Plugin, error) {

	p := &Plugin{
		orchestrator: orchestrator,
		name:         csiPluginName,
		nodeName:     nodeName,
		version:      tridentconfig.OrchestratorVersion.ShortString(),
		endpoint:     endpoint,
		role:         CSIController,
	}

	// Define controller capabilities
	p.addControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
	})

	// Define volume capabilities
	p.addVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	})

	return p, nil
}

func NewNodePlugin(nodeName, endpoint, caCert, clientCert, clientKey string,
	orchestrator core.Orchestrator) (*Plugin, error) {

	p := &Plugin{
		orchestrator: orchestrator,
		name:         csiPluginName,
		nodeName:     nodeName,
		version:      tridentconfig.OrchestratorVersion.ShortString(),
		endpoint:     endpoint,
		role:         CSINode,
	}

	p.addNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	})
	port := "34571"
	for _, envVar := range os.Environ() {
		values := strings.Split(envVar, "=")
		if values[0] == "TRIDENT_CSI_SERVICE_PORT" {
			port = values[1]
			break
		}
	}
	restURL := "https://" + rest.ServerCertName + ":" + port
	var err error
	p.restClient, err = CreateTLSRestClient(restURL, caCert, clientCert, clientKey)
	if err != nil {
		return nil, err
	}

	// Define volume capabilities
	p.addVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	})

	return p, nil
}

func NewAllInOnePlugin(nodeName, endpoint, caCert, clientCert, clientKey string,
	orchestrator core.Orchestrator) (*Plugin, error) {

	p := &Plugin{
		orchestrator: orchestrator,
		name:         csiPluginName,
		nodeName:     nodeName,
		version:      tridentconfig.OrchestratorVersion.ShortString(),
		endpoint:     endpoint,
		role:         CSIAllInOne,
	}

	// Define controller capabilities
	p.addControllerServiceCapabilities([]csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
	})

	p.addNodeServiceCapabilities([]csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
	})
	port := "34571"
	for _, envVar := range os.Environ() {
		values := strings.Split(envVar, "=")
		if values[0] == "TRIDENT_CSI_SERVICE_PORT" {
			port = values[1]
			break
		}
	}
	restURL := "https://" + rest.ServerCertName + ":" + port
	var err error
	p.restClient, err = CreateTLSRestClient(restURL, caCert, clientCert, clientKey)
	if err != nil {
		return nil, err
	}

	// Define volume capabilities
	p.addVolumeCapabilityAccessModes([]csi.VolumeCapability_AccessMode_Mode{
		csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	})

	return p, nil
}

func (p *Plugin) Activate() error {
	go func() {
		log.Info("Activating CSI frontend.")
		p.grpc = NewNonBlockingGRPCServer()
		p.grpc.Start(p.endpoint, p, p, p)
		if p.role == CSINode || p.role == CSIAllInOne {
			err := p.nodeRegisterWithController()
			if err != nil {
				log.Errorf("Error registering node %s with controller; %v", p.nodeName, err)
				p.grpc.GracefulStop()
			}
		}
	}()
	return nil
}

func (p *Plugin) Deactivate() error {
	log.Info("Deactivating CSI frontend.")
	p.grpc.GracefulStop()
	if p.role == CSINode || p.role == CSIAllInOne {
		err := p.nodeDeregisterWithController()
		if err != nil {
			log.Errorf("Error deregistering node %s with controller; %v", p.nodeName, err)
			return err
		}
	}
	return nil
}

func (p *Plugin) GetName() string {
	return string(tridentconfig.ContextCSI)
}

func (p *Plugin) Version() string {
	return csiVersion
}

func (p *Plugin) addControllerServiceCapabilities(cl []csi.ControllerServiceCapability_RPC_Type) {

	var csCap []*csi.ControllerServiceCapability

	for _, c := range cl {
		log.WithField("capability", c.String()).Info("Enabling controller service capability.")
		csCap = append(csCap, NewControllerServiceCapability(c))
	}

	p.csCap = csCap
}

func (p *Plugin) addNodeServiceCapabilities(cl []csi.NodeServiceCapability_RPC_Type) {

	var nsCap []*csi.NodeServiceCapability

	for _, c := range cl {
		log.WithField("capability", c.String()).Info("Enabling node service capability.")
		nsCap = append(nsCap, NewNodeServiceCapability(c))
	}

	p.nsCap = nsCap
}

func (p *Plugin) addVolumeCapabilityAccessModes(vc []csi.VolumeCapability_AccessMode_Mode) {

	var vCap []*csi.VolumeCapability_AccessMode

	for _, c := range vc {
		log.WithField("mode", c.String()).Info("Enabling volume access mode.")
		vCap = append(vCap, NewVolumeCapabilityAccessMode(c))
	}

	p.vCap = vCap
}

func (p *Plugin) getCSIErrorForOrchestratorError(err error) error {
	if core.IsNotReadyError(err) {
		return status.Error(codes.Unavailable, err.Error())
	} else if core.IsBootstrapError(err) {
		return status.Error(codes.FailedPrecondition, err.Error())
	} else if core.IsNotFoundError(err) {
		return status.Error(codes.NotFound, err.Error())
	} else {
		return status.Error(codes.Unknown, err.Error())
	}
}
