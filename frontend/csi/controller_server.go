// Copyright 2019 NetApp, Inc. All Rights Reserved.

package csi

import (
	"fmt"
	"strconv"

	"github.com/container-storage-interface/spec/lib/go/csi"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	tridentconfig "github.com/netapp/trident/config"
	"github.com/netapp/trident/core"
	frontendcommon "github.com/netapp/trident/frontend/common"
	"github.com/netapp/trident/storage"
	"github.com/netapp/trident/utils"
)

func (p *Plugin) CreateVolume(
	ctx context.Context, req *csi.CreateVolumeRequest,
) (*csi.CreateVolumeResponse, error) {

	fields := log.Fields{"Method": "CreateVolume", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> CreateVolume")
	defer log.WithFields(fields).Debug("<<<< CreateVolume")

	// Check arguments
	if len(req.GetName()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume name missing in request")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities missing in request")
	}

	// Check for pre-existing volume with the same name
	existingVolume, err := p.orchestrator.GetVolume(req.Name)
	if err != nil && !core.IsNotFoundError(err) {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	// If pre-existing volume found, check for the requested capacity and already allocated capacity
	if existingVolume != nil {

		// Check if the size of existing volume is compatible with the new request
		existingSize, _ := strconv.ParseInt(existingVolume.Config.Size, 10, 64)
		if existingSize < int64(req.GetCapacityRange().GetRequiredBytes()) {
			return nil, status.Error(
				codes.AlreadyExists,
				fmt.Sprintf("volume %s (but with different size) already exists", req.GetName()))
		}

		// Request matches existing volume, so just return it
		csiVolume, err := p.getCSIVolumeFromTridentVolume(existingVolume)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		return &csi.CreateVolumeResponse{Volume: csiVolume}, nil
	}

	// Check for matching volume capabilities
	log.Debugf("Volume capabilities (%d): %v", len(req.GetVolumeCapabilities()), req.GetVolumeCapabilities())
	protocol := tridentconfig.ProtocolAny
	accessMode := tridentconfig.ModeAny
	fileSystem := ""
	//var mountFlags []string

	if req.GetVolumeCapabilities() != nil {
		for _, capability := range req.GetVolumeCapabilities() {

			// Ensure access type is "MountVolume"
			if block := capability.GetBlock(); block != nil {
				return nil, status.Error(codes.InvalidArgument, "block access type not supported")
			}

			// See if we have a backend for the specified access mode
			accessMode = p.getAccessForCSIAccessMode(capability.GetAccessMode().Mode)
			protocol = p.getProtocolForCSIAccessMode(capability.GetAccessMode().Mode)
			if !p.hasBackendForProtocol(protocol) {
				return nil, status.Error(codes.InvalidArgument, "no available storage for access mode")
			}

			// See if fsType was specified
			if mount := capability.GetMount(); mount != nil {
				fileSystem = mount.GetFsType()
				//mountFlags = mount.GetMountFlags()
			}
		}
	}

	// Find a matching storage class, or register a new one
	scConfig, err := frontendcommon.GetStorageClass(req.Parameters, p.orchestrator)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "could not create a storage class from volume request")
	}

	var sizeBytes int64
	if req.CapacityRange != nil {
		sizeBytes = req.CapacityRange.RequiredBytes
	}

	// Convert volume creation options into a Trident volume config
	volConfig, err := frontendcommon.GetVolumeConfig(req.Name, scConfig.Name, sizeBytes,
		req.Parameters, protocol, accessMode)
	if err != nil {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	// Copy any volume attributes from the capabilities
	if volConfig.FileSystem == "" {
		volConfig.FileSystem = fileSystem
	}

	// Invoke the orchestrator to create the new volume
	newVolume, err := p.orchestrator.AddVolume(volConfig)
	if err != nil {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	csiVolume, err := p.getCSIVolumeFromTridentVolume(newVolume)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.CreateVolumeResponse{Volume: csiVolume}, nil
}

func (p *Plugin) DeleteVolume(
	ctx context.Context, req *csi.DeleteVolumeRequest,
) (*csi.DeleteVolumeResponse, error) {

	fields := log.Fields{"Method": "DeleteVolume", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> DeleteVolume")
	defer log.WithFields(fields).Debug("<<<< DeleteVolume")

	if req.GetVolumeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "no volume ID provided")
	}

	err := p.orchestrator.DeleteVolume(req.VolumeId)
	if err != nil {
		log.WithFields(log.Fields{
			"volumeName": req.VolumeId,
			"error":      err,
		}).Debugf("Could not delete volume.")

		// In CSI, delete is idempotent, so don't return an error if the volume doesn't exist
		if !core.IsNotFoundError(err) {
			return nil, p.getCSIErrorForOrchestratorError(err)
		}
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func stashIscsiTargetPortals(publishInfo map[string]string, accessInfo utils.VolumeAccessInfo) {

	count := 1 + len(accessInfo.IscsiPortals)
	publishInfo["iscsiTargetPortalCount"] = strconv.Itoa(count)
	publishInfo["p1"] = accessInfo.IscsiTargetPortal
	for i, p := range accessInfo.IscsiPortals {
		key := fmt.Sprintf("p%d", i+2)
		publishInfo[key] = p
	}
}

func (p *Plugin) ControllerPublishVolume(
	ctx context.Context, req *csi.ControllerPublishVolumeRequest,
) (*csi.ControllerPublishVolumeResponse, error) {

	fields := log.Fields{"Method": "ControllerPublishVolume", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> ControllerPublishVolume")
	defer log.WithFields(fields).Debug("<<<< ControllerPublishVolume")

	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "no volume ID provided")
	}

	nodeID := req.GetNodeId()
	if nodeID == "" {
		return nil, status.Error(codes.InvalidArgument, "no node ID provided")
	}

	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "no volume capabilities provided")
	}

	// Make sure volume exists
	volume, err := p.orchestrator.GetVolume(volumeID)
	if err != nil {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	// Get node attributes from the node ID
	nodeInfo, err := p.orchestrator.GetNode(nodeID)
	if err != nil {
		log.WithField("node", nodeID).Error("Node info not found.")
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// Set up volume publish info with what we know about the node
	volumePublishInfo := &utils.VolumePublishInfo{
		Localhost: false,
		HostIQN:   []string{nodeInfo.IQN},
		HostIP:    []string{},
		HostName:  nodeInfo.Name,
	}

	// Update NFS export rules (?), add node IQN to igroup, etc.
	err = p.orchestrator.PublishVolume(volume.Config.Name, volumePublishInfo)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Build CSI controller publish info from volume publish info
	publishInfo := map[string]string{
		"protocol": string(volume.Config.Protocol),
	}

	if volume.Config.Protocol == tridentconfig.File {
		publishInfo["nfsServerIp"] = volume.Config.AccessInfo.NfsServerIP
		publishInfo["nfsPath"] = volume.Config.AccessInfo.NfsPath
	} else if volume.Config.Protocol == tridentconfig.Block {
		stashIscsiTargetPortals(publishInfo, volume.Config.AccessInfo)
		publishInfo["iscsiTargetIqn"] = volume.Config.AccessInfo.IscsiTargetIQN
		publishInfo["iscsiLunNumber"] = strconv.Itoa(int(volume.Config.AccessInfo.IscsiLunNumber))
		publishInfo["iscsiInterface"] = volume.Config.AccessInfo.IscsiInterface
		publishInfo["iscsiIgroup"] = volume.Config.AccessInfo.IscsiIgroup
		publishInfo["iscsiUsername"] = volume.Config.AccessInfo.IscsiUsername
		publishInfo["iscsiInitiatorSecret"] = volume.Config.AccessInfo.IscsiInitiatorSecret
		publishInfo["iscsiTargetSecret"] = volume.Config.AccessInfo.IscsiTargetSecret
		publishInfo["filesystemType"] = volumePublishInfo.FilesystemType
		publishInfo["useCHAP"] = strconv.FormatBool(volumePublishInfo.UseCHAP)
		publishInfo["sharedTarget"] = strconv.FormatBool(volumePublishInfo.SharedTarget)
	}

	return &csi.ControllerPublishVolumeResponse{PublishContext: publishInfo}, nil
}

func (p *Plugin) ControllerUnpublishVolume(
	ctx context.Context, req *csi.ControllerUnpublishVolumeRequest,
) (*csi.ControllerUnpublishVolumeResponse, error) {

	fields := log.Fields{"Method": "ControllerUnpublishVolume", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> ControllerUnpublishVolume")
	defer log.WithFields(fields).Debug("<<<< ControllerUnpublishVolume")

	volumeID := req.GetVolumeId()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "no volume ID provided")
	}

	// Make sure volume exists
	if _, err := p.orchestrator.GetVolume(volumeID); err != nil {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	// Apart from validation, Trident has nothing to do for this entry point
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (p *Plugin) ValidateVolumeCapabilities(
	ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest,
) (*csi.ValidateVolumeCapabilitiesResponse, error) {

	volumeID := req.GetVolumeId()

	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "no volume ID provided")
	}
	if req.GetVolumeCapabilities() == nil {
		return nil, status.Error(codes.InvalidArgument, "no volume capabilities provided")
	}

	volume, err := p.orchestrator.GetVolume(volumeID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "volume not found")
	}

	resp := &csi.ValidateVolumeCapabilitiesResponse{}

	for _, v := range req.GetVolumeCapabilities() {
		if volume.Config.AccessMode != p.getAccessForCSIAccessMode(v.GetAccessMode().Mode) {
			resp.Message = "Could not satisfy one or more access modes."
			return resp, nil
		}
		if block := v.GetBlock(); block != nil {
			if volume.Config.Protocol != tridentconfig.Block {
				resp.Message = "Could not satisfy block protocol."
				return resp, nil
			}
		} else {
			if volume.Config.Protocol != tridentconfig.File {
				resp.Message = "Could not satisfy file protocol."
				return resp, nil
			}
		}
	}

	confirmed := &csi.ValidateVolumeCapabilitiesResponse_Confirmed{}
	confirmed.VolumeCapabilities = req.GetVolumeCapabilities()

	resp.Confirmed = confirmed

	return resp, nil
}

