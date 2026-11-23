package configure

import (
	"database/sql"
	"encoding/json"
	"log"
	"strings"

	"github.com/ProxyStation/proxystation/db"
)

// setting 表只有一行（id=1），把配置的标量字段拆成列。节点清理/复制规则、认证账号
// 列表与整行配置一起读写、从不单独过滤，保留 JSON 列。相比原来一个 kv JSON 值，
// 字段可读可查、迁移更强，也配合上层 settingCache 避免高频反序列化。

type settingRow struct {
	logLevel                           string
	kernelMode                         string
	enableSniff                        bool
	socks5SniffEnabled                 sql.NullInt64
	httpSniffEnabled                   sql.NullInt64
	enableHijackDNS                    bool
	dnsMode                            string
	dnsRemoteServer                    string
	subscriptionAutoUpdateMode         string
	subscriptionAutoUpdateIntervalHour int
	transparentMode                    string
	lanSharingEnabled                  bool
	socks5Port                         int
	httpPort                           int
	maxLogLines                        int
	maxLogFileSize                     int64
	downloadProxy                      string
	githubMirror                       string
	probeTargets                       string
	groupRealProbeIntervalSec          int
	groupProbeMode                     string
	groupProbeConcurrency              int
	groupSwitchThresholdMs             int
	groupSwitchCooldownSec             int
	groupActiveNodeProbeEnabled        sql.NullInt64
	groupActiveNodeProbeIntervalSec    int
	nodeCleanupRules                   string
	nodeCleanupMode                    string
	nodeCleanupIntervalHour            int
	nodeCleanupGroupIDs                string
	nodeCleanupRecycleGroupID          string
	subscriptionBestNodeCopyRules      string
	ipScoreEnabled                     bool
	ipScoreProvider                    string
	ipScoreEndpoint                    string
	ipScoreMinIntervalMs               int
	socks5Username                     string
	socks5Password                     string
	httpUsername                       string
	httpPassword                       string
	socks5Accounts                     string
	httpAccounts                       string
	webUsername                        string
	webPassword                        string
	sharedNodesFixed                   bool
}

const settingColumns = "log_level, kernel_mode, enable_sniff, socks5_sniff_enabled, http_sniff_enabled, enable_hijack_dns, dns_mode, dns_remote_server, subscription_auto_update_mode, subscription_auto_update_interval_hour, transparent_mode, lan_sharing_enabled, socks5_port, http_port, max_log_lines, max_log_file_size, download_proxy, github_mirror, probe_targets, group_real_probe_interval_sec, group_probe_mode, group_probe_concurrency, group_switch_threshold_ms, group_switch_cooldown_sec, group_active_node_probe_enabled, group_active_node_probe_interval_sec, node_cleanup_rules, node_cleanup_mode, node_cleanup_interval_hour, node_cleanup_group_ids, node_cleanup_recycle_group_id, subscription_best_node_copy_rules, ip_score_enabled, ip_score_provider, ip_score_endpoint, ip_score_min_interval_ms, socks5_username, socks5_password, http_username, http_password, socks5_accounts, http_accounts, web_username, web_password, shared_nodes_fixed"

func intPtrOrNull(v *bool) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	n := int64(0)
	if *v {
		n = 1
	}
	return sql.NullInt64{Int64: n, Valid: true}
}

func jsonList(v any, fallback string) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return fallback
	}
	return string(b)
}

