package drivers

import "testing"
import netlink "github.com/vishvananda/netlink"

func initOvsdbDriver(t *testing.T) *OvsdbDriver {
	d, err := NewOvsdbDriver("ovs-br0")
	if err != nil {
		t.Fatalf("driver init failed. Error: %s", err)
	}

	return d
}

func TestNewOvsdbDriver(t *testing.T) {
	initOvsdbDriver(t)
	//defer func() { d.ovsClient.Disconnect }()
}

func TestAddlPort(t *testing.T) {
	d := initOvsdbDriver(t)
	ovsPortName := "port1"
	ovsPortType := ""
	err := d.AddPort(ovsPortName, ovsPortType, 10, 100, 1000)
	if err != nil {
		t.Fatalf("AddPort failed. Error: %s", err)
	}
	_, err = netlink.LinkByName(ovsPortName)
	if err != nil {
		t.Fatalf("AddPort failed. Error: %s", err)
	}

	err = d.DelPort(ovsPortName)
	if err != nil {
		t.Fatalf("DelPort failed. Error: %s", err)
	}

	_, err = netlink.LinkByName(ovsPortName)
	if err == nil {
		t.Fatalf("DelPort failed. Error: %s", err)
	}
	//defer func() { d.ovsClient.Disconnect }()
}
