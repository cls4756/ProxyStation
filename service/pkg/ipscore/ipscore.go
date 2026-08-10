// ipscore 查询 IP 的"纯净度"评分（0-100，越高越可信）。
//
// 这类评分服务都没有面向第三方的正式 API 授权，因此本包默认不启用，必须由用户在
// 设置里显式打开并选择服务商——是否愿意把节点地址发给第三方，由用户自己决定。
//
// 所有 provider 共用一套串行限速：这些是别人的免费服务，批量刷会触发保护甚至被封，
// 宁可慢也不能把请求打成洪水。
package ipscore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider 评分服务商标识
type Provider string

const (
	// ProviderCleanIP cleanip.io，评分口径公开（6 维度加权 × 用途乘子），返回结构化 JSON
	ProviderCleanIP Provider = "cleanip"
	// ProviderCustom 用户自填接口。只配一个 URL，响应字段靠通用规则识别，
	// 因此适配任何返回"分值越高越干净"的 JSON 接口。
	ProviderCustom Provider = "custom"
)

const defaultCleanIPEndpoint = "https://cleanip.io/cli"

// IPPlaceholder 自定义 URL 里代表待查 IP 的占位符。没写占位符时按 ?ip= 拼查询参数。
const IPPlaceholder = "{ip}"

// Result 单个 IP 的评分结果
type Result struct {
	// Score 纯净度评分 0-100，越高越可信。-1 表示未取到
	Score int
	// Grade 等级（如 A+/B/C），部分服务商提供
	Grade string
	// RiskLevel 风险等级描述
	RiskLevel string
	// Provider 实际产出该结果的服务商
	Provider Provider
	// Country / Region / City 是评分服务同一次响应附带的地理信息
	Country     string
	CountryCode string
	Region      string
	City        string
	// Err 查询失败原因
	Err error
}

// Options 查询参数
type Options struct {
	Provider Provider
	// Endpoint 为空时用该 provider 的默认地址。允许用户自建镜像或换用兼容接口。
	Endpoint string
	// MinInterval 两次请求之间的最小间隔，0 表示用默认值
	MinInterval time.Duration
	// Timeout 单次请求超时
	Timeout time.Duration
}

// DefaultMinInterval 默认请求间隔。这些服务都在 Cloudflare 后面且未授权批量调用，
// 1.5 秒一个在实测中稳定，也不至于让几十个节点的检测慢到无法接受。
const DefaultMinInterval = 1500 * time.Millisecond

// Client 按顺序、限速地查询评分。多个调用方共用同一个 Client 时共享限速节奏。
type Client struct {
	HTTPClient *http.Client

	// cleanIPEndpoint 覆盖内置 cleanip 的接口地址，仅供测试注入。
	// 用户配置里的地址只作用于自定义服务商，不会流到这里。
	cleanIPEndpoint string

	mu       sync.Mutex
	lastCall time.Time
}

func NewClient(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{HTTPClient: hc}
}

// Lookup 查询单个 IP。调用会阻塞到满足限速间隔为止，因此必须在后台任务里用。
func (c *Client) Lookup(ip string, opt Options) Result {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return Result{Score: -1, Provider: opt.Provider, Err: fmt.Errorf("IP 为空")}
	}
	c.waitTurn(opt.MinInterval)

	switch opt.Provider {
	case ProviderCustom:
		return c.lookupCustom(ip, opt)
	case ProviderCleanIP, "":
		return c.lookupCleanIP(ip, opt)
	default:
		return Result{Score: -1, Provider: opt.Provider, Err: fmt.Errorf("未知的评分服务商：%s", opt.Provider)}
	}
}

