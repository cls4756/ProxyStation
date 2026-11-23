package configure

import (
	"database/sql"
	"encoding/json"
	"log"

	"github.com/ProxyStation/proxystation/db"
)

// custom_inbounds 表把入站的顶层字段按列存储。协议专属的嵌套选项
// （vmess/cfdo/cfgoodnet/accounts/sniff_dest）始终与父行一起读写、从不单独
// 过滤或排序，因此保留 JSON 列，不再把整个入站集合塞进一个 kv 值。
type customInboundRow struct {
	id             string
	tag            string
	name           string
	protocol       string
	listen         string
	port           int
	udpEnabled     bool
	network        string
	followRedirect bool
	sniffEnabled   bool
	sniffDest      string
	username       string
	password       string
	accounts       string
	vmess          sql.NullString
	cfdo           sql.NullString
	cfgoodnet      sql.NullString
}

var customInboundDAO = idxRecordDAO[customInboundRow]{
	table: "custom_inbounds",
	columns: []string{
		"id", "tag", "name", "protocol", "listen", "port", "udp_enabled",
		"network", "follow_redirect", "sniff_enabled", "sniff_dest",
		"username", "password", "accounts", "vmess", "cfdo", "cfgoodnet",
	},
	args: func(r customInboundRow) []any {
		return []any{
			r.id, r.tag, r.name, r.protocol, r.listen, r.port, r.udpEnabled,
			r.network, r.followRedirect, r.sniffEnabled, r.sniffDest,
			r.username, r.password, r.accounts, r.vmess, r.cfdo, r.cfgoodnet,
		}
	},
	scan: func(scan func(...any) error) (customInboundRow, error) {
		var r customInboundRow
		if err := scan(
			&r.id, &r.tag, &r.name, &r.protocol, &r.listen, &r.port, &r.udpEnabled,
			&r.network, &r.followRedirect, &r.sniffEnabled, &r.sniffDest,
			&r.username, &r.password, &r.accounts, &r.vmess, &r.cfdo, &r.cfgoodnet,
		); err != nil {
			return r, err
		}
		return r, nil
	},
}

func customInboundRowFrom(in CustomInbound) customInboundRow {
	sniffDest := "[]"
	if len(in.SniffDest) > 0 {
		if b, err := json.Marshal(in.SniffDest); err == nil {
			sniffDest = string(b)
		}
	}
	accounts := "[]"
	if len(in.Accounts) > 0 {
		if b, err := json.Marshal(in.Accounts); err == nil {
			accounts = string(b)
		}
	}
	var vmess, cfdo, cfgoodnet sql.NullString
	if in.VMess != nil {
		if b, err := json.Marshal(in.VMess); err == nil {
			vmess = sql.NullString{String: string(b), Valid: true}
		}
	}
	if in.CfDO != nil {
		if b, err := json.Marshal(in.CfDO); err == nil {
			cfdo = sql.NullString{String: string(b), Valid: true}
		}
	}
	if in.CfGoodNet != nil {
		if b, err := json.Marshal(in.CfGoodNet); err == nil {
			cfgoodnet = sql.NullString{String: string(b), Valid: true}
		}
	}
	return customInboundRow{
		id: in.ID, tag: in.Tag, name: in.Name, protocol: in.Protocol, listen: in.Listen, port: in.Port,
		udpEnabled: in.UDPEnabled, network: in.Network, followRedirect: in.FollowRedirect,
		sniffEnabled: in.SniffEnabled, sniffDest: sniffDest, username: in.Username, password: in.Password,
		accounts: accounts, vmess: vmess, cfdo: cfdo, cfgoodnet: cfgoodnet,
	}
}

func (r customInboundRow) toInbound() CustomInbound {
	in := CustomInbound{
		ID: r.id, Tag: r.tag, Name: r.name, Protocol: r.protocol, Listen: r.listen, Port: r.port,
		UDPEnabled: r.udpEnabled, Network: r.network, FollowRedirect: r.followRedirect,
		SniffEnabled: r.sniffEnabled, Username: r.username, Password: r.password,
	}
	if r.sniffDest != "" {
		_ = json.Unmarshal([]byte(r.sniffDest), &in.SniffDest)
	}
	if r.accounts != "" {
		_ = json.Unmarshal([]byte(r.accounts), &in.Accounts)
	}
	if r.vmess.Valid {
		in.VMess = &VMessInbound{}
		_ = json.Unmarshal([]byte(r.vmess.String), in.VMess)
	}
	if r.cfdo.Valid {
		in.CfDO = &CfDOInbound{}
		_ = json.Unmarshal([]byte(r.cfdo.String), in.CfDO)
	}
	if r.cfgoodnet.Valid {
		in.CfGoodNet = &CfGoodNetInbound{}
		_ = json.Unmarshal([]byte(r.cfgoodnet.String), in.CfGoodNet)
	}
	return in
}

func loadCustomInbounds() []CustomInbound {
	rows, err := customInboundDAO.getAll()
	if err != nil {
		return []CustomInbound{}
	}
	result := make([]CustomInbound, 0, len(rows))
	for _, r := range rows {
		result = append(result, r.toInbound())
	}
	return result
}

func replaceCustomInbounds(inbounds []CustomInbound) error {
	rows := make([]customInboundRow, len(inbounds))
	for i := range inbounds {
		rows[i] = customInboundRowFrom(inbounds[i])
	}
	return customInboundDAO.replaceAll(rows)
}

// MigrateCustomInboundStorage 把 custom_inbounds 从 kv 里的单个 JSON 数组迁移到列式表。
func MigrateCustomInboundStorage() error {
	raw, err := db.GetRaw("system", "custom_inbounds")
	if err != nil {
		return nil
	}
	var inbounds []CustomInbound
	if err := json.Unmarshal(raw, &inbounds); err != nil {
		return nil
	}
	if err := replaceCustomInbounds(inbounds); err != nil {
		return err
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM kv WHERE bucket = ? AND key = ?`, "system", "custom_inbounds"); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM list_items WHERE bucket = ? AND key = ?`, "system", "custom_inbounds")
		return err
	}); err != nil {
		return err
	}
	if len(inbounds) > 0 {
		log.Printf("已把 %d 个自定义入站迁移到列式存储", len(inbounds))
	}
	return nil
}
