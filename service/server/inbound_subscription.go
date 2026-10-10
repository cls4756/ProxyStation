package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
	"github.com/gin-gonic/gin"
	gonanoid "github.com/matoous/go-nanoid"
)

type inboundSubscriptionView struct {
	configure.InboundSubscription
	URL string `json:"url"`
}

type inboundSubscriptionRequest struct {
	Name       string                              `json:"name"`
	Host       string                              `json:"host"`
	Format     string                              `json:"format"`
	InboundIDs []string                            `json:"inboundIds"`
	Rules      []configure.InboundSubscriptionRule `json:"rules"`
}

func getInboundSubscriptions(c *gin.Context) {
	items := configure.GetInboundSubscriptions()
	result := make([]inboundSubscriptionView, 0, len(items))
	for _, item := range items {
		result = append(result, inboundSubscriptionView{InboundSubscription: item, URL: inboundSubscriptionURL(c, item.Token)})
	}
	c.JSON(http.StatusOK, gin.H{"subscriptions": result})
}

func createInboundSubscription(c *gin.Context) {
	var req inboundSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := normalizeInboundSubscription(req, nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.AppendInboundSubscription(item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, inboundSubscriptionView{InboundSubscription: *item, URL: inboundSubscriptionURL(c, item.Token)})
}

func updateInboundSubscription(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	items := configure.GetInboundSubscriptions()
	if index < 0 || index >= len(items) {
		c.JSON(http.StatusNotFound, gin.H{"error": "subscription not found"})
		return
	}
	var req inboundSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := normalizeInboundSubscription(req, &items[index])
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := configure.SetInboundSubscription(index, item); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, inboundSubscriptionView{InboundSubscription: *item, URL: inboundSubscriptionURL(c, item.Token)})
}

func deleteInboundSubscription(c *gin.Context) {
	index, err := strconv.Atoi(c.Param("index"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid index"})
		return
	}
	if err := configure.DeleteInboundSubscription(index); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func normalizeInboundSubscription(req inboundSubscriptionRequest, old *configure.InboundSubscription) (*configure.InboundSubscription, error) {
	name := strings.TrimSpace(req.Name)
	host := strings.TrimSpace(req.Host)
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if name == "" {
		return nil, fmt.Errorf("订阅名称不能为空")
	}
	if !validPublicHost(host) {
		return nil, fmt.Errorf("请填写有效的域名或 IP 地址")
	}
	if format != "singbox" && format != "v2ray" {
		return nil, fmt.Errorf("订阅格式仅支持 singbox 或 v2ray")
	}
	ids := uniqueStrings(req.InboundIDs)
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fmt.Errorf("请选择 1-100 个入站")
	}
	available := inboundSubscriptionInbounds()
	found := make(map[string]bool, len(available))
	for _, ib := range available {
		found[ib.ID] = true
	}
	for _, id := range ids {
		if !found[id] {
			return nil, fmt.Errorf("入站不存在: %s", id)
		}
		for _, ib := range available {
			if ib.ID == id {
				protocol := strings.ToLower(ib.Protocol)
				if protocol != "http" && protocol != "socks" && protocol != "vmess" {
					return nil, fmt.Errorf("入站 %s 不是 HTTP/SOCKS/VMess", ib.Name)
				}
				break
			}
		}
	}
	rules, err := normalizeInboundSubscriptionRules(req.Rules, available, ids, format)
	if err != nil {
		return nil, err
	}
	item := &configure.InboundSubscription{Name: name, Host: host, Format: format, InboundIDs: ids, Rules: rules, UpdatedAt: time.Now()}
	if old != nil {
		item.ID, item.Token, item.CreatedAt = old.ID, old.Token, old.CreatedAt
	} else {
		item.ID, _ = gonanoid.Nanoid()
		item.Token, _ = gonanoid.Nanoid(32)
		item.CreatedAt = time.Now()
	}
	return item, nil
}

func normalizeInboundSubscriptionRules(rules []configure.InboundSubscriptionRule, available []configure.CustomInbound, selectedIDs []string, format string) ([]configure.InboundSubscriptionRule, error) {
	if len(rules) > 100 {
		return nil, fmt.Errorf("订阅规则不能超过 100 条")
	}
	if format == "v2ray" && len(rules) > 0 {
		return nil, fmt.Errorf("v2ray 格式不支持携带路由规则，请改用 sing-box 格式")
	}
	selected := make(map[string]bool, len(selectedIDs))
	for _, id := range selectedIDs {
		selected[id] = true
	}
	result := make([]configure.InboundSubscriptionRule, 0, len(rules))
	for index, rule := range rules {
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
		if rule.Action == "" {
			rule.Action = "proxy"
		}
		if rule.Action != "proxy" && rule.Action != "direct" && rule.Action != "block" {
			return nil, fmt.Errorf("第 %d 条订阅规则动作非法", index+1)
		}
		if rule.Action == "proxy" && rule.OutboundInboundID != "" && !selected[rule.OutboundInboundID] {
			return nil, fmt.Errorf("第 %d 条规则引用了未选择的代理入站", index+1)
		}
		if rule.Protocol != "" && rule.Protocol != "tcp" && rule.Protocol != "udp" && rule.Protocol != "tcp,udp" {
			return nil, fmt.Errorf("第 %d 条订阅规则协议非法", index+1)
		}
		if rule.Ports != "" {
			if _, _, err := parseSubscriptionPorts(rule.Ports); err != nil {
				return nil, fmt.Errorf("第 %d 条订阅规则端口非法: %w", index+1, err)
			}
		}
		rule.Domains = uniqueStrings(rule.Domains)
		rule.IPs = uniqueStrings(rule.IPs)
		if len(rule.Domains) == 0 && len(rule.IPs) == 0 && rule.Ports == "" && rule.Protocol == "" {
			return nil, fmt.Errorf("第 %d 条订阅规则至少需要一个匹配条件", index+1)
		}
		if rule.ID == "" {
			rule.ID, _ = gonanoid.Nanoid()
		}
		result = append(result, rule)
	}
	_ = available
	return result, nil
}

func validPublicHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/?#\t\r\n :") {
		return false
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return true
	}
	return strings.Contains(host, ".") || (len(host) >= 1 && len(host) <= 253)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func inboundSubscriptionInbounds() []configure.CustomInbound {
	result := configure.GetCustomInbounds()
	setting := configure.GetSettingNotNil()
	result = append([]configure.CustomInbound{{ID: "builtin:socks", Name: "默认 SOCKS5", Protocol: "socks", Port: setting.Socks5Port, Accounts: setting.Socks5Accounts, Username: setting.Socks5Username, Password: setting.Socks5Password}, {ID: "builtin:http", Name: "默认 HTTP", Protocol: "http", Port: setting.HttpPort, Accounts: setting.HttpAccounts, Username: setting.HttpUsername, Password: setting.HttpPassword}}, result...)
	return result
}