// waitTurn 保证相邻两次外部请求之间至少间隔 minInterval
func (c *Client) waitTurn(minInterval time.Duration) {
	if minInterval <= 0 {
		minInterval = DefaultMinInterval
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastCall.IsZero() {
		if wait := minInterval - time.Since(c.lastCall); wait > 0 {
			time.Sleep(wait)
		}
	}
	c.lastCall = time.Now()
}

type cleanIPResponse struct {
	OK  bool `json:"ok"`
	Geo struct {
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		City        string `json:"city"`
	} `json:"geo"`
	Purity struct {
		Score int    `json:"score"`
		Grade string `json:"grade"`
	} `json:"purity"`
	Risk struct {
		RiskLabel string `json:"risk_label"`
		RiskLevel string `json:"risk_level"`
	} `json:"risk"`
}

// Options.Endpoint 只属于 ProviderCustom：内置服务商固定用自己的地址，
// 这样界面切换服务商时用户填过的自定义地址可以一直留着不被内置服务商误用。
func (c *Client) lookupCleanIP(ip string, opt Options) Result {
	endpoint := defaultCleanIPEndpoint
	if c.cleanIPEndpoint != "" {
		endpoint = c.cleanIPEndpoint
	}
	u, err := buildURL(endpoint, map[string]string{"ip": ip, "json": "1"})
	if err != nil {
		return Result{Score: -1, Provider: ProviderCleanIP, Err: err}
	}

	var resp cleanIPResponse
	if err := c.getJSON(u, opt.Timeout, &resp); err != nil {
		return Result{Score: -1, Provider: ProviderCleanIP, Err: err}
	}
	if !resp.OK {
		return Result{Score: -1, Provider: ProviderCleanIP, Err: fmt.Errorf("查询失败")}
	}
	risk := firstNonEmpty(resp.Risk.RiskLabel, resp.Risk.RiskLevel)
	return Result{
		Score:       clampScore(resp.Purity.Score),
		Grade:       strings.TrimSpace(resp.Purity.Grade),
		RiskLevel:   risk,
		Provider:    ProviderCleanIP,
		Country:     strings.TrimSpace(resp.Geo.Country),
		CountryCode: strings.ToUpper(strings.TrimSpace(resp.Geo.CountryCode)),
		Region:      strings.TrimSpace(resp.Geo.Region),
		City:        strings.TrimSpace(resp.Geo.City),
	}
}

// scoreFieldNames 自定义接口里可能表示评分的字段名，按优先级排列。
// 覆盖了常见的几种命名：cleanip 的 purity.score、ip.net.coffee 的 trust_score 等。
var scoreFieldNames = []string{
	"trust_score", "trustScore",
	"purity_score", "purityScore",
	"score", "value", "rating",
}

// gradeFieldNames / riskFieldNames 是可选的展示字段，取不到就留空
var gradeFieldNames = []string{"grade", "level", "rank"}
var riskFieldNames = []string{"risk_label", "riskLabel", "risk_level", "riskLevel", "abuser_level", "abuserLevel", "risk"}
var countryFieldNames = []string{"country", "country_name", "countryName"}
var countryCodeFieldNames = []string{"country_code", "countryCode", "country_iso", "countryIso", "country_iso_code"}
var regionFieldNames = []string{"region", "region_name", "regionName", "state", "province"}
var cityFieldNames = []string{"city", "city_name", "cityName"}

// lookupCustom 查询用户自填的接口。URL 里写 {ip} 时替换该占位符，否则作为 ?ip= 参数追加。
// 响应按字段名在整个 JSON 树里递归查找评分，因此不要求对方的结构与某个服务商一致。
func (c *Client) lookupCustom(ip string, opt Options) Result {
	endpoint := strings.TrimSpace(opt.Endpoint)
	if endpoint == "" {
		return Result{Score: -1, Provider: ProviderCustom, Err: fmt.Errorf("未填写自定义评分接口地址")}
	}

	var target string
	if strings.Contains(endpoint, IPPlaceholder) {
		target = strings.ReplaceAll(endpoint, IPPlaceholder, url.PathEscape(ip))
	} else {
		u, err := buildURL(endpoint, map[string]string{"ip": ip})
		if err != nil {
			return Result{Score: -1, Provider: ProviderCustom, Err: err}
		}
		target = u
	}

	var raw map[string]interface{}
	if err := c.getJSON(target, opt.Timeout, &raw); err != nil {
		return Result{Score: -1, Provider: ProviderCustom, Err: err}
	}

	score, ok := findNumberField(raw, scoreFieldNames)
	if !ok {
		return Result{Score: -1, Provider: ProviderCustom,
			Err: fmt.Errorf("响应中找不到评分字段（支持 %s）", strings.Join(scoreFieldNames, " / "))}
	}
	grade, _ := findStringField(raw, gradeFieldNames)
	risk, _ := findStringField(raw, riskFieldNames)
	country, _ := findStringField(raw, countryFieldNames)
	countryCode, _ := findStringField(raw, countryCodeFieldNames)
	region, _ := findStringField(raw, regionFieldNames)
	city, _ := findStringField(raw, cityFieldNames)
	return Result{
		Score:       clampScore(int(score)),
		Grade:       strings.TrimSpace(grade),
		RiskLevel:   strings.TrimSpace(risk),
		Provider:    ProviderCustom,
		Country:     strings.TrimSpace(country),
		CountryCode: strings.ToUpper(strings.TrimSpace(countryCode)),
		Region:      strings.TrimSpace(region),
		City:        strings.TrimSpace(city),
	}
}

// findNumberField 在 JSON 树里按名字优先级查找数值字段。
// 先整层广度优先匹配同一个名字，避免深层的同名字段抢在浅层前面。
func findNumberField(root map[string]interface{}, names []string) (float64, bool) {
	for _, name := range names {
		if v, ok := searchField(root, name); ok {
			switch n := v.(type) {
			case float64:
				return n, true
			case string:
				if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
					return f, true
				}
			}
		}
	}
	return 0, false
}

