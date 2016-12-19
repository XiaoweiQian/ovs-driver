package drivers

import (
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	pluginNet "github.com/docker/go-plugins-helpers/network"
)

const (
	mtuOption        = "mtu"
	vlanOption       = "vlan"
	bandwidthOption  = "bandwidth"
	brustOption      = "brust"
	containerEthName = "eth"
)

type networkTable map[string]*network
type endpointTable map[string]*endpoint

//Driver aa
type Driver struct {
	id       string
	ovsdb    *OvsdbDriver
	networks networkTable
	sync.Mutex
}

type subnet struct {
	subnetIP *net.IPNet
	gwIP     *net.IPNet
}

type network struct {
	id        string
	vlan      int
	gateway   string
	bandwidth int64
	brust     int64
	driver    *driver
	sbox      osl.Sandbox
	endpoints endpointTable
	subnets   []*subnet
	sync.Mutex
}

type endpoint struct {
	id       string
	nid      string
	intfName string
	mac      net.HardwareAddr
	addr     *net.IPNet
}

// NewDriver ...
func NewDriver() (*Driver, error) {
	// initiate the OvsdbDriver
	ovsdb, err := NewOvsdbDriver("ovsbr")
	if err != nil {
		return nil, fmt.Errorf("ovsdb driver init failed. Error: %s", err)
	}

	if ovsdb == nil {
		return nil, fmt.Errorf("could not connect to open vswitch")
	}

	d := &Driver{
		ovsdb:    ovsdb,
		networks: networkTable{},
	}
	return d, nil
}

// GetCapabilities ...
func (d *Driver) GetCapabilities() (*pluginNet.CapabilitiesResponse, error) {
	logrus.Debugf("GetCapabilities ovs")
	cap := &pluginNet.CapabilitiesResponse{Scope: pluginNet.GlobalScope}
	return cap, nil
}

// CreateNetwork ...
func (d *Driver) CreateNetwork(r *pluginNet.CreateNetworkRequest) error {
	logrus.Debugf("CreateNetwork ovs")
	id := r.NetworkID
	ipV4Data := r.IPv4Data
	if id == "" {
		return fmt.Errorf("invalid network id")
	}
	if len(ipV4Data) == 0 || ipV4Data[0].Pool.String() == "0.0.0.0/0" {
		return fmt.Errorf("ipv4 pool is empty")
	}
	gateway, mask, err := getGatewayIP(r)
	if err != nil {
		return err
	}

	n := &network{
		id:        id,
		driver:    d,
		endpoints: endpointTable{},
		subnets:   []*subnet{},
		gateway:   gateway,
		vlan:      getVlan(r),
		brust:     getBrust(r),
		bandwidth: getBandwith(r),
	}

	for i, ipd := range ipV4Data {
		s := &subnet{
			subnetIP: ipd.Pool,
			gwIP:     ipd.Gateway,
		}

		n.subnets = append(n.subnets, s)
	}

	d.Lock()
	d.networks[id] = n
	d.Unlock()

	return nil
}

// DeleteNetwork ...
func (d *Driver) DeleteNetwork(r *pluginNet.DeleteNetworkRequest) error {
	logrus.Debugf("DeleteNetwork ovs")
	nid := r.NetworkID
	if nid == "" {
		return fmt.Errorf("invalid network id")
	}

	d.Lock()
	n, ok := d.networks[nid]
	d.Unlock()
	if !ok {
		return fmt.Errorf("could not find network with id %s", nid)
	}

	for _, ep := range n.endpoints {
		if ep.intfName != "" {
			if err := d.ovsdb.DelPort(intfName); err != nil {
				return fmt.Errorf("ovs could not delete port with name %s", intfName)
			}
		}

	}
	d.Lock()
	delete(d.networks, nid)
	d.Unlock()

	return nil
}

// AllocateNetwork ...
func (d *Driver) AllocateNetwork(r *pluginNet.AllocateNetworkRequest) (*pluginNet.AllocateNetworkResponse, error) {
	logrus.Debugf("AllocateNetwork ovs")
	id := r.NetworkID
	ipV4Data := r.IPv4Data
	if id == "" {
		return nil, fmt.Errorf("invalid network id for ovs network")
	}

	if ipV4Data == nil {
		return nil, fmt.Errorf("empty ipv4 data passed during ovs network creation")
	}
	gateway, mask, err := getGatewayIP(r)
	if err != nil {
		return err
	}

	opts := r.Options

	n := &network{
		id:        id,
		driver:    d,
		gateway:   gateway,
		subnets:   []*subnet{},
		vlan:      getVlan(r),
		brust:     getBrust(r),
		bandwidth: getBandwith(r),
	}
	for i, ipd := range ipV4Data {
		s := &subnet{
			subnetIP: ipd.Pool,
			gwIP:     ipd.Gateway,
		}
		n.subnets = append(n.subnets, s)
	}
	d.Lock()
	d.networks[id] = n
	d.Unlock()

	return opts, nil

}

