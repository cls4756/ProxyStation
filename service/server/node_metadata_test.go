package server

import (
	"testing"

	"github.com/ProxyStation/proxystation/db/configure"
)

func TestNodeMetadataMissingFields(t *testing.T) {
	score := 80
	tests := []struct {
		name   string
		server configure.ServerRaw
		want   bool
	}{
		{name: "all present", server: configure.ServerRaw{IPType: "idc", IPGeoCountry: "美国", IPScore: &score}},
		{name: "missing type", server: configure.ServerRaw{IPGeoCountry: "美国", IPScore: &score}, want: true},
		{name: "missing geo", server: configure.ServerRaw{IPType: "idc", IPScore: &score}, want: true},
		{name: "missing score", server: configure.ServerRaw{IPType: "idc", IPGeoCountry: "美国"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := needsNodeMetadata(&test.server); got != test.want {
				t.Fatalf("needsNodeMetadata() = %v, want %v", got, test.want)
			}
		})
	}
}
