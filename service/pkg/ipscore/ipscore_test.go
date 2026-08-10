package ipscore

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	c := NewClient(srv.Client())
	// 内置服务商的地址是写死的，测试从这里注入而不是走用户配置的 Endpoint
	c.cleanIPEndpoint = srv.URL
	return c, srv
}

const cleanIPBody = `{"ok":true,"geo":{"country":"澳大利亚","country_code":"AU","region":"昆士兰州","city":"布里斯班"},"purity":{"score":82,"grade":"B"},
	"risk":{"risk_label":"Clean","risk_level":"Clean"}}`

func TestLookupCleanIPParsesScore(t *testing.T) {
	var gotQuery string
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, cleanIPBody)
	})
	defer srv.Close()

	res := c.Lookup("8.8.8.8", Options{Provider: ProviderCleanIP, MinInterval: time.Millisecond})
	if res.Err != nil {
		t.Fatalf("查询失败：%v", res.Err)
	}
	if res.Score != 82 || res.Grade != "B" || res.RiskLevel != "Clean" {
		t.Errorf("解析结果不符：%+v", res)
	}
	if res.Country != "澳大利亚" || res.CountryCode != "AU" || res.Region != "昆士兰州" || res.City != "布里斯班" {
		t.Errorf("地理信息解析不符：%+v", res)
	}
	// json=1 是 CleanIP 返回结构化数据的开关，漏了会拿到终端着色的文本
	if gotQuery != "ip=8.8.8.8&json=1" {
		t.Errorf("请求参数不符：%q", gotQuery)
	}
}

// 自定义接口把 IP 拼在路径上时，用 {ip} 占位符指定位置。
func TestLookupCustomSubstitutesPlaceholder(t *testing.T) {
	var gotPath string
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"trust_score":66,"intelligence":{"abuser_level":"low"}}`)
	})
	defer srv.Close()

	res := c.Lookup("1.1.1.1", Options{Provider: ProviderCustom, Endpoint: srv.URL + "/lookup/{ip}", MinInterval: time.Millisecond})
	if res.Err != nil {
		t.Fatalf("查询失败：%v", res.Err)
	}
	if res.Score != 66 || res.RiskLevel != "low" {
		t.Errorf("解析结果不符：%+v", res)
	}
	if gotPath != "/lookup/1.1.1.1" {
		t.Errorf("占位符应被替换成 IP，实际路径 %q", gotPath)
	}
}

// 没写占位符时按查询参数拼，兼容 ?ip= 风格的接口。
func TestLookupCustomFallsBackToQueryParam(t *testing.T) {
	var gotQuery string
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("ip")
		fmt.Fprint(w, `{"score":42}`)
	})
	defer srv.Close()

	res := c.Lookup("8.8.8.8", Options{Provider: ProviderCustom, Endpoint: srv.URL + "/api", MinInterval: time.Millisecond})
	if res.Err != nil {
		t.Fatalf("查询失败：%v", res.Err)
	}
	if res.Score != 42 {
		t.Errorf("应取到 score=42，实际 %+v", res)
	}
	if gotQuery != "8.8.8.8" {
		t.Errorf("未写占位符时应拼 ?ip=，实际 %q", gotQuery)
	}
}

// 评分字段藏在嵌套对象里也要能取到，这样一个 URL 就能适配不同响应结构。
func TestLookupCustomFindsNestedScore(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"data":{"purity":{"score":88,"grade":"A"},"geo":{"country_name":"Japan","country_code":"jp","region_name":"Tokyo","city":"Chiyoda"}}}`)
	})
	defer srv.Close()

	res := c.Lookup("1.1.1.1", Options{Provider: ProviderCustom, Endpoint: srv.URL, MinInterval: time.Millisecond})
	if res.Err != nil {
		t.Fatalf("查询失败：%v", res.Err)
	}
	if res.Score != 88 || res.Grade != "A" {
		t.Errorf("应从嵌套结构里取到 88/A，实际 %+v", res)
	}
	if res.Country != "Japan" || res.CountryCode != "JP" || res.Region != "Tokyo" || res.City != "Chiyoda" {
		t.Errorf("自定义接口地理字段解析不符：%+v", res)
	}
}

// 字符串形式的数字也接受，有些接口会把分值序列化成字符串。
func TestLookupCustomAcceptsStringScore(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"score":"73"}`)
	})
	defer srv.Close()

	res := c.Lookup("1.1.1.1", Options{Provider: ProviderCustom, Endpoint: srv.URL, MinInterval: time.Millisecond})
	if res.Err != nil || res.Score != 73 {
		t.Errorf("字符串分值应被解析成 73，实际 %+v", res)
	}
}

// 评分缺失时不能当成 0 分落库，否则界面会显示"最差"而不是"未检测"。
func TestLookupCustomMissingScoreIsError(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"intelligence":{"abuser_level":"low"}}`)
	})
	defer srv.Close()

	res := c.Lookup("1.1.1.1", Options{Provider: ProviderCustom, Endpoint: srv.URL, MinInterval: time.Millisecond})
	if res.Err == nil {
		t.Fatal("响应里没有评分时应报错")
	}
	if res.Score != -1 {
		t.Errorf("失败时评分应为 -1，实际 %d", res.Score)
	}
}

// 自定义服务商没填地址时直接失败，不能默默去打 cleanip 的接口。
func TestLookupCustomWithoutEndpointFails(t *testing.T) {
	c := NewClient(nil)
	res := c.Lookup("1.1.1.1", Options{Provider: ProviderCustom, MinInterval: time.Millisecond})
	if res.Err == nil || res.Score != -1 {
		t.Errorf("缺少地址时应报错且评分为 -1，实际 %+v", res)
	}
}