// FreeNetwork ...
func (d *Driver) FreeNetwork(r *pluginNet.FreeNetworkRequest) error {
	logrus.Debugf("FreeNetwork ovs")
	id := r.NetworkID
	if id == "" {
		return fmt.Errorf("invalid network id passed while freeing ovs network")
	}

	d.Lock()
	n, ok := d.networks[id]
	d.Unlock()

	if !ok {
		return fmt.Errorf("ovs network with id %s not found", id)
	}

	d.Lock()
	delete(d.networks, id)
	d.Unlock()

	return nil
}

// CreateEndpoint ...
func (d *Driver) CreateEndpoint(r *pluginNet.CreateEndpointRequest) (*pluginNet.CreateEndpointResponse, error) {
	logrus.Debugf("CreateEndpoint ovs")
	networkID := r.NetworkID
	if networkID == "" {
		return fmt.Errorf("invalid network id passed while create ovs endpoint")
	}
	endpointID := r.EndpointID
	if endpointID == "" {
		return fmt.Errorf("invalid endpoint id passed while create ovs endpoint")
	}
	intf := r.Interface
	if intf == nil {
		return fmt.Errorf("invalid interface passed while create ovs endpoint")
	}
	n, ok := d.networks[networkID]
	if !ok {
		return fmt.Errorf("ovs network with id %s not found", networkID)
	}
	ep := &endpoint{
		id:   endpointID,
		nid:  networkID,
		addr: intf.Address,
		mac:  intf.MacAddress,
	}
	if ep.addr == nil {
		return fmt.Errorf("create endpoint was not passed interface IP address")
	}

	if s := n.getSubnetforIP(ep.addr); s == nil {
		return fmt.Errorf("no matching subnet for IP %q in network %q", ep.addr, ep.nid)
	}

	if ep.mac == nil {
		ep.mac = GenerateMACFromIP(ep.addr.IP)
		intf.MacAddress = ep.mac
	}

	err := d.ovsdb.AddPort(addr, mac, intfName, n.vlan, n.brust, n.bandwidth)
	if err != nil {
		return fmt.Errorf("ovs create endpoint error with addr=%s,mac=%s,intfName=%s,vlan=%s,brust=%s,bandwidth=%s,err=%s", addr, mac, intfName, vlan, brust, bandwidth, err)
	}
	n.Lock()
	n.endpoints[ep.id] = ep
	n.Unlock()
	epResponse := &pluginNet.CreateEndpointResponse{Interface: intf}
	return epResponse, nil
}

// DeleteEndpoint ...
func (d *Driver) DeleteEndpoint(r *pluginNet.DeleteEndpointRequest) error {
	logrus.Debugf("DeleteEndpoint ovs")
	nid := r.NetworkID
	eid := r.EndpointID
	if nid == "" {
		return fmt.Errorf("invalid network id")
	}
	if eid == "" {
		return fmt.Errorf("invalid endpoint id")
	}
	n := d.networks[nid]
	if n == nil {
		return fmt.Errorf("network id %q not found", nid)
	}
	ep := n.endpoints[eid]
	if ep == nil {
		return fmt.Errorf("endpoint id %q not found", eid)
	}
	intfName := ep.intfName
	if intfName == "" {
		return nil
	}
	err := d.ovsdb.DelPort(intfName)
	if err != nil {
		return fmt.Errorf("ovs delete endpoint failed with InterfaceName=%s,err=%s", intfName, err)
	}
	n.Lock()
	delete(n.endpoints, eid)
	n.Unlock()

	return nil
}

// EndpointInfo ...
func (d *Driver) EndpointInfo(r *pluginNet.InfoRequest) (*pluginNet.InfoResponse, error) {
	logrus.Debugf("EndpointInfo ovs")
	res := &pluginNet.InfoResponse{
		Value: make(map[string]string),
	}
	return res, nil
}

