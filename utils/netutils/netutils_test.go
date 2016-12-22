package netutils

import (
	"testing"

	"github.com/XiaoweiQian/ovs-driver/utils/assert"
)

func TestGenerateRandomMAC(t *testing.T) {
	mac1 := GenerateRandomMAC()
	mac2 := GenerateRandomMAC()
	assert.NotEqual(t, mac1.String(), mac2.String())
}