func inboundSubscriptionURL(c *gin.Context, token string) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host + "/subscribe/inbounds/" + url.PathEscape(token)
}

func getPublicInboundSubscription(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	token := c.Param("token")
	var item *configure.InboundSubscription
	for _, value := range configure.GetInboundSubscriptions() {
		if value.Token == token {
			copy := value
			item = &copy
			break
		}
	}
	if item == nil {
		c.String(http.StatusNotFound, "subscription not found")
		return
	}
	content, err := renderInboundSubscription(*item)
	if err != nil {
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	if item.Format == "v2ray" {
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(base64.StdEncoding.EncodeToString([]byte(content))))
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(content))
}

func renderInboundSubscription(item configure.InboundSubscription) (string, error) {
	all := inboundSubscriptionInbounds()
	byID := map[string]configure.CustomInbound{}
	for _, ib := range all {
		byID[ib.ID] = ib
	}
	if item.Format == "v2ray" {
		links := make([]string, 0, len(item.InboundIDs))
		for _, id := range item.InboundIDs {
			ib := byID[id]
			link, err := inboundV2rayLink(ib, item.Host)
			if err != nil {
				return "", err
			}
			links = append(links, link)
		}
		return strings.Join(links, "\n"), nil
	}
	outbounds := make([]map[string]interface{}, 0, len(item.InboundIDs))
	outboundTags := make(map[string]string, len(item.InboundIDs))
	for _, id := range item.InboundIDs {
		ib := byID[id]
		out, err := inboundSingboxOutbound(ib, item.Host)
		if err != nil {
			return "", err
		}
		outbounds = append(outbounds, out)
		outboundTags[id] = ib.Name
	}
	config := map[string]interface{}{"outbounds": outbounds}
	if len(item.Rules) > 0 {
		route, err := renderInboundSubscriptionRoute(item.Rules, item.InboundIDs, outboundTags)
		if err != nil {
			return "", err
		}
		config["route"] = route
	}
	data, err := json.MarshalIndent(config, "", "  ")
	return string(data), err
}