// Join ...
func (d *Driver) Join(r *pluginNet.JoinRequest) (*pluginNet.JoinResponse, error) {
	logrus.Debugf("Join ovs")
	nid := r.NetworkID
	eid := r.EndpointID
	if nid == "" {
		return fmt.Errorf("invalid network id")
	}
	if eid == "" {
		return fmt.Errorf("invalid endpoint id")
	}
	n := d.networks[nid]
	if n == nil {
		return fmt.Errorf("network id %q not found", nid)
	}
	ep := n.endpoints[eid]
	if ep == nil {
		return fmt.Errorf("endpoint id %q not found", eid)
	}
	intfName := ep.intfName
	if intfName == nil {
		return fmt.Errorf("intfName %q empty", intfName)
	}
	s := n.getSubnetforIP(ep.addr)
	if s == nil {
		return fmt.Errorf("could not find subnet for endpoint %s", eid)
	}

	res := &pluginNet.JoinResponse{
		InterfaceName: pluginNet.InterfaceName{
			SrcName:   intfName,
			DstPrefix: containerEthName,
		},
		Gateway: n.gateway,
	}
	logrus.Debugf("Join ovs endpoint %s:%s to %s", r.NetworkID, r.EndpointID, r.SandboxKey)
	return res, nil

}

// Leave ...
func (d *Driver) Leave(r *pluginNet.LeaveRequest) error {
	logrus.Debugf("Leave ovs")
	nid := r.NetworkID
	eid := r.EndpointID
	if nid == "" {
		return fmt.Errorf("invalid network id")
	}
	if eid == "" {
		return fmt.Errorf("invalid endpoint id")
	}
	n := d.networks[nid]
	if n == nil {
		return fmt.Errorf("network id %q not found", nid)
	}
	ep := n.endpoints[eid]
	if ep == nil {
		return fmt.Errorf("endpoint id %q not found", eid)
	}
	intfName := ep.intfName
	if intfName == nil {
		return fmt.Errorf("intfName %q empty", intfName)
	}
	err := d.ovsdb.DelPort(intfName)
	if err != nil {
		return fmt.Errorf("ovs leave and delete endpoint failed with InterfaceName=%s,err=%s", intfName, err)
	}
	n.Lock()
	delete(n.endpoints, eid)
	n.Unlock()

	return nil
}

// DiscoverNew ...
func (d *Driver) DiscoverNew(r *pluginNet.DiscoveryNotification) error {
	panic("not implemented")
}

// DiscoverDelete ...
func (d *Driver) DiscoverDelete(r *pluginNet.DiscoveryNotification) error {
	panic("not implemented")
}

// ProgramExternalConnectivity ...
func (d *Driver) ProgramExternalConnectivity(r *pluginNet.ProgramExternalConnectivityRequest) error {
	panic("not implemented")
}

// RevokeExternalConnectivity ...
func (d *Driver) RevokeExternalConnectivity(r *pluginNet.RevokeExternalConnectivityRequest) error {
	panic("not implemented")
}

func getVlan(r *pluginNet.CreateNetworkRequest) (int, error) {
	vlan := 0
	if r.Options != nil {
		if v, ok := r.Options[vlanOption].(int); ok {
			vlan = v
		}
	}
	return vlan, nil
}
func getBandwith(r *pluginNet.CreateNetworkRequest) (int64, error) {
	bandwidth := 0
	if r.Options != nil {
		if b, ok := r.Options[bandwidthOption].(int); ok {
			bandwidth = b
		}
	}
	return bandwidth, nil
}
func getBrust(r *pluginNet.CreateNetworkRequest) (int64, error) {
	brust := 0
	if r.Options != nil {
		if b, ok := r.Options[brustOption].(int); ok {
			brust = b
		}
	}
	return brust, nil
}

// getSubnetforIP returns the subnet to which the given IP belongs
func (n *network) getSubnetforIP(ip *net.IPNet) *subnet {
	for _, s := range n.subnets {
		// first check if the mask lengths are the same
		i, _ := s.subnetIP.Mask.Size()
		j, _ := ip.Mask.Size()
		if i != j {
			continue
		}
		if s.subnetIP.Contains(ip.IP) {
			return s
		}
	}
	return nil
}

func getGatewayIP(r *pluginNet.CreateNetworkRequest) (string, string, error) {
	var gatewayIP string

	if len(r.IPv4Data) > 0 {
		if r.IPv4Data[0] != nil {
			if r.IPv4Data[0].Gateway != "" {
				gatewayIP = r.IPv4Data[0].Gateway
			}
		}
	}

	if gatewayIP == "" {
		return "", "", fmt.Errorf("No gateway IP found")
	}
	parts := strings.Split(gatewayIP, "/")
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("Cannot split gateway IP address")
	}
	return parts[0], parts[1], nil
}
