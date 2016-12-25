package drivers

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/Sirupsen/logrus"
	"github.com/XiaoweiQian/ovs-driver/utils/netutils"
	pluginNet "github.com/docker/go-plugins-helpers/network"
	"github.com/docker/libnetwork/datastore"
)

type endpointTable map[string]*endpoint
type endpoint struct {
	id       string
	nid      string
	intfName string
	mac      net.HardwareAddr
	addr     *net.IPNet
	dbExists bool
	dbIndex  uint64
}

const ovsEndpointPrefix = "ovs/endpoint"

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
	intfName, err := netutils.GenerateIfaceName(intfPrefix, intfLen)
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
		ep.mac = netutils.GenerateRandomMAC()
		intf.MacAddress = ep.mac.String()
	}

	portType := internalPort
	ovsPortName := intfName
	if useVeth {
		portType = vethPort
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
		// Create a Veth pair
		err = netutils.CreateVethPair(intfName, ovsPortName)
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

	if err := d.writeEndpointToStore(ep); err != nil {
		return nil, fmt.Errorf("failed to update ovs endpoint %s to local store: %v", ep.id[0:7], err)
	}
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
	if err := d.deleteEndpoint(n, ep); err != nil {
		return err
	}

	return nil
}

func (d *Driver) deleteEndpoint(n *network, ep *endpoint) error {
	intfName := ep.intfName
	if intfName == "" {
		return nil
	}
	ovsPortName := intfName
	if useVeth {
		// Get OVS port name
		ovsPortName = getOvsPortName(intfName)
		if err := netutils.DeleteVethPair(intfName, ovsPortName); err != nil {
			return fmt.Errorf("delete veth pair failed with InterfaceName=%s,peer=%s,err=%s", intfName, ovsPortName, err)
		}
	}
	n.Lock()
	delete(n.endpoints, ep.id)
	n.Unlock()

	if err := d.deleteEndpointFromStore(ep); err != nil {
		logrus.Debugf("Failed to delete ovs endpoint %s from local store: %v", ep.id[0:7], err)
	}
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

func (d *Driver) writeEndpointToStore(e *endpoint) error {
	if d.localStore == nil {
		return fmt.Errorf("ovs local store not initialized, ep not added")
	}

	if err := d.localStore.PutObjectAtomic(e); err != nil {
		return err
	}
	return nil
}

func (d *Driver) deleteEndpointFromStore(e *endpoint) error {
	if d.localStore == nil {
		return fmt.Errorf("ovs local store not initialized, ep not deleted")
	}

	if err := d.localStore.DeleteObjectAtomic(e); err != nil {
		return err
	}

	return nil
}

func (ep *endpoint) New() datastore.KVObject {
	return &endpoint{}
}

func (ep *endpoint) CopyTo(o datastore.KVObject) error {
	dstep := o.(*endpoint)
	*dstep = *ep
	return nil
}

func (ep *endpoint) DataScope() string {
	return datastore.LocalScope
}

func (ep *endpoint) Key() []string {
	return []string{ovsEndpointPrefix, ep.id}
}

func (ep *endpoint) KeyPrefix() []string {
	return []string{ovsEndpointPrefix}
}

func (ep *endpoint) Index() uint64 {
	return ep.dbIndex
}

func (ep *endpoint) SetIndex(index uint64) {
	ep.dbIndex = index
	ep.dbExists = true
}

func (ep *endpoint) Exists() bool {
	return ep.dbExists
}

func (ep *endpoint) Skip() bool {
	return false
}

func (ep *endpoint) Value() []byte {
	b, err := json.Marshal(ep)
	if err != nil {
		return nil
	}
	return b
}

func (ep *endpoint) SetValue(value []byte) error {
	return json.Unmarshal(value, ep)
}

func (ep *endpoint) MarshalJSON() ([]byte, error) {
	epMap := make(map[string]interface{})

	epMap["id"] = ep.id
	epMap["nid"] = ep.nid
	if ep.intfName != "" {
		epMap["intfName"] = ep.intfName
	}
	if ep.addr != nil {
		epMap["addr"] = ep.addr.String()
	}
	if len(ep.mac) != 0 {
		epMap["mac"] = ep.mac.String()
	}

	return json.Marshal(epMap)
}

func (ep *endpoint) UnmarshalJSON(value []byte) error {
	var (
		err   error
		epMap map[string]interface{}
	)

	json.Unmarshal(value, &epMap)

	ep.id = epMap["id"].(string)
	ep.nid = epMap["nid"].(string)
	if v, ok := epMap["mac"]; ok {
		if ep.mac, err = net.ParseMAC(v.(string)); err != nil {
			return fmt.Errorf("failed to decode endpoint interface mac address after json unmarshal: %s", v.(string))
		}
	}
	if v, ok := epMap["addr"]; ok {
		if _, ep.addr, err = net.ParseCIDR(v.(string)); err != nil {
			return fmt.Errorf("failed to decode endpoint interface ipv4 address after json unmarshal: %v", err)
		}
	}
	if v, ok := epMap["intfName"]; ok {
		ep.intfName = v.(string)
	}

	return nil
}