func renderInboundSubscriptionRoute(rules []configure.InboundSubscriptionRule, selectedIDs []string, outboundTags map[string]string) (map[string]interface{}, error) {
	selectedTags := make([]string, 0, len(selectedIDs))
	for _, id := range selectedIDs {
		if tag := outboundTags[id]; tag != "" {
			selectedTags = append(selectedTags, tag)
		}
	}
	if len(selectedTags) == 0 {
		return nil, fmt.Errorf("订阅没有可用代理出站")
	}
	output := make([]map[string]interface{}, 0, len(rules))
	allRuleSetTags := make([]string, 0)
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		value := map[string]interface{}{}
		var ruleSetTags, suffixes, keywords, regexes, cidrs []string
		privateIP := false
		for _, domain := range rule.Domains {
			switch {
			case strings.HasPrefix(domain, "geosite:"):
				name := strings.TrimPrefix(domain, "geosite:")
				if name != "" {
					tag := "geosite-" + name
					ruleSetTags = append(ruleSetTags, tag)
					allRuleSetTags = append(allRuleSetTags, tag)
				}
			case strings.HasPrefix(domain, "domain:"):
				suffixes = append(suffixes, strings.TrimPrefix(domain, "domain:"))
			case strings.HasPrefix(domain, "regexp:"):
				regexes = append(regexes, strings.TrimPrefix(domain, "regexp:"))
			case strings.HasPrefix(domain, "keyword:"):
				keywords = append(keywords, strings.TrimPrefix(domain, "keyword:"))
			default:
				suffixes = append(suffixes, domain)
			}
		}
		for _, ip := range rule.IPs {
			if ip == "geoip:private" {
				privateIP = true
			} else if strings.HasPrefix(ip, "geoip:") {
				tag := "geoip-" + strings.TrimPrefix(ip, "geoip:")
				if tag != "geoip-" {
					ruleSetTags = append(ruleSetTags, tag)
					allRuleSetTags = append(allRuleSetTags, tag)
				}
			} else {
				cidrs = append(cidrs, ip)
			}
		}
		if len(ruleSetTags) > 0 {
			value["rule_set"] = ruleSetTags
		}
		if len(suffixes) > 0 {
			value["domain_suffix"] = suffixes
		}
		if len(keywords) > 0 {
			value["domain_keyword"] = keywords
		}
		if len(regexes) > 0 {
			value["domain_regex"] = regexes
		}
		if len(cidrs) > 0 {
			value["ip_cidr"] = cidrs
		}
		if privateIP {
			value["ip_is_private"] = true
		}
		if ports, ranges, err := parseSubscriptionPorts(rule.Ports); err != nil {
			return nil, err
		} else {
			if len(ports) > 0 {
				value["port"] = ports
			}
			if len(ranges) > 0 {
				value["port_range"] = ranges
			}
		}
		if rule.Protocol != "" {
			value["network"] = rule.Protocol
		}
		switch rule.Action {
		case "direct":
			value["outbound"] = "direct"
		case "block":
			value["action"] = "reject"
		default:
			tag := selectedTags[0]
			if rule.OutboundInboundID != "" {
				tag = outboundTags[rule.OutboundInboundID]
			}
			if tag == "" {
				return nil, fmt.Errorf("规则 %s 没有可用代理出站", rule.Name)
			}
			value["outbound"] = tag
		}
		output = append(output, value)
	}
	route := map[string]interface{}{"rules": output, "final": selectedTags[0]}
	if len(allRuleSetTags) > 0 {
		definitions := make([]map[string]interface{}, 0, len(allRuleSetTags))
		seen := make(map[string]bool, len(allRuleSetTags))
		for _, tag := range allRuleSetTags {
			if seen[tag] {
				continue
			}
			seen[tag] = true
			name := strings.TrimPrefix(tag, "geosite-")
			resourceURL := "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/" + name + ".srs"
			if strings.HasPrefix(tag, "geoip-") {
				name = strings.TrimPrefix(tag, "geoip-")
				resourceURL = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geoip/" + name + ".srs"
			}
			definitions = append(definitions, map[string]interface{}{"tag": tag, "type": "remote", "format": "binary", "url": resourceURL, "download_detour": selectedTags[0]})
		}
		route["rule_set"] = definitions
	}
	return route, nil
}