func settingRowFrom(s *Setting) settingRow {
	return settingRow{
		logLevel:                           s.LogLevel,
		kernelMode:                         s.KernelMode,
		enableSniff:                        s.EnableSniff,
		socks5SniffEnabled:                 intPtrOrNull(s.Socks5SniffEnabled),
		httpSniffEnabled:                   intPtrOrNull(s.HttpSniffEnabled),
		enableHijackDNS:                    s.EnableHijackDNS,
		dnsMode:                            s.DNSMode,
		dnsRemoteServer:                    s.DNSRemoteServer,
		subscriptionAutoUpdateMode:         s.SubscriptionAutoUpdateMode,
		subscriptionAutoUpdateIntervalHour: s.SubscriptionAutoUpdateIntervalHour,
		transparentMode:                    s.TransparentMode,
		lanSharingEnabled:                  s.LanSharingEnabled,
		socks5Port:                         s.Socks5Port,
		httpPort:                           s.HttpPort,
		maxLogLines:                        s.MaxLogLines,
		maxLogFileSize:                     s.MaxLogFileSize,
		downloadProxy:                      s.DownloadProxy,
		githubMirror:                       s.GithubMirror,
		probeTargets:                       s.ProbeTargets,
		groupRealProbeIntervalSec:          s.GroupRealProbeIntervalSec,
		groupProbeMode:                     s.GroupProbeMode,
		groupProbeConcurrency:              s.GroupProbeConcurrency,
		groupSwitchThresholdMs:             s.GroupSwitchThresholdMs,
		groupSwitchCooldownSec:             s.GroupSwitchCooldownSec,
		groupActiveNodeProbeEnabled:        intPtrOrNull(s.GroupActiveNodeProbeEnabled),
		groupActiveNodeProbeIntervalSec:    s.GroupActiveNodeProbeIntervalSec,
		nodeCleanupRules:                   jsonList(s.NodeCleanupRules, "[]"),
		nodeCleanupMode:                    s.NodeCleanupMode,
		nodeCleanupIntervalHour:            s.NodeCleanupIntervalHour,
		nodeCleanupGroupIDs:                jsonList(s.NodeCleanupGroupIDs, "[]"),
		nodeCleanupRecycleGroupID:          s.NodeCleanupRecycleGroupID,
		subscriptionBestNodeCopyRules:      jsonList(s.SubscriptionBestNodeCopyRules, "[]"),
		ipScoreEnabled:                     s.IPScoreEnabled,
		ipScoreProvider:                    s.IPScoreProvider,
		ipScoreEndpoint:                    s.IPScoreEndpoint,
		ipScoreMinIntervalMs:               s.IPScoreMinIntervalMs,
		socks5Username:                     s.Socks5Username,
		socks5Password:                     s.Socks5Password,
		httpUsername:                       s.HttpUsername,
		httpPassword:                       s.HttpPassword,
		socks5Accounts:                     jsonList(s.Socks5Accounts, "[]"),
		httpAccounts:                       jsonList(s.HttpAccounts, "[]"),
		webUsername:                        s.WebUsername,
		webPassword:                        s.WebPassword,
		sharedNodesFixed:                   s.SharedNodesFixed,
	}
}

