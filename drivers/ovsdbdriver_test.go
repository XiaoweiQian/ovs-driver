package drivers

import (
	"testing"
	"time"

	"github.com/docker/docker/pkg/testutil/assert"
	netlink "github.com/vishvananda/netlink"
)

func initOvsdbDriver(t *testing.T) *OvsdbDriver {
	d, err := NewOvsdbDriver("ovs-br0")
	assert.NilError(t, err)
	return d
}

func TestNewOvsdbDriver(t *testing.T) {
	initOvsdbDriver(t)
	//defer func() { d.ovsClient.Disconnect }()
}

func TestAddlPort(t *testing.T) {
	d := initOvsdbDriver(t)
	ovsPortName := "port1"
	ovsPortType := "internal"
	err := d.AddPort(ovsPortName, ovsPortType, 10, 100, 1000)
	assert.NilError(t, err)

	// Wait a little for OVS to create the interface
	time.Sleep(300 * time.Millisecond)
	_, err = netlink.LinkByName(ovsPortName)
	assert.NilError(t, err)

	err = d.DelPort(ovsPortName)
	assert.NilError(t, err)

	// Wait a little for OVS to create the interface
	time.Sleep(300 * time.Millisecond)
	_, err = netlink.LinkByName(ovsPortName)
	assert.NotNil(t, err)
	//defer func() { d.ovsClient.Disconnect }()
}
