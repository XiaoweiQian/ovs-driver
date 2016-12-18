package drivers

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/Sirupsen/logrus"
	"github.com/socketplane/libovsdb"
)

const (
	ovsDataBase  = "Open_vSwitch"
	socketFile   = "/var/run/openvswitch/db.sock"
	bridgeName   = "ovsbr"
	portTable    = "Port"
	intfTable    = "Interface"
	bridgeTable  = "Bridge"
	insertOp     = "insert"
	mutateOp     = "mutate"
	deleteOp     = "delete"
	internalPort = "internal"
)

//OvsdbDriver ...
type OvsdbDriver struct {
	bridgeName string
	ovsClient  *libovsdb.OvsdbClient
	cache      map[string]map[libovsdb.UUID]libovsdb.Row
	cacheLock  sync.RWMutex
}

// NewOvsdbDriver ...
func NewOvsdbDriver(bridgeName string) (*OvsdbDriver, error) {
	// Create a new ovsdb driver instance
	d := new(OvsdbDriver)
	d.bridgeName = bridgeName

	// Connect to ovs
	ovsClient, err := libovsdb.ConnectWithUnixSocket(socketFile)
	if err != nil {
		logrus.Fatalf("Error connecting to ovs. Err: %v", err)
		return nil, err
	}

	d.ovsClient = ovsClient

	// Initialize the cache
	d.cache = make(map[string]map[libovsdb.UUID]libovsdb.Row)
	d.ovsClient.Register(d)
	initial, _ := d.ovsClient.MonitorAll(ovsDataBase, "")
	d.populateCache(*initial)

	return d, nil
}

// AddPort create a ovs internal port
func (d *OvsdbDriver) AddPort(endpointID, portName string, tag, burst int, bandwidth int64) error {

	intfUUID := "intf"
	portUUID := "port"

	// insert interface
	intf := make(map[string]interface{})
	intf["name"] = portName
	intf["type"] = internalPort
	if bandwidth != 0 {
		intf["ingress_policing_rate"] = bandwidth
	}
	if burst != 0 {
		intf["ingress_policing_burst"] = burst
	}

	intfOp := libovsdb.Operation{
		Op:       insertOp,
		Table:    intfTable,
		Row:      intf,
		UUIDName: intfUUID,
	}

	// insert port
	port := make(map[string]interface{})
	port["name"] = portName
	port["interfaces"] = libovsdb.UUID{GoUUID: intfUUID}
	if tag != 0 {
		port["vlan_mode"] = "access"
		port["tag"] = tag
	} else {
		port["vlan_mode"] = "trunk"
	}

	portOp := libovsdb.Operation{
		Op:       insertOp,
		Table:    portTable,
		Row:      port,
		UUIDName: portUUID,
	}

	// mutate bridge table
	mutateUUID := []libovsdb.UUID{libovsdb.UUID{GoUUID: portUUID}}
	mutateSet, _ := libovsdb.NewOvsSet(mutateUUID)
	mutation := libovsdb.NewMutation("ports", insertOp, mutateSet)
	condition := libovsdb.NewCondition("name", "==", d.bridgeName)
	mutateOp := libovsdb.Operation{
		Op:        mutateOp,
		Table:     bridgeTable,
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	ops := []libovsdb.Operation{intfOp, portOp, mutateOp}
	return d.doOperations(ops)

}

// DelPort ...
func (d *OvsdbDriver) DelPort(portName string) error {
	portUUID := []libovsdb.UUID{{GoUUID: portName}}
	condition := libovsdb.NewCondition("name", "==", portName)

	//delete interface
	intfOp := libovsdb.Operation{
		Op:    deleteOp,
		Table: intfTable,
		Where: []interface{}{condition},
	}
	//delete port
	condition = libovsdb.NewCondition("name", "==", portName)
	portOp := libovsdb.Operation{
		Op:    deleteOp,
		Table: portTable,
		Where: []interface{}{condition},
	}

	// get from cache
	d.cacheLock.RLock()
	for uuid, row := range d.cache["Port"] {
		name := row.Fields["name"].(string)
		if name == portName {
			portUUID = []libovsdb.UUID{uuid}
			break
		}
	}
	d.cacheLock.RUnlock()

	// mutate the bridge
	mutateSet, _ := libovsdb.NewOvsSet(portUUID)
	mutation := libovsdb.NewMutation("ports", deleteOp, mutateSet)
	condition = libovsdb.NewCondition("name", "==", d.bridgeName)
	mutateOp := libovsdb.Operation{
		Op:        mutateOp,
		Table:     bridgeTable,
		Mutations: []interface{}{mutation},
		Where:     []interface{}{condition},
	}

	// do transaction
	ops := []libovsdb.Operation{intfOp, portOp, mutateOp}
	return d.doOperations(ops)

}

func (d *OvsdbDriver) populateCache(updates libovsdb.TableUpdates) {
	d.cacheLock.Lock()
	defer func() { d.cacheLock.Unlock() }()

	for table, tableUpdate := range updates.Updates {
		if _, ok := d.cache[table]; !ok {
			d.cache[table] = make(map[libovsdb.UUID]libovsdb.Row)
		}
		for uuid, row := range tableUpdate.Rows {
			empty := libovsdb.Row{}
			if !reflect.DeepEqual(row.New, empty) {
				d.cache[table][libovsdb.UUID{GoUUID: uuid}] = row.New
			} else {
				delete(d.cache[table], libovsdb.UUID{GoUUID: uuid})
			}
		}
	}
}

func (d *OvsdbDriver) doOperations(ops []libovsdb.Operation) error {
	reply, _ := d.ovsClient.Transact(ovsDataBase, ops...)
	if len(reply) < len(ops) {
		logrus.Errorf("Unexpected number of replies. Expected: %d, Recvd: %d", len(ops), len(reply))
	}

	for i, o := range reply {
		if o.Error != "" && i < len(ops) {
			return fmt.Errorf("%s(%s)", o.Error, o.Details)
		} else if o.Error != "" {
			return fmt.Errorf("%s(%s)", o.Error, o.Details)
		}
	}

	return nil

}

//Update ...
func (d *OvsdbDriver) Update(context interface{}, tableUpdates libovsdb.TableUpdates) {
	panic("not implemented")
}

//Locked ...
func (d *OvsdbDriver) Locked([]interface{}) {
	panic("not implemented")
}

//Stolen ...
func (d *OvsdbDriver) Stolen([]interface{}) {
	panic("not implemented")
}

//Echo ...
func (d *OvsdbDriver) Echo([]interface{}) {
	panic("not implemented")
}

//Disconnected ...
func (d *OvsdbDriver) Disconnected(*libovsdb.OvsdbClient) {
	panic("not implemented")
}
