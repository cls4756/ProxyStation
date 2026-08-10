// ipinfo 通过 ip-api.com 的公开批量接口判断节点地址属于机房、家宽还是移动网络。
//
// 选它的原因：免费、不需要 API key、单次请求可查 100 个 IP，且直接给出 hosting /
// mobile 标记，不用自己维护 ASN 名单。代价是免费版只提供 HTTP（HTTPS 需付费），
// 请求内容仅为节点地址，不含任何凭据。
package ipinfo

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kind 地址类型
type Kind string

const (
	KindIDC         Kind = "idc"         // 机房 / 托管
	KindResidential Kind = "residential" // 家宽住宅
	KindMobile      Kind = "mobile"      // 移动蜂窝网络
	KindUnknown     Kind = "unknown"     // 查不到或查询失败
)

// batchLimit ip-api.com /batch 单次请求的 IP 上限
const batchLimit = 100

const endpoint = "http://ip-api.com/batch?fields=status,message,country,countryCode,regionName,city,isp,org,as,mobile,proxy,hosting,query"

// Result 单个主机的判定结果
type Result struct {
	Kind Kind
	// IP 实际查询的地址（域名会先解析成 IP）
	IP string
	// ISP 运营商 / 组织名，用于界面展示判定依据
	ISP         string
	Country     string
	CountryCode string
	Region      string
	City        string
	// Err 该主机查询失败的原因，成功时为 nil
	Err error
}

type batchEntry struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	ISP         string `json:"isp"`
	Org         string `json:"org"`
	AS          string `json:"as"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	RegionName  string `json:"regionName"`
	City        string `json:"city"`
	Mobile      bool   `json:"mobile"`
	Proxy       bool   `json:"proxy"`
	Hosting     bool   `json:"hosting"`
	Query       string `json:"query"`
}

// Lookup 查询一批主机（IP 或域名）的地址类型，返回以入参主机名为键的结果。
// client 由调用方提供，用于复用代理配置；重复的主机只会查一次。
func Lookup(client *http.Client, hosts []string) map[string]Result {
	results := make(map[string]Result, len(hosts))
	// 多个节点常指向同一 IP，按 IP 聚合后再查，省掉重复请求
	ipToHosts := make(map[string][]string)
	order := make([]string, 0, len(hosts))
	seen := make(map[string]bool, len(hosts))

	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		ip, err := resolveHost(host)
		if err != nil {
			results[host] = Result{Kind: KindUnknown, Err: err}
			continue
		}
		if isPrivateIP(ip) {
			results[host] = Result{Kind: KindUnknown, IP: ip, Err: fmt.Errorf("私有地址，无法判断类型")}
			continue
		}
		if _, ok := ipToHosts[ip]; !ok {
			order = append(order, ip)
		}
		ipToHosts[ip] = append(ipToHosts[ip], host)
	}

	for start := 0; start < len(order); start += batchLimit {
		end := start + batchLimit
		if end > len(order) {
			end = len(order)
		}
		chunk := order[start:end]
		entries, wait, err := queryBatch(client, chunk)
		if err != nil {
			for _, ip := range chunk {
				for _, host := range ipToHosts[ip] {
					results[host] = Result{Kind: KindUnknown, IP: ip, Err: err}
				}
			}
			continue
		}
		for i, ip := range chunk {
			res := Result{Kind: KindUnknown, IP: ip}
			if i < len(entries) {
				res = entryToResult(ip, entries[i])
			} else {
				res.Err = fmt.Errorf("查询结果缺失")
			}
			for _, host := range ipToHosts[ip] {
				results[host] = res
			}
		}
		// 免费额度按分钟计，剩余次数见底时等限流窗口重置，否则后续批次会整批 429
		if wait > 0 && end < len(order) {
			time.Sleep(wait)
		}
	}
	return results
}

func entryToResult(ip string, e batchEntry) Result {
	res := Result{
		IP: ip, Kind: KindUnknown, ISP: firstNonEmpty(e.ISP, e.Org, e.AS),
		Country: e.Country, CountryCode: e.CountryCode, Region: e.RegionName, City: e.City,
	}
	if e.Status != "success" {
		msg := strings.TrimSpace(e.Message)
		if msg == "" {
			msg = "查询失败"
		}
		res.Err = fmt.Errorf("%s", msg)
		return res
	}
	switch {
	case e.Hosting:
		res.Kind = KindIDC
	case e.Mobile:
		res.Kind = KindMobile
	default:
		res.Kind = KindResidential
	}
	return res
}

// queryBatch 查一批 IP，第二个返回值是限流额度将要耗尽时建议等待的时长。
func queryBatch(client *http.Client, ips []string) ([]batchEntry, time.Duration, error) {
	payload := make([]map[string]string, 0, len(ips))
	for _, ip := range ips {
		payload = append(payload, map[string]string{"query": ip})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求 ip-api.com 失败：%w", err)
	}
	defer resp.Body.Close()

	wait := waitFromHeaders(resp.Header)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, wait, fmt.Errorf("ip-api.com 限流，请稍后重试")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, wait, fmt.Errorf("ip-api.com 返回 HTTP %d", resp.StatusCode)
	}
	var entries []batchEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, wait, fmt.Errorf("解析 ip-api.com 响应失败：%w", err)
	}
	return entries, wait, nil
}

// waitFromHeaders 解析 ip-api.com 的限流头：X-Rl 是当前窗口剩余次数，
// X-Ttl 是窗口重置剩余秒数。
func waitFromHeaders(h http.Header) time.Duration {
	remaining, err := strconv.Atoi(strings.TrimSpace(h.Get("X-Rl")))
	if err != nil || remaining > 0 {
		return 0
	}
	ttl, err := strconv.Atoi(strings.TrimSpace(h.Get("X-Ttl")))
	if err != nil || ttl <= 0 {
		ttl = 60
	}
	if ttl > 65 {
		ttl = 65
	}
	return time.Duration(ttl+1) * time.Second
}

func resolveHost(host string) (string, error) {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("域名解析失败：%w", err)
	}
	for _, addr := range addrs {
		if addr.To4() != nil {
			return addr.String(), nil
		}
	}
	if len(addrs) > 0 {
		return addrs[0].String(), nil
	}
	return "", fmt.Errorf("域名解析失败：无结果")
}

func isPrivateIP(raw string) bool {
	ip := net.ParseIP(raw)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
