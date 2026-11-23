package configure

import "testing"

// 列式存储下搜索仍按 name/host/type 三个字段匹配，含 JSON 特殊字符也不应漏掉。
func TestSearchServersMatchesNameHostType(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	servers := []ServerRaw{
		{Name: "A&B 香港", Host: "h1.example", Port: 1, Type: "vless"},
		{Name: "plain node", Host: "h2.example", Port: 2, Type: "vmess"},
	}
	if err := SetServers(servers); err != nil {
		t.Fatalf("set servers: %v", err)
	}

	cases := []struct {
		query string
		want  int
	}{
		{"a&b", 1},
		{"plain", 1},
		{"香港", 1},
		{"h2", 1},
		{"vmess", 1},
		{"missing", 0},
	}
	for _, tc := range cases {
		_, total, err := SearchServers(ServerFilter{Query: tc.query}, "manual", 10, 0)
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		if total != tc.want {
			t.Errorf("search %q total = %d, want %d", tc.query, total, tc.want)
		}
	}
}

// 列过滤应覆盖各种精确/范围条件。
func TestSearchServersColumnFilters(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	maxLatency := 200
	minScore := 70
	score := 90
	servers := []ServerRaw{
		{Name: "a", Host: "a.example", Port: 1, Type: "vless", IPType: "residential",
			LastProbeTime: 1, Latency: 120, LastFastProbeTime: 1, FastLatency: 100, IPScore: &score},
		{Name: "b", Host: "b.example", Port: 2, Type: "vmess", IPType: "idc",
			LastProbeTime: 1, Latency: 500},
		{Name: "c", Host: "c.example", Port: 3, Type: "vless"},
		{Name: "d", Host: "d.example", Port: 4, Type: "vless", LastProbeTime: 1, Latency: -1},
	}
	if err := SetServers(servers); err != nil {
		t.Fatalf("set servers: %v", err)
	}

	check := func(name string, f ServerFilter, want int) {
		t.Helper()
		_, total, err := SearchServers(f, "manual", 10, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total != want {
			t.Errorf("%s total = %d, want %d", name, total, want)
		}
	}
	check("type", ServerFilter{Type: "vless"}, 3)
	check("ipType", ServerFilter{IPType: "residential"}, 1)
	check("ipType unchecked", ServerFilter{IPType: "unchecked"}, 2)
	check("probe reachable", ServerFilter{Probe: "reachable"}, 2)
	check("probe failed", ServerFilter{Probe: "failed"}, 1)
	check("probe unchecked", ServerFilter{Probe: "unchecked"}, 1)
	check("fastProbe reachable", ServerFilter{FastProbe: "reachable"}, 1)
	check("maxLatency", ServerFilter{MaxLatency: &maxLatency}, 1)
	check("minScore", ServerFilter{MinScore: &minScore}, 1)
	check("combined", ServerFilter{Type: "vless", Probe: "reachable", MaxLatency: &maxLatency}, 1)
}