func scanSettingRow(scan func(...any) error) (*settingRow, error) {
	var r settingRow
	if err := scan(&r.logLevel, &r.kernelMode, &r.enableSniff, &r.socks5SniffEnabled, &r.httpSniffEnabled,
		&r.enableHijackDNS, &r.dnsMode, &r.dnsRemoteServer, &r.subscriptionAutoUpdateMode,
		&r.subscriptionAutoUpdateIntervalHour, &r.transparentMode, &r.lanSharingEnabled, &r.socks5Port,
		&r.httpPort, &r.maxLogLines, &r.maxLogFileSize, &r.downloadProxy, &r.githubMirror, &r.probeTargets,
		&r.groupRealProbeIntervalSec, &r.groupProbeMode, &r.groupProbeConcurrency, &r.groupSwitchThresholdMs,
		&r.groupSwitchCooldownSec, &r.groupActiveNodeProbeEnabled, &r.groupActiveNodeProbeIntervalSec,
		&r.nodeCleanupRules, &r.nodeCleanupMode, &r.nodeCleanupIntervalHour, &r.nodeCleanupGroupIDs,
		&r.nodeCleanupRecycleGroupID, &r.subscriptionBestNodeCopyRules, &r.ipScoreEnabled, &r.ipScoreProvider,
		&r.ipScoreEndpoint, &r.ipScoreMinIntervalMs, &r.socks5Username, &r.socks5Password, &r.httpUsername,
		&r.httpPassword, &r.socks5Accounts, &r.httpAccounts, &r.webUsername, &r.webPassword, &r.sharedNodesFixed); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r settingRow) apply(s *Setting) {
	s.LogLevel = r.logLevel
	s.KernelMode = r.kernelMode
	s.EnableSniff = r.enableSniff
	if r.socks5SniffEnabled.Valid {
		v := r.socks5SniffEnabled.Int64 != 0
		s.Socks5SniffEnabled = &v
	}
	if r.httpSniffEnabled.Valid {
		v := r.httpSniffEnabled.Int64 != 0
		s.HttpSniffEnabled = &v
	}
	s.EnableHijackDNS = r.enableHijackDNS
	s.DNSMode = r.dnsMode
	s.DNSRemoteServer = r.dnsRemoteServer
	s.SubscriptionAutoUpdateMode = r.subscriptionAutoUpdateMode
	s.SubscriptionAutoUpdateIntervalHour = r.subscriptionAutoUpdateIntervalHour
	s.TransparentMode = r.transparentMode
	s.LanSharingEnabled = r.lanSharingEnabled
	s.Socks5Port = r.socks5Port
	s.HttpPort = r.httpPort
	s.MaxLogLines = r.maxLogLines
	s.MaxLogFileSize = r.maxLogFileSize
	s.DownloadProxy = r.downloadProxy
	s.GithubMirror = r.githubMirror
	s.ProbeTargets = r.probeTargets
	s.GroupRealProbeIntervalSec = r.groupRealProbeIntervalSec
	s.GroupProbeMode = r.groupProbeMode
	s.GroupProbeConcurrency = r.groupProbeConcurrency
	s.GroupSwitchThresholdMs = r.groupSwitchThresholdMs
	s.GroupSwitchCooldownSec = r.groupSwitchCooldownSec
	if r.groupActiveNodeProbeEnabled.Valid {
		v := r.groupActiveNodeProbeEnabled.Int64 != 0
		s.GroupActiveNodeProbeEnabled = &v
	}
	s.GroupActiveNodeProbeIntervalSec = r.groupActiveNodeProbeIntervalSec
	s.NodeCleanupMode = r.nodeCleanupMode
	s.NodeCleanupIntervalHour = r.nodeCleanupIntervalHour
	s.NodeCleanupRecycleGroupID = r.nodeCleanupRecycleGroupID
	s.IPScoreEnabled = r.ipScoreEnabled
	s.IPScoreProvider = r.ipScoreProvider
	s.IPScoreEndpoint = r.ipScoreEndpoint
	s.IPScoreMinIntervalMs = r.ipScoreMinIntervalMs
	s.Socks5Username = r.socks5Username
	s.Socks5Password = r.socks5Password
	s.HttpUsername = r.httpUsername
	s.HttpPassword = r.httpPassword
	s.WebUsername = r.webUsername
	s.WebPassword = r.webPassword
	s.SharedNodesFixed = r.sharedNodesFixed
	_ = json.Unmarshal([]byte(r.nodeCleanupRules), &s.NodeCleanupRules)
	_ = json.Unmarshal([]byte(r.nodeCleanupGroupIDs), &s.NodeCleanupGroupIDs)
	_ = json.Unmarshal([]byte(r.subscriptionBestNodeCopyRules), &s.SubscriptionBestNodeCopyRules)
	_ = json.Unmarshal([]byte(r.socks5Accounts), &s.Socks5Accounts)
	_ = json.Unmarshal([]byte(r.httpAccounts), &s.HttpAccounts)
}

