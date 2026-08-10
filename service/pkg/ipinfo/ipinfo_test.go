package ipinfo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubTransport 拦截所有出站请求，返回预设响应，避免测试依赖 ip-api.com。
type stubTransport struct {
	// bodies 按调用顺序记录每次请求的 query 列表
	bodies [][]string
	// respond 返回响应体与响应头
	respond func(call int, queries []string) (string, int, http.Header)
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, _ := io.ReadAll(req.Body)
	var payload []map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("请求体不是合法 JSON 数组: %w", err)
	}
	queries := make([]string, 0, len(payload))
	for _, item := range payload {
		queries = append(queries, item["query"])
	}
	call := len(s.bodies)
	s.bodies = append(s.bodies, queries)

	body, status, header := s.respond(call, queries)
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     header,
		Request:    req,
	}, nil
}

func stubClient(t *testing.T, respond func(call int, queries []string) (string, int, http.Header)) (*http.Client, *stubTransport) {
	t.Helper()
	tr := &stubTransport{respond: respond}
	return &http.Client{Transport: tr}, tr
}

func successBody(queries []string, extra string) string {
	var sb strings.Builder
	sb.WriteString("[")
	for i, q := range queries {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"status":"success","query":%q%s}`, q, extra)
	}
	sb.WriteString("]")
	return sb.String()
}

func TestEntryToResultClassification(t *testing.T) {
	cases := []struct {
		name  string
		entry batchEntry
		want  Kind
	}{
		// hosting 是 ip-api 对 IDC 段最直接的信号
		{"hosting", batchEntry{Status: "success", Hosting: true}, KindIDC},
		{"mobile", batchEntry{Status: "success", Mobile: true}, KindMobile},
		// 两者同时为真时按机房算，避免把 IDC 误标成家宽
		{"hosting 优先于 mobile", batchEntry{Status: "success", Hosting: true, Mobile: true}, KindIDC},
		{"residential", batchEntry{Status: "success"}, KindResidential},
		{"查询失败", batchEntry{Status: "fail", Message: "reserved range"}, KindUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := entryToResult("1.2.3.4", c.entry)
			if got.Kind != c.want {
				t.Errorf("Kind = %q, want %q", got.Kind, c.want)
			}
			if c.want == KindUnknown && got.Err == nil {
				t.Error("失败结果应带上错误原因，便于界面提示")
			}
			if c.want != KindUnknown && got.Err != nil {
				t.Errorf("成功结果不应带错误：%v", got.Err)
			}
		})
	}
}

// ISP 缺失时应退回 org / as，否则界面上判定依据会是空白
func TestEntryToResultISPFallback(t *testing.T) {
	got := entryToResult("1.2.3.4", batchEntry{Status: "success", Org: "", AS: "AS13335 Cloudflare"})
	if got.ISP != "AS13335 Cloudflare" {
		t.Errorf("ISP 应回退到 AS 字段，实际 %q", got.ISP)
	}
}

func TestEntryToResultIncludesGeo(t *testing.T) {
	got := entryToResult("1.2.3.4", batchEntry{
		Status: "success", Country: "Australia", CountryCode: "AU", RegionName: "Queensland", City: "Brisbane",
	})
	if got.Country != "Australia" || got.CountryCode != "AU" || got.Region != "Queensland" || got.City != "Brisbane" {
		t.Fatalf("unexpected geo result: %+v", got)
	}
}

func TestLookupMapsResultsBackToHosts(t *testing.T) {
	client, tr := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		return `[
			{"status":"success","query":"1.1.1.1","hosting":true,"isp":"Cloudflare"},
			{"status":"success","query":"2.2.2.2","mobile":true,"isp":"China Mobile"},
			{"status":"fail","message":"reserved range","query":"3.3.3.3"}
		]`, 200, nil
	})

	got := Lookup(client, []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"})
	if len(got) != 3 {
		t.Fatalf("应返回 3 条结果，实际 %d: %+v", len(got), got)
	}
	if got["1.1.1.1"].Kind != KindIDC || got["1.1.1.1"].ISP != "Cloudflare" {
		t.Errorf("1.1.1.1 应为机房+Cloudflare，实际 %+v", got["1.1.1.1"])
	}
	if got["2.2.2.2"].Kind != KindMobile {
		t.Errorf("2.2.2.2 应为移动网络，实际 %+v", got["2.2.2.2"])
	}
	if got["3.3.3.3"].Kind != KindUnknown || got["3.3.3.3"].Err == nil {
		t.Errorf("3.3.3.3 查询失败应为 unknown 且带错误，实际 %+v", got["3.3.3.3"])
	}
	if len(tr.bodies) != 1 {
		t.Fatalf("应只发 1 次请求，实际 %d", len(tr.bodies))
	}
}

// 多个节点常指向同一 IP，重复地址只该查一次，且每个入参都要拿到结果
func TestLookupDeduplicatesHosts(t *testing.T) {
	client, tr := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		return successBody(queries, `,"hosting":true`), 200, nil
	})

	got := Lookup(client, []string{"1.1.1.1", "1.1.1.1", "9.9.9.9", "1.1.1.1"})
	if len(tr.bodies) != 1 || len(tr.bodies[0]) != 2 {
		t.Fatalf("重复地址应合并为 2 个查询，实际 %+v", tr.bodies)
	}
	if got["1.1.1.1"].Kind != KindIDC || got["9.9.9.9"].Kind != KindIDC {
		t.Errorf("两个地址都应有结果，实际 %+v", got)
	}
}

// 免费接口单批最多 100 个，超出必须自动分批，否则整批会被拒绝
func TestLookupSplitsOversizedInput(t *testing.T) {
	hosts := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		hosts = append(hosts, fmt.Sprintf("8.8.%d.%d", i/256, i%256))
	}
	client, tr := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		if len(queries) > batchLimit {
			t.Errorf("单批 %d 个，超过上限 %d", len(queries), batchLimit)
		}
		return successBody(queries, `,"hosting":true`), 200, nil
	})

	got := Lookup(client, hosts)
	if len(got) != 150 {
		t.Errorf("应返回 150 条结果，实际 %d", len(got))
	}
	if len(tr.bodies) != 2 {
		t.Errorf("150 个地址应分 2 批，实际 %d 批", len(tr.bodies))
	}
}

// 私有地址查公网库没有意义，应就地标记而不是白跑一次请求
func TestLookupSkipsPrivateAndEmptyHosts(t *testing.T) {
	client, tr := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		return successBody(queries, `,"hosting":true`), 200, nil
	})

	got := Lookup(client, []string{"127.0.0.1", "192.168.1.10", "  ", "1.1.1.1"})
	if len(tr.bodies) != 1 || len(tr.bodies[0]) != 1 || tr.bodies[0][0] != "1.1.1.1" {
		t.Fatalf("只应查询公网地址，实际请求 %+v", tr.bodies)
	}
	for _, h := range []string{"127.0.0.1", "192.168.1.10"} {
		if got[h].Kind != KindUnknown || got[h].Err == nil {
			t.Errorf("%s 应标为 unknown 且说明原因，实际 %+v", h, got[h])
		}
	}
	if _, ok := got["  "]; ok {
		t.Error("空主机名不应出现在结果里")
	}
}

// 整批请求失败时，该批每个主机都要有结果，不能留空导致界面卡在检测中
func TestLookupMarksWholeBatchOnHTTPError(t *testing.T) {
	client, _ := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		return "", http.StatusTooManyRequests, http.Header{"X-Rl": []string{"0"}, "X-Ttl": []string{"3"}}
	})

	got := Lookup(client, []string{"1.1.1.1", "2.2.2.2"})
	if len(got) != 2 {
		t.Fatalf("应为 2 个主机都返回结果，实际 %+v", got)
	}
	for h, r := range got {
		if r.Kind != KindUnknown || r.Err == nil {
			t.Errorf("%s 应为 unknown 且带错误，实际 %+v", h, r)
		}
	}
}

// 返回条数少于请求条数时，缺失项要标记出来而不是错位复用别人的结果
func TestLookupHandlesShortResponse(t *testing.T) {
	client, _ := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		return `[{"status":"success","query":"1.1.1.1","hosting":true}]`, 200, nil
	})

	got := Lookup(client, []string{"1.1.1.1", "2.2.2.2"})
	if got["1.1.1.1"].Kind != KindIDC {
		t.Errorf("首条应正常解析，实际 %+v", got["1.1.1.1"])
	}
	if got["2.2.2.2"].Kind != KindUnknown || got["2.2.2.2"].Err == nil {
		t.Errorf("缺失项应标为 unknown 并带错误，实际 %+v", got["2.2.2.2"])
	}
}

func TestLookupEmptyInput(t *testing.T) {
	client, tr := stubClient(t, func(call int, queries []string) (string, int, http.Header) {
		t.Error("空输入不应发起请求")
		return "[]", 200, nil
	})

	if got := Lookup(client, nil); len(got) != 0 {
		t.Errorf("空输入应返回空结果，实际 %+v", got)
	}
	if len(tr.bodies) != 0 {
		t.Errorf("空输入不应发起请求，实际 %d 次", len(tr.bodies))
	}
}

func TestWaitFromHeaders(t *testing.T) {
	// 还有余量时不等待
	if d := waitFromHeaders(http.Header{"X-Rl": []string{"12"}, "X-Ttl": []string{"40"}}); d != 0 {
		t.Errorf("有剩余额度时不应等待，实际 %v", d)
	}
	// 额度耗尽时等到窗口重置
	if d := waitFromHeaders(http.Header{"X-Rl": []string{"0"}, "X-Ttl": []string{"10"}}); d <= 0 {
		t.Error("额度耗尽时应返回等待时长")
	}
	// 缺头时不应无限等待
	if d := waitFromHeaders(http.Header{}); d != 0 {
		t.Errorf("缺少限流头时不应等待，实际 %v", d)
	}
	// TTL 异常大时要有上限，避免请求线程被长时间占住
	if d := waitFromHeaders(http.Header{"X-Rl": []string{"0"}, "X-Ttl": []string{"9999"}}); d > 70_000_000_000 {
		t.Errorf("等待时长应有上限，实际 %v", d)
	}
}
