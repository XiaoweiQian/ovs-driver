package drivers

import "testing"

func initOvsdbDriver(t *testing.T) *OvsdbDriver {
	d, err := NewOvsdbDriver("ovsbr")
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
	err := d.AddPort("", "port1", 10, 100, 1000)
	if err != nil {
		t.Fatalf("AddPort failed. Error: %s", err)
	}

	err = d.DelPort("port1")
	if err != nil {
		t.Fatalf("DelPort failed. Error: %s", err)
	}
	//defer func() { d.ovsClient.Disconnect }()
}