// 用户填的自定义地址只属于自定义服务商。若内置服务商也拿它当接口地址，
// 界面上"切到 cleanip 再切回来还记得自定义地址"就会变成拿自定义地址去打 cleanip。
func TestLookupCleanIPIgnoresUserEndpoint(t *testing.T) {
	var hit bool
	rogue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		fmt.Fprint(w, cleanIPBody)
	}))
	defer rogue.Close()

	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, cleanIPBody)
	})
	defer srv.Close()

	res := c.Lookup("8.8.8.8", Options{Provider: ProviderCleanIP, Endpoint: rogue.URL, MinInterval: time.Millisecond})
	if res.Err != nil {
		t.Fatalf("查询失败：%v", res.Err)
	}
	if hit {
		t.Error("内置服务商不应使用用户配置的自定义地址")
	}
}

// 这是本次需求的重点：批量检测必须被限速，不能对第三方免费服务发洪水请求。
func TestLookupRespectsMinInterval(t *testing.T) {
	var hits int32
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		fmt.Fprint(w, cleanIPBody)
	})
	defer srv.Close()

	const interval = 120 * time.Millisecond
	start := time.Now()
	for i := 0; i < 3; i++ {
		c.Lookup(fmt.Sprintf("9.9.9.%d", i), Options{Provider: ProviderCleanIP, MinInterval: interval})
	}
	elapsed := time.Since(start)

	if hits != 3 {
		t.Fatalf("应发出 3 次请求，实际 %d", hits)
	}
	// 首次不等待，后两次各等一个间隔
	if min := 2 * interval; elapsed < min {
		t.Errorf("3 次查询至少应耗时 %v，实际 %v —— 限速没生效", min, elapsed)
	}
}

// 并发调用同一个 Client 时限速依然要成立，否则"全选检测"会瞬间打满。
func TestLookupSerializesConcurrentCallers(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, cleanIPBody)
	})
	defer srv.Close()

	const interval = 80 * time.Millisecond
	start := time.Now()
	done := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		go func(i int) {
			c.Lookup(fmt.Sprintf("7.7.7.%d", i), Options{Provider: ProviderCleanIP, MinInterval: interval})
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if min := 3 * interval; time.Since(start) < min {
		t.Errorf("4 个并发调用至少应耗时 %v，实际 %v", min, time.Since(start))
	}
}

func TestLookupSurfacesRateLimit(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	res := c.Lookup("8.8.8.8", Options{Provider: ProviderCleanIP, MinInterval: time.Millisecond})
	if res.Err == nil {
		t.Fatal("HTTP 429 应报错")
	}
	// 用户需要知道是被限流了，才好去调大间隔
	if got := res.Err.Error(); got == "" || !contains(got, "限流") {
		t.Errorf("错误信息应说明是限流，实际 %q", got)
	}
}

func TestLookupRejectsUnknownProvider(t *testing.T) {
	c := NewClient(nil)
	res := c.Lookup("8.8.8.8", Options{Provider: "bogus", MinInterval: time.Millisecond})
	if res.Err == nil {
		t.Fatal("未知服务商应报错")
	}
}

func TestValidateProviderNormalizes(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "cleanip"}, {"CleanIP", "cleanip"}, {" custom ", "custom"},
	} {
		got, err := ValidateProvider(tc.in)
		if err != nil {
			t.Errorf("in=%q 不应报错：%v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("in=%q 应归一为 %q，实际 %q", tc.in, tc.want, got)
		}
	}
	if _, err := ValidateProvider("nope"); err == nil {
		t.Error("非法服务商应报错")
	}
	// net.coffee 已从内置列表移除，只能通过自定义地址使用
	if _, err := ValidateProvider("netcoffee"); err == nil {
		t.Error("netcoffee 不应再是内置服务商")
	}
}

// 自定义服务商必须填地址，但功能未启用时放宽，允许先选服务商稍后再补。
func TestValidateProviderEndpointRequiresCustomURL(t *testing.T) {
	if _, _, err := ValidateProviderEndpoint("custom", "", true); err == nil {
		t.Error("启用时自定义服务商缺地址应报错")
	}
	if _, _, err := ValidateProviderEndpoint("custom", "", false); err != nil {
		t.Errorf("未启用时不应强制要求地址：%v", err)
	}
	if _, ep, err := ValidateProviderEndpoint("custom", "https://ip.net.coffee/api/ip/lookup/{ip}", true); err != nil || ep == "" {
		t.Errorf("带占位符的合法地址应通过，实际 %q / %v", ep, err)
	}
}

func TestValidateEndpoint(t *testing.T) {
	if got, err := ValidateEndpoint("  "); err != nil || got != "" {
		t.Errorf("空值表示用默认地址，实际 %q / %v", got, err)
	}
	if _, err := ValidateEndpoint("ftp://x.com"); err == nil {
		t.Error("非 http(s) 协议应报错")
	}
	if _, err := ValidateEndpoint("notaurl"); err == nil {
		t.Error("缺少域名应报错")
	}
	if got, err := ValidateEndpoint("https://cleanip.io/cli"); err != nil || got != "https://cleanip.io/cli" {
		t.Errorf("合法地址应原样保留，实际 %q / %v", got, err)
	}
}

// 评分超出 0-100 时钳住，免得界面上出现负数或 3 位数的进度条
func TestClampScore(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-5, 0}, {0, 0}, {50, 50}, {100, 100}, {130, 100}} {
		if got := clampScore(tc.in); got != tc.want {
			t.Errorf("clampScore(%d) = %d，应为 %d", tc.in, got, tc.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