func parseSubscriptionPorts(raw string) ([]uint16, []string, error) {
	var ports []uint16
	var ranges []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			pieces := strings.Split(part, "-")
			if len(pieces) != 2 {
				return nil, nil, fmt.Errorf("端口范围格式错误")
			}
			start, err1 := strconv.Atoi(strings.TrimSpace(pieces[0]))
			end, err2 := strconv.Atoi(strings.TrimSpace(pieces[1]))
			if err1 != nil || err2 != nil || start < 1 || end > 65535 || start > end {
				return nil, nil, fmt.Errorf("端口范围超出 1-65535")
			}
			ranges = append(ranges, fmt.Sprintf("%d:%d", start, end))
			continue
		}
		port, err := strconv.Atoi(part)
		if err != nil || port < 1 || port > 65535 {
			return nil, nil, fmt.Errorf("端口超出 1-65535")
		}
		ports = append(ports, uint16(port))
	}
	return ports, ranges, nil
}

func inboundSingboxOutbound(ib configure.CustomInbound, host string) (map[string]interface{}, error) {
	p := strings.ToLower(ib.Protocol)
	if p == "vmess" {
		user, err := firstVMessInboundUser(ib)
		if err != nil {
			return nil, err
		}
		out := map[string]interface{}{
			"type":        "vmess",
			"tag":         ib.Name,
			"server":      host,
			"server_port": ib.Port,
			"uuid":        user.UUID,
			"alter_id":    user.AlterID,
			"security":    "auto",
		}
		if ib.VMess != nil && strings.EqualFold(ib.VMess.Transport, "ws") {
			transport := map[string]interface{}{"type": "ws", "path": firstNonEmptySubscription(ib.VMess.Path, "/")}
			if strings.TrimSpace(ib.VMess.Host) != "" {
				transport["headers"] = map[string]string{"Host": strings.TrimSpace(ib.VMess.Host)}
			}
			out["transport"] = transport
		}
		return out, nil
	}
	if p != "socks" && p != "http" {
		return nil, fmt.Errorf("入站 %s 不是 HTTP/SOCKS/VMess", ib.Name)
	}
	out := map[string]interface{}{"type": p, "tag": ib.Name, "server": host, "server_port": ib.Port}
	accounts := ib.AuthAccounts()
	if len(accounts) > 0 {
		out["username"], out["password"] = accounts[0].Username, accounts[0].Password
	}
	return out, nil
}
func inboundV2rayLink(ib configure.CustomInbound, host string) (string, error) {
	p := strings.ToLower(ib.Protocol)
	if p == "vmess" {
		user, err := firstVMessInboundUser(ib)
		if err != nil {
			return "", err
		}
		transport, path, wsHost := "tcp", "", ""
		if ib.VMess != nil && strings.EqualFold(ib.VMess.Transport, "ws") {
			transport = "ws"
			path = firstNonEmptySubscription(ib.VMess.Path, "/")
			wsHost = strings.TrimSpace(ib.VMess.Host)
		}
		payload := map[string]string{
			"v":    "2",
			"ps":   ib.Name,
			"add":  strings.Trim(host, "[]"),
			"port": strconv.Itoa(ib.Port),
			"id":   user.UUID,
			"aid":  strconv.Itoa(user.AlterID),
			"scy":  "auto",
			"net":  transport,
			"type": "none",
			"host": wsHost,
			"path": path,
			"tls":  "",
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(data), nil
	}
	if p != "socks" && p != "http" {
		return "", fmt.Errorf("入站 %s 不是 HTTP/SOCKS/VMess", ib.Name)
	}
	u := &url.URL{Scheme: p, Host: net.JoinHostPort(host, strconv.Itoa(ib.Port)), User: nil}
	accounts := ib.AuthAccounts()
	if len(accounts) > 0 {
		u.User = url.UserPassword(accounts[0].Username, accounts[0].Password)
	}
	q := u.Query()
	q.Set("name", ib.Name)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func firstVMessInboundUser(ib configure.CustomInbound) (configure.VMessInboundUser, error) {
	if ib.VMess != nil {
		for _, user := range ib.VMess.Users {
			user.UUID = strings.TrimSpace(user.UUID)
			if user.UUID != "" {
				return user, nil
			}
		}
	}
	return configure.VMessInboundUser{}, fmt.Errorf("VMess 入站 %s 没有可用 UUID", ib.Name)
}

func firstNonEmptySubscription(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