func findStringField(root map[string]interface{}, names []string) (string, bool) {
	for _, name := range names {
		if v, ok := searchField(root, name); ok {
			if s, isStr := v.(string); isStr && strings.TrimSpace(s) != "" {
				return s, true
			}
		}
	}
	return "", false
}

// searchField 广度优先查找指定字段名，浅层优先
func searchField(root map[string]interface{}, name string) (interface{}, bool) {
	queue := []map[string]interface{}{root}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if v, ok := node[name]; ok && v != nil {
			return v, true
		}
		for _, v := range node {
			if child, ok := v.(map[string]interface{}); ok {
				queue = append(queue, child)
			}
		}
	}
	return nil, false
}

func (c *Client) getJSON(rawURL string, timeout time.Duration, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ProxyStation/1.0 (+node ip purity check)")

	hc := c.HTTPClient
	if timeout > 0 {
		clone := *hc
		clone.Timeout = timeout
		hc = &clone
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("请求评分服务失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("评分服务限流，请降低检测频率或稍后再试")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("评分服务返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析评分响应失败：%w", err)
	}
	return nil
}

func buildURL(endpoint string, params map[string]string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("评分接口地址非法：%w", err)
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func clampScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// ValidateProvider 归一化并校验服务商标识
func ValidateProvider(raw string) (Provider, error) {
	switch Provider(strings.ToLower(strings.TrimSpace(raw))) {
	case ProviderCleanIP, "":
		return ProviderCleanIP, nil
	case ProviderCustom:
		return ProviderCustom, nil
	default:
		return "", fmt.Errorf("未知的评分服务商：%s", raw)
	}
}

// ValidateEndpoint 校验自定义接口地址，空串表示用默认地址。
// {ip} 占位符会先替换掉再解析，否则花括号会被判成非法 URL 字符。
func ValidateEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	probe := strings.ReplaceAll(raw, IPPlaceholder, "1.1.1.1")
	u, err := url.Parse(probe)
	if err != nil {
		return "", fmt.Errorf("评分接口地址非法：%v", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("评分接口地址必须以 http:// 或 https:// 开头")
	}
	if u.Host == "" {
		return "", fmt.Errorf("评分接口地址缺少域名")
	}
	return raw, nil
}

// ValidateSettings 校验服务商与地址的组合，自定义服务商必须自带地址。
func ValidateSettings(provider, endpoint string) (Provider, string, error) {
	return ValidateProviderEndpoint(provider, endpoint, true)
}

// ValidateProviderEndpoint 校验服务商与地址。requireEndpoint 为真时，自定义服务商
// 必须填地址——功能未启用时放宽，允许用户先选服务商、之后再回来补地址。
func ValidateProviderEndpoint(provider, endpoint string, requireEndpoint bool) (Provider, string, error) {
	p, err := ValidateProvider(provider)
	if err != nil {
		return "", "", err
	}
	ep, err := ValidateEndpoint(endpoint)
	if err != nil {
		return "", "", err
	}
	if requireEndpoint && p == ProviderCustom && ep == "" {
		return "", "", fmt.Errorf("选择自定义服务商时必须填写接口地址")
	}
	return p, ep, nil
}
