package drivers

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/XiaoweiQian/ovs-driver/utils/netutils"
	pluginNet "github.com/docker/go-plugins-helpers/network"
	"github.com/docker/libkv/store/boltdb"
	"github.com/docker/libnetwork/datastore"
	docker "github.com/fsouza/go-dockerclient"
)

const (
	swarmEndpoint    = "http://localhost:6732"
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

//Driver aa
type Driver struct {
	id         string
	ovsdb      *OvsdbDriver
	networks   networkTable
	localStore datastore.DataStore
	client     *docker.Client
	sync.Mutex
}

type subnet struct {
	subnetIP *net.IPNet
	gwIP     *net.IPNet
}

type network struct {
	id        string
	vlan      int
	bandwidth int
	brust     int
	driver    *Driver
	endpoints endpointTable
	subnets   []*subnet
	sync.Mutex
}

// Init ...
func Init() (*Driver, error) {
	// initiate the OvsdbDriver
	ovsdb, err := NewOvsdbDriver(ovsBridgeName)
	// initiate the boltdb
	boltdb.Register()
	if err != nil {
		return nil, fmt.Errorf("ovsdb driver init failed. Error: %s", err)
	}

	if ovsdb == nil {
		return nil, fmt.Errorf("could not connect to open vswitch")
	}

	client, err := docker.NewClient(swarmEndpoint)
	if err != nil {
		return nil, fmt.Errorf("could not connect to swarm. Error: %s", err)
	}

	store, err := datastore.NewDataStore(datastore.LocalScope, nil)
	if err != nil {
		return nil, fmt.Errorf("could not init ovs local store. Error: %s", err)
	}

	d := &Driver{
		ovsdb:      ovsdb,
		networks:   networkTable{},
		localStore: store,
		client:     client,
	}
	if err := d.restoreEndpoints(); err != nil {
		logrus.Debugf("Failure during ovs endpoints restore: %v", err)
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
	n := &network{
		id:        id,
		driver:    d,
		endpoints: endpointTable{},
		subnets:   []*subnet{},
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
	for _, ep := range n.endpoints {
		if err := d.deleteEndpoint(n, ep); err != nil {
			return err
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
	// Wait a little for OVS to create the interface
	time.Sleep(300 * time.Millisecond)
	// Set the OVS side of the port as up
	err := netutils.SetLinkUp(ovsPortName)
	if err != nil {
		logrus.Errorf("Error setting link %s up. Err: %v", ovsPortName, err)
		return nil, err
	}

	res := &pluginNet.JoinResponse{
		InterfaceName: pluginNet.InterfaceName{
			SrcName:   intfName,
			DstPrefix: containerEthName,
		},
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

// Endpoints are stored in the local store. Restore them and reconstruct
func (d *Driver) restoreEndpoints() error {
	if d.localStore == nil {
		logrus.Debugf("Cannot restore ovs endpoints because local datastore is missing.")
		return nil
	}
	logrus.Debugf("Restore ovs endpoints from local datastore.")

	kvol, err := d.localStore.List(datastore.Key(ovsEndpointPrefix), &endpoint{})
	if err != nil && err != datastore.ErrKeyNotFound {
		return fmt.Errorf("failed to read ovs endpoint from store: %v", err)
	}

	if err == datastore.ErrKeyNotFound {
		logrus.Debugf("Restore endpoint,But key not found.key=%s ", ovsEndpointPrefix)
		return nil
	}
	var ovsPortName string
	for _, kvo := range kvol {
		ep := kvo.(*endpoint)
		n := d.network(ep.nid)
		if n == nil {
			logrus.Debugf("Network (%s) not found for restored endpoint (%s)", ep.nid[0:7], ep.id[0:7])
			logrus.Debugf("Deleting stale ovs endpoint (%s) from store", ep.id[0:7])
			if err := d.deleteEndpointFromStore(ep); err != nil {
				logrus.Debugf("Failed to delete stale ovs endpoint (%s) from store", ep.id[0:7])
			}
			ovsPortName = ep.intfName
			if useVeth {
				// Get OVS port name
				ovsPortName = getOvsPortName(ep.intfName)
				if err := netutils.DeleteVethPair(ep.intfName, ovsPortName); err != nil {
					return fmt.Errorf("delete veth pair failed with InterfaceName=%s,peer=%s,err=%s", ep.intfName, ovsPortName, err)
				}
			}
			if err := d.ovsdb.DelPort(ovsPortName); err != nil {
				return fmt.Errorf("ovs could not delete port with name %s", ovsPortName)
			}

			continue
		}
		n.Lock()
		epJSON, _ := ep.MarshalJSON()
		logrus.Debugf("Success restore endpoint=%s from local store ", epJSON)
		n.endpoints[ep.id] = ep
		n.Unlock()
	}
	return nil
}

func (d *Driver) network(nid string) *network {
	d.Lock()
	n, ok := d.networks[nid]
	d.Unlock()
	if !ok {
		n = d.getNetworkFromSwarm(nid)
		if n != nil {
			d.Lock()
			d.networks[nid] = n
			d.Unlock()
		}
	}

	return n
}

func (d *Driver) getNetworkFromSwarm(nid string) *network {
	if d.client == nil {
		return nil
	}
	nw, err := d.client.NetworkInfo(nid)
	logrus.Debugf("Network (%s)  found from swarm", nw)
	if err != nil {
		return nil
	}
	opts := nw.Options
	subnets := nw.IPAM.Config
	vlan, _ := strconv.Atoi(opts[vlanOption])
	brust, _ := strconv.Atoi(opts[brustOption])
	bandwidth, _ := strconv.Atoi(opts[bandwidthOption])
	n := &network{
		id:        nid,
		driver:    d,
		endpoints: endpointTable{},
		vlan:      vlan,
		brust:     brust,
		bandwidth: bandwidth,
		subnets:   []*subnet{},
	}
	var pool, gw *net.IPNet
	for _, ipd := range subnets {
		_, pool, _ = net.ParseCIDR(ipd.IPRange)
		_, gw, _ = net.ParseCIDR(ipd.Gateway)
		s := &subnet{
			subnetIP: pool,
			gwIP:     gw,
		}
		n.subnets = append(n.subnets, s)
	}

	logrus.Debugf("restore Network (%s) from swarm", n)
	return n
}