func (p *Plugin) ListVolumes(
	ctx context.Context, req *csi.ListVolumesRequest,
) (*csi.ListVolumesResponse, error) {

	fields := log.Fields{"Method": "ListVolumes", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> ListVolumes")
	defer log.WithFields(fields).Debug("<<<< ListVolumes")

	volumes, err := p.orchestrator.ListVolumes()
	if err != nil {
		return nil, p.getCSIErrorForOrchestratorError(err)
	}

	entries := make([]*csi.ListVolumesResponse_Entry, 0)

	for _, volume := range volumes {
		if csiVolume, err := p.getCSIVolumeFromTridentVolume(volume); err == nil {
			entries = append(entries, &csi.ListVolumesResponse_Entry{Volume: csiVolume})
		}
	}

	return &csi.ListVolumesResponse{Entries: entries}, nil
}

func (p *Plugin) GetCapacity(
	ctx context.Context, req *csi.GetCapacityRequest,
) (*csi.GetCapacityResponse, error) {

	// Trident doesn't report pool capacities
	return nil, status.Error(codes.Unimplemented, "")
}

func (p *Plugin) ControllerGetCapabilities(
	ctx context.Context, req *csi.ControllerGetCapabilitiesRequest,
) (*csi.ControllerGetCapabilitiesResponse, error) {

	fields := log.Fields{"Method": "ControllerGetCapabilities", "Type": "CSI_Controller"}
	log.WithFields(fields).Debug(">>>> ControllerGetCapabilities")
	defer log.WithFields(fields).Debug("<<<< ControllerGetCapabilities")

	return &csi.ControllerGetCapabilitiesResponse{Capabilities: p.csCap}, nil
}

func (p *Plugin) CreateSnapshot(
	ctx context.Context, req *csi.CreateSnapshotRequest,
) (*csi.CreateSnapshotResponse, error) {

	// Trident doesn't support snapshots
	return nil, status.Error(codes.Unimplemented, "")
}

func (p *Plugin) DeleteSnapshot(
	ctx context.Context, req *csi.DeleteSnapshotRequest,
) (*csi.DeleteSnapshotResponse, error) {

	// Trident doesn't support snapshots
	return nil, status.Error(codes.Unimplemented, "")
}

func (p *Plugin) ListSnapshots(
	ctx context.Context, req *csi.ListSnapshotsRequest,
) (*csi.ListSnapshotsResponse, error) {

	// Trident doesn't support snapshots
	return nil, status.Error(codes.Unimplemented, "")
}

func (p *Plugin) getCSIVolumeFromTridentVolume(volume *storage.VolumeExternal) (*csi.Volume, error) {

	capacity, err := strconv.ParseInt(volume.Config.Size, 10, 64)
	if err != nil {
		log.WithFields(log.Fields{
			"volume": volume.Config.InternalName,
			"size":   volume.Config.Size,
		}).Warn("Could not parse volume size.")
		capacity = 0
	}

	attributes := map[string]string{
		"backend":      volume.Backend,
		"name":         volume.Config.Name,
		"internalName": volume.Config.InternalName,
		"protocol":     string(volume.Config.Protocol),
	}

	return &csi.Volume{
		CapacityBytes: capacity,
		VolumeId:      volume.Config.Name,
		VolumeContext: attributes,
	}, nil
}

func (p *Plugin) getAccessForCSIAccessMode(accessMode csi.VolumeCapability_AccessMode_Mode) tridentconfig.AccessMode {
	switch accessMode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:
		return tridentconfig.ReadWriteOnce
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		return tridentconfig.ReadWriteOnce
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
		return tridentconfig.ReadOnlyMany
	case csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:
		return tridentconfig.ReadWriteMany
	case csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:
		return tridentconfig.ReadWriteMany
	default:
		return tridentconfig.ModeAny
	}
}

func (p *Plugin) getProtocolForCSIAccessMode(accessMode csi.VolumeCapability_AccessMode_Mode) tridentconfig.Protocol {
	switch accessMode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER: // block or file OK
		return tridentconfig.ProtocolAny
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY: // block or file OK
		return tridentconfig.ProtocolAny
	case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY: // block or file OK
		return tridentconfig.ProtocolAny
	case csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER: // block or file OK
		return tridentconfig.ProtocolAny
	case csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER: // file required
		return tridentconfig.File
	default:
		return tridentconfig.ProtocolAny
	}
}

func (p *Plugin) hasBackendForProtocol(protocol tridentconfig.Protocol) bool {

	backends, err := p.orchestrator.ListBackends()
	if err != nil || backends == nil || len(backends) == 0 {
		return false
	}

	if protocol == tridentconfig.ProtocolAny {
		return true
	}

	for _, b := range backends {
		if b.Protocol == tridentconfig.ProtocolAny || b.Protocol == protocol {
			return true
		}
	}

	return false
}
