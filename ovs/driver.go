package ovs

import "github.com/docker/go-plugins-helpers/network"

//Driver aa
type Driver struct {
	id string
}

// GetCapabilities ...
func (d *Driver) GetCapabilities() (*network.CapabilitiesResponse, error) {
	panic("not implemented")
}

// CreateNetwork ...
func (d *Driver) CreateNetwork(*network.CreateNetworkRequest) error {
	panic("not implemented")
}

// AllocateNetwork ...
func (d *Driver) AllocateNetwork(*network.AllocateNetworkRequest) (*network.AllocateNetworkResponse, error) {
	panic("not implemented")
}

// DeleteNetwork ...
func (d *Driver) DeleteNetwork(*network.DeleteNetworkRequest) error {
	panic("not implemented")
}

// FreeNetwork ...
func (d *Driver) FreeNetwork(*network.FreeNetworkRequest) error {
	panic("not implemented")
}

// CreateEndpoint ...
func (d *Driver) CreateEndpoint(*network.CreateEndpointRequest) (*network.CreateEndpointResponse, error) {
	panic("not implemented")
}

// DeleteEndpoint ...
func (d *Driver) DeleteEndpoint(*network.DeleteEndpointRequest) error {
	panic("not implemented")
}

// EndpointInfo ...
func (d *Driver) EndpointInfo(*network.InfoRequest) (*network.InfoResponse, error) {
	panic("not implemented")
}

// Join ...
func (d *Driver) Join(*network.JoinRequest) (*network.JoinResponse, error) {
	panic("not implemented")
}

// Leave ...
func (d *Driver) Leave(*network.LeaveRequest) error {
	panic("not implemented")
}

// DiscoverNew ...
func (d *Driver) DiscoverNew(*network.DiscoveryNotification) error {
	panic("not implemented")
}

// DiscoverDelete ...
func (d *Driver) DiscoverDelete(*network.DiscoveryNotification) error {
	panic("not implemented")
}

// ProgramExternalConnectivity ...
func (d *Driver) ProgramExternalConnectivity(*network.ProgramExternalConnectivityRequest) error {
	panic("not implemented")
}

// RevokeExternalConnectivity ...
func (d *Driver) RevokeExternalConnectivity(*network.RevokeExternalConnectivityRequest) error {
	panic("not implemented")
}
