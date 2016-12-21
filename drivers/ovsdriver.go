package drivers

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/Sirupsen/logrus"
	pluginNet "github.com/docker/go-plugins-helpers/network"
)

const (
	ovsBridgeName    = "ovs-br0"
	mtuOption        = "mtu"
	vlanOption       = "vlan"
	bandwidthOption  = "bandwidth"
	brustOption      = "brust"
	genericOption    = "com.docker.network.generic"
	intfLen          = 7
	intfPrefix       = "port"
	containerEthName = "eth"
	useVeth          = true
	internalPort     = "internal"
	vethPort         = ""
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
	bandwidth int
	brust     int
	driver    *Driver
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
	ovsdb, err := NewOvsdbDriver(ovsBridgeName)
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
	id := r.NetworkID
	opts := r.Options
	ipV4Data := r.IPv4Data
	logrus.Debugf("CreateNetwork ovs with networkID=%s,opts=%s", id, opts)
	if id == "" {
		return fmt.Errorf("invalid network id")
	}
	if len(ipV4Data) == 0 {
		return fmt.Errorf("ipv4 pool is empty")
	}
	gateway, _, err := getGatewayIP(ipV4Data)
	if err != nil {
		return err
	}
	n := &network{
		id:        id,
		driver:    d,
		endpoints: endpointTable{},
		subnets:   []*subnet{},
		gateway:   gateway,
		vlan:      getVlan(opts),
		brust:     getBrust(opts),
		bandwidth: getBandwidth(opts),
	}

	var pool, gw *net.IPNet
	for _, ipd := range ipV4Data {
		_, pool, _ = net.ParseCIDR(ipd.Pool)
		_, gw, _ = net.ParseCIDR(ipd.Gateway)
		s := &subnet{
			subnetIP: pool,
			gwIP:     gw,
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
	var ovsPortName string
	for _, ep := range n.endpoints {
		if ep.intfName != "" {
			if useVeth {
				// Get OVS port name
				ovsPortName = getOvsPortName(ep.intfName)
				if err := DeleteVethPair(ep.intfName, ovsPortName); err != nil {
					return fmt.Errorf("delete veth pair failed with InterfaceName=%s,peer=%s,err=%s", ep.intfName, ovsPortName, err)
				}
			}
			if err := d.ovsdb.DelPort(ovsPortName); err != nil {
				return fmt.Errorf("ovs could not delete port with name %s", ovsPortName)
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
	id := r.NetworkID
	opts := r.Options
	logrus.Debugf("AllocateNetwork ovs with networkID=%s,opts=%s", id, opts)
	ipV4Data := r.IPv4Data
	if id == "" {
		return nil, fmt.Errorf("invalid network id for ovs network")
	}

	if ipV4Data == nil {
		return nil, fmt.Errorf("empty ipv4 data passed during ovs network creation")
	}

	n := &network{
		id:      id,
		driver:  d,
		subnets: []*subnet{},
	}
	var pool, gw *net.IPNet
	for _, ipd := range ipV4Data {
		_, pool, _ = net.ParseCIDR(ipd.Pool)
		_, gw, _ = net.ParseCIDR(ipd.Gateway)
		s := &subnet{
			subnetIP: pool,
			gwIP:     gw,
		}
		n.subnets = append(n.subnets, s)
	}
	d.Lock()
	d.networks[id] = n
	d.Unlock()
	res := &pluginNet.AllocateNetworkResponse{Options: opts}

	return res, nil

}

// FreeNetwork ...
func (d *Driver) FreeNetwork(r *pluginNet.FreeNetworkRequest) error {
	logrus.Debugf("FreeNetwork ovs")
	id := r.NetworkID
	if id == "" {
		return fmt.Errorf("invalid network id passed while freeing ovs network")
	}

	d.Lock()
	_, ok := d.networks[id]
	d.Unlock()

	if !ok {
		logrus.Debugf("ovs network with id %s not found", id)
		return nil
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
		return nil, fmt.Errorf("invalid network id passed while create ovs endpoint")
	}
	endpointID := r.EndpointID
	if endpointID == "" {
		return nil, fmt.Errorf("invalid endpoint id passed while create ovs endpoint")
	}
	intf := r.Interface
	if intf == nil {
		return nil, fmt.Errorf("invalid interface passed while create ovs endpoint")
	}
	n, ok := d.networks[networkID]
	if !ok {
		return nil, fmt.Errorf("ovs network with id %s not found", networkID)
	}
	_, addr, _ := net.ParseCIDR(intf.Address)
	mac, _ := net.ParseMAC(intf.MacAddress)
	intfName, err := GenerateIfaceName(intfPrefix, intfLen)
	if err != nil {
		return nil, fmt.Errorf("ovs generate interface name err=%s", err)
	}
	ep := &endpoint{
		id:       endpointID,
		nid:      networkID,
		intfName: intfName,
		addr:     addr,
		mac:      mac,
	}
	if ep.addr == nil {
		return nil, fmt.Errorf("create endpoint was not passed interface IP address")
	}

	if s := n.getSubnetforIP(ep.addr); s == nil {
		return nil, fmt.Errorf("no matching subnet for IP %q in network %q", ep.addr, ep.nid)
	}

	if ep.mac == nil {
		ep.mac = GenerateRandomMAC()
		intf.MacAddress = ep.mac.String()
	}

	portType := internalPort
	ovsPortName := intfName
	if useVeth {
		portType = vethPort
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
		// Create a Veth pair
		err = CreateVethPair(intfName, ovsPortName)
		if err != nil {
			logrus.Errorf("Error creating veth pairs. Err: %v", err)
			return nil, err
		}
	}

	logrus.Debugf("ovs create endpoint with addr=%s,mac=%s,intfName=%s,vlan=%d,brust=%d,bandwidth=%d,err=%s", ep.addr.String(), ep.mac.String(), ovsPortName, n.vlan, n.brust, n.bandwidth, err)
	err = d.ovsdb.AddPort(ovsPortName, portType, n.vlan, n.brust, n.bandwidth)
	if err != nil {
		return nil, fmt.Errorf("ovs create endpoint error with addr=%s,mac=%s,intfName=%s,vlan=%d,brust=%d,bandwidth=%d,err=%s", ep.addr.String(), ep.mac.String(), ovsPortName, n.vlan, n.brust, n.bandwidth, err)
	}
	n.Lock()
	n.endpoints[ep.id] = ep
	n.Unlock()
	epResponse := &pluginNet.CreateEndpointResponse{Interface: &pluginNet.EndpointInterface{"", "", intf.MacAddress}}
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
	ovsPortName := intfName
	if useVeth {
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
		if err := DeleteVethPair(intfName, ovsPortName); err != nil {
			return fmt.Errorf("delete veth pair failed with InterfaceName=%s,peer=%s,err=%s", intfName, ovsPortName, err)
		}
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
		return nil, fmt.Errorf("invalid network id")
	}
	if eid == "" {
		return nil, fmt.Errorf("invalid endpoint id")
	}
	n := d.networks[nid]
	if n == nil {
		return nil, fmt.Errorf("network id %q not found", nid)
	}
	ep := n.endpoints[eid]
	if ep == nil {
		return nil, fmt.Errorf("endpoint id %q not found", eid)
	}
	intfName := ep.intfName
	if intfName == "" {
		return nil, fmt.Errorf("intfName %q empty", intfName)
	}
	s := n.getSubnetforIP(ep.addr)
	if s == nil {
		return nil, fmt.Errorf("could not find subnet for endpoint %s", eid)
	}
	ovsPortName := intfName
	if useVeth {
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
	}
	// Set the OVS side of the port as up
	err := SetLinkUp(ovsPortName)
	if err != nil {
		logrus.Errorf("Error setting link %s up. Err: %v", ovsPortName, err)
		return nil, err
	}

	res := &pluginNet.JoinResponse{
		InterfaceName: pluginNet.InterfaceName{
			SrcName:   intfName,
			DstPrefix: containerEthName,
		},
		Gateway: n.gateway,
	}
	logrus.Debugf("Join ovs with port=%s,ip=%s and mac=%s", ovsPortName, ep.addr.String(), ep.mac.String())
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
	if intfName == "" {
		return fmt.Errorf("intfName %q empty", intfName)
	}
	ovsPortName := intfName
	if useVeth {
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
	}
	err := d.ovsdb.DelPort(ovsPortName)
	if err != nil {
		return fmt.Errorf("ovs delete endpoint failed with InterfaceName=%s,err=%s", intfName, err)
	}

	return nil
}

// DiscoverNew ...
func (d *Driver) DiscoverNew(r *pluginNet.DiscoveryNotification) error {
	logrus.Debugf("DiscoverNew ovs")
	return nil
}

// DiscoverDelete ...
func (d *Driver) DiscoverDelete(r *pluginNet.DiscoveryNotification) error {
	logrus.Debugf("DiscoverDelete ovs")
	return nil
}

// ProgramExternalConnectivity ...
func (d *Driver) ProgramExternalConnectivity(r *pluginNet.ProgramExternalConnectivityRequest) error {
	logrus.Debugf("ProgramExternalConnectivity ovs")
	return nil
}

// RevokeExternalConnectivity ...
func (d *Driver) RevokeExternalConnectivity(r *pluginNet.RevokeExternalConnectivityRequest) error {
	logrus.Debugf("RevokeExternalConnectivity ovs")
	return nil
}

func getVlan(opts map[string]interface{}) int {
	vlan := 0
	if opts != nil {
		if o, ok := opts[genericOption].(map[string]interface{}); ok {
			v, _ := o[vlanOption].(string)
			vlan, _ = strconv.Atoi(v)
		}
	}
	return vlan
}
func getBandwidth(opts map[string]interface{}) int {
	var bandwidth int
	if opts != nil {
		if o, ok := opts[genericOption].(map[string]interface{}); ok {
			b, _ := o[bandwidthOption].(string)
			bandwidth, _ = strconv.Atoi(b)
		}
	}
	return bandwidth
}
func getBrust(opts map[string]interface{}) int {
	var brust int
	if opts != nil {
		if o, ok := opts[genericOption].(map[string]interface{}); ok {
			b, _ := o[brustOption].(string)
			brust, _ = strconv.Atoi(b)
		}
	}
	return brust
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

func getGatewayIP(ipv4 []*pluginNet.IPAMData) (string, string, error) {
	var gatewayIP string

	if len(ipv4) > 0 {
		if ipv4[0] != nil {
			if ipv4[0].Gateway != "" {
				gatewayIP = ipv4[0].Gateway
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

// getOvsPortName returns OVS port name depending on if we use Veth pairs
func getOvsPortName(intfName string) string {
	ovsPortName := strings.Replace(intfName, "port", "vport", 1)
	return ovsPortName
}
