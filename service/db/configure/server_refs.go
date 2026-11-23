package configure

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ProxyStation/proxystation/db"
)

func removeManualServersWithRefs(indexes []int) error {
	if len(indexes) == 0 {
		return nil
	}
	err := db.WriteTx(func(tx *sql.Tx) error {
		return removeManualServersWithRefsTx(tx, indexes)
	})
	if err == nil {
		invalidateGroupRefIndex()
	}
	return err
}

func removeManualServersWithRefsTx(tx *sql.Tx, indexes []int) error {
	unique := dedupeInts(indexes)
	sort.Sort(sort.Reverse(sort.IntSlice(unique)))
	for _, index := range unique {
		result, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ? AND idx = ?`,
			nodeBucket, manualServersKey, index)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM group_servers WHERE type = 'server' AND ref_index = ?`, index); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE group_servers SET ref_index = ref_index - 1
			WHERE type = 'server' AND ref_index > ?`, index); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE outbounds SET target_type = 'none', node_ref = NULL,
			active_node_ref = NULL, group_id = '', mode = ''
			WHERE target_type = 'node' AND json_extract(node_ref, '$.type') = 'server'
			AND COALESCE(json_extract(node_ref, '$.index'), 0) = ?`, index); err != nil {
			return err
		}
		for _, column := range []string{"node_ref", "active_node_ref"} {
			if _, err := tx.Exec(`UPDATE outbounds SET `+column+` = CASE
				WHEN COALESCE(json_extract(`+column+`, '$.index'), 0) = ? THEN NULL
				ELSE json_set(`+column+`, '$.index', COALESCE(json_extract(`+column+`, '$.index'), 0) - 1) END
				WHERE json_extract(`+column+`, '$.type') = 'server'
				AND COALESCE(json_extract(`+column+`, '$.index'), 0) >= ?`, index, index); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE nodes SET idx = idx - 1 WHERE bucket = ? AND key = ? AND idx > ?`,
			nodeBucket, manualServersKey, index); err != nil {
			return err
		}
	}
	return nil
}

type inboundProxyNodeRow struct {
	index  int
	server ServerRaw
}

func inboundProxyNodeRowsTx(tx *sql.Tx) ([]inboundProxyNodeRow, error) {
	rows, err := tx.Query(`SELECT idx, `+nodeSelectColumns()+` FROM nodes
		WHERE bucket = ? AND key = ? AND source GLOB 'inbound:*' ORDER BY idx`,
		nodeBucket, manualServersKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]inboundProxyNodeRow, 0)
	for rows.Next() {
		var row inboundProxyNodeRow
		server, err := scanServerRow(func(dest ...any) error {
			return rows.Scan(append([]any{&row.index}, dest...)...)
		})
		if err != nil {
			return nil, err
		}
		row.server = server
		result = append(result, row)
	}
	return result, rows.Err()
}

func SyncInboundProxyServers(generated []ServerRaw) error {
	desired := make([]ServerRaw, 0, len(generated))
	sources := make(map[string]bool, len(generated))
	for _, server := range generated {
		if !strings.HasPrefix(server.Source, "inbound:") {
			return fmt.Errorf("invalid inbound proxy node source")
		}
		if sources[server.Source] {
			continue
		}
		sources[server.Source] = true
		desired = append(desired, server)
	}
	err := db.WriteTx(func(tx *sql.Tx) error {
		var groupID string
		err := tx.QueryRow(`SELECT id FROM groups WHERE from_sub = 0 AND lower(trim(name)) = 'server'
			ORDER BY idx LIMIT 1`).Scan(&groupID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows {
			group := &Group{Name: "SERVER", CreatedAt: time.Now()}
			ensureGroupID(group)
			if group.ID == "" {
				return fmt.Errorf("failed to create server group ID")
			}
			if err := groupDAO.appendTx(tx, []groupRow{groupRowFrom(group)}); err != nil {
				return err
			}
			groupID = group.ID
		}
		if _, err := tx.Exec(`DELETE FROM group_servers WHERE lower(trim(type)) = 'inbound'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE nodes SET source = 'manual' WHERE bucket = ? AND key = ?
			AND source GLOB 'inbound:*' AND EXISTS (
				SELECT 1 FROM group_servers AS refs WHERE refs.type = 'server'
				AND refs.ref_index = nodes.idx AND refs.group_id != ?
			)`, nodeBucket, manualServersKey, groupID); err != nil {
			return err
		}
		current, err := inboundProxyNodeRowsTx(tx)
		if err != nil {
			return err
		}
		retired := make([]int, 0)
		seen := make(map[string]bool, len(current))
		for _, row := range current {
			source := row.server.Source
			if !sources[source] || seen[source] {
				retired = append(retired, row.index)
			}
			seen[source] = true
		}
		if err := removeManualServersWithRefsTx(tx, retired); err != nil {
			return err
		}
		if len(retired) > 0 {
			current, err = inboundProxyNodeRowsTx(tx)
			if err != nil {
				return err
			}
		}
		existing := make(map[string]inboundProxyNodeRow, len(current))
		previous := make([]ServerRaw, 0, len(current))
		for _, row := range current {
			existing[row.server.Source] = row
			previous = append(previous, row.server)
		}
		PreserveServerRuntimeResults(previous, desired)
		var base int
		if len(desired) > len(current) {
			if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) + 1 FROM nodes WHERE bucket = ? AND key = ?`,
				nodeBucket, manualServersKey).Scan(&base); err != nil {
				return err
			}
		}
		refs := make([]ServerRef, 0, len(desired))
		for _, server := range desired {
			index := base
			if row, found := existing[server.Source]; found {
				index = row.index
				args := append(serverRowArgs(&server), nodeBucket, manualServersKey, index)
				if _, err := tx.Exec(`UPDATE nodes SET `+nodeUpdateSet()+`
					WHERE bucket = ? AND key = ? AND idx = ?`, args...); err != nil {
					return err
				}
			} else {
				args := append([]any{nodeBucket, manualServersKey, index}, serverRowArgs(&server)...)
				if _, err := tx.Exec(`INSERT INTO nodes(bucket, key, idx, `+nodeSelectColumns()+
					`) VALUES(`+placeholders(3+len(nodeColumns))+`)`, args...); err != nil {
					return err
				}
				base++
			}
			refs = append(refs, ServerRef{Type: "server", Index: index})
		}
		members, err := groupRefsTx(tx, groupID)
		if err != nil {
			return err
		}
		return groupServersReplaceTx(tx, groupID, append(members, refs...))
	})
	if err == nil {
		invalidateGroupRefIndex()
	}
	return err
}
