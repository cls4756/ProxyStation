package dnsupstream

import "testing"

func TestParseDefaults(t *testing.T) {
	spec, err := Parse("")
	if err != nil {
		t.Fatalf("Parse empty: %v", err)
	}
	if spec.Kind != KindHTTPS || spec.Host != "8.8.8.8" || spec.Port != 443 || spec.Path != DefaultDoHPath {
		t.Fatalf("unexpected default spec: %+v", spec)
	}
}

func TestParseCases(t *testing.T) {
	cases := []struct {
		raw  string
		want Spec
	}{
		{"https://8.8.8.8/dns-query", Spec{KindHTTPS, "8.8.8.8", 443, "/dns-query"}},
		{"8.8.8.8", Spec{KindHTTPS, "8.8.8.8", 443, "/dns-query"}},
		{"https://dns.google/dns-query", Spec{KindHTTPS, "dns.google", 443, "/dns-query"}},
		{"https://dns.google", Spec{KindHTTPS, "dns.google", 443, "/dns-query"}},
		{"https://DNS.Google/dns-query", Spec{KindHTTPS, "dns.google", 443, "/dns-query"}},
		{"https://1.1.1.1:8443/resolve", Spec{KindHTTPS, "1.1.1.1", 8443, "/resolve"}},
		{"tls://1.1.1.1", Spec{KindTLS, "1.1.1.1", 853, ""}},
		{"tls://dns.google:8853", Spec{KindTLS, "dns.google", 8853, ""}},
		{"udp://223.5.5.5", Spec{KindUDP, "223.5.5.5", 53, ""}},
		{"udp://223.5.5.5:5353", Spec{KindUDP, "223.5.5.5", 5353, ""}},
		{"[2001:4860:4860::8888]", Spec{KindHTTPS, "2001:4860:4860::8888", 443, "/dns-query"}},
		{"  tls://1.0.0.1  ", Spec{KindTLS, "1.0.0.1", 853, ""}},
	}
	for _, tc := range cases {
		got, err := Parse(tc.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("Parse(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	bad := []string{
		"ftp://8.8.8.8",
		"https://",
		"https://8.8.8.8:0/dns-query",
		"https://8.8.8.8:70000/dns-query",
		"tls://8.8.8.8/dns-query",
		"udp://8.8.8.8/dns-query",
		"https://user:pass@8.8.8.8/dns-query",
		"https://8.8.8.8/dns-query?x=1",
	}
	for _, raw := range bad {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) 期望报错，实际通过", raw)
		}
	}
}

func TestNormalizeAndURL(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"   ":                              "",
		"8.8.8.8":                          "https://8.8.8.8/dns-query",
		"https://8.8.8.8":                  "https://8.8.8.8/dns-query",
		"https://dns.google:443/dns-query": "https://dns.google/dns-query",
		"tls://1.1.1.1:853":                "tls://1.1.1.1",
		"tls://1.1.1.1":                    "tls://1.1.1.1",
		"udp://223.5.5.5":                  "udp://223.5.5.5",
	}
	for raw, want := range cases {
		got, err := Normalize(raw)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("Normalize(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, err := Normalize("http://8.8.8.8"); err == nil {
		t.Fatal("Normalize(http://8.8.8.8) 期望报错")
	}
}