func settingGetRow() (*settingRow, error) {
	row := db.ReadDB().QueryRow(`SELECT ` + settingColumns + ` FROM setting WHERE id = 1`)
	r, err := scanSettingRow(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func settingSet(s *Setting) error {
	r := settingRowFrom(s)
	args := []any{
		r.logLevel, r.kernelMode, r.enableSniff, r.socks5SniffEnabled, r.httpSniffEnabled, r.enableHijackDNS,
		r.dnsMode, r.dnsRemoteServer, r.subscriptionAutoUpdateMode, r.subscriptionAutoUpdateIntervalHour,
		r.transparentMode, r.lanSharingEnabled, r.socks5Port, r.httpPort, r.maxLogLines, r.maxLogFileSize,
		r.downloadProxy, r.githubMirror, r.probeTargets, r.groupRealProbeIntervalSec, r.groupProbeMode,
		r.groupProbeConcurrency, r.groupSwitchThresholdMs, r.groupSwitchCooldownSec, r.groupActiveNodeProbeEnabled,
		r.groupActiveNodeProbeIntervalSec, r.nodeCleanupRules, r.nodeCleanupMode, r.nodeCleanupIntervalHour,
		r.nodeCleanupGroupIDs, r.nodeCleanupRecycleGroupID, r.subscriptionBestNodeCopyRules, r.ipScoreEnabled,
		r.ipScoreProvider, r.ipScoreEndpoint, r.ipScoreMinIntervalMs, r.socks5Username, r.socks5Password,
		r.httpUsername, r.httpPassword, r.socks5Accounts, r.httpAccounts, r.webUsername, r.webPassword,
		r.sharedNodesFixed,
	}
	return db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO setting(id, `+settingColumns+`) VALUES(1, `+placeholders(45)+`)
			ON CONFLICT(id) DO UPDATE SET `+settingUpdateSet()+``, args...)
		return err
	})
}

func settingUpdateSet() string {
	cols := []string{"log_level", "kernel_mode", "enable_sniff", "socks5_sniff_enabled", "http_sniff_enabled",
		"enable_hijack_dns", "dns_mode", "dns_remote_server", "subscription_auto_update_mode",
		"subscription_auto_update_interval_hour", "transparent_mode", "lan_sharing_enabled", "socks5_port",
		"http_port", "max_log_lines", "max_log_file_size", "download_proxy", "github_mirror", "probe_targets",
		"group_real_probe_interval_sec", "group_probe_mode", "group_probe_concurrency", "group_switch_threshold_ms",
		"group_switch_cooldown_sec", "group_active_node_probe_enabled", "group_active_node_probe_interval_sec",
		"node_cleanup_rules", "node_cleanup_mode", "node_cleanup_interval_hour", "node_cleanup_group_ids",
		"node_cleanup_recycle_group_id", "subscription_best_node_copy_rules", "ip_score_enabled",
		"ip_score_provider", "ip_score_endpoint", "ip_score_min_interval_ms", "socks5_username", "socks5_password",
		"http_username", "http_password", "socks5_accounts", "http_accounts", "web_username", "web_password",
		"shared_nodes_fixed"}
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = c + " = excluded." + c
	}
	return strings.Join(parts, ", ")
}

// MigrateSettingStorage 把 setting 从 kv 里的单个 JSON 迁移到单行列式表，并把旧 JSON
// 里缺失的字段补齐为默认值后落库。幂等：kv 里没有 setting 键时跳过。
func MigrateSettingStorage() error {
	raw, err := db.GetRaw("system", "setting")
	if err != nil {
		return nil
	}
	s := NewSetting()
	if err := json.Unmarshal(raw, s); err != nil {
		return nil
	}
	normalizeSetting(s)
	if err := settingSet(s); err != nil {
		return err
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, "system", "setting")
		return err
	}); err != nil {
		return err
	}
	log.Printf("已把全局设置迁移到列式存储")
	return nil
}
