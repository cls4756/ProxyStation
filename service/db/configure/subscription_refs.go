package configure

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ProxyStation/proxystation/db"
)

func removeSubscriptionsWithRefs(indexes []int) error {
	unique := dedupeInts(indexes)
	if len(unique) == 0 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.IntSlice(unique)))
	err := db.WriteTx(func(tx *sql.Tx) error {
		for _, index := range unique {
			var id, groupID string
			err := tx.QueryRow(`SELECT id, group_id FROM subscriptions WHERE idx = ?`, index).Scan(&id, &groupID)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM group_servers WHERE type = 'sub_server' AND sub = ?`, index); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE group_servers SET sub = sub - 1 WHERE type = 'sub_server' AND sub > ?`, index); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE outbounds SET target_type = 'none', node_ref = NULL,
				active_node_ref = NULL, group_id = '', mode = ''
				WHERE (target_type = 'node' AND json_extract(node_ref, '$.type') = 'sub_server'
					AND COALESCE(json_extract(node_ref, '$.sub'), 0) = ?)
				OR (target_type = 'group' AND group_id != '' AND group_id = ?)`, index, groupID); err != nil {
				return err
			}
			for _, column := range []string{"node_ref", "active_node_ref"} {
				if _, err := tx.Exec(`UPDATE outbounds SET `+column+` = CASE
					WHEN COALESCE(json_extract(`+column+`, '$.sub'), 0) = ? THEN NULL
					ELSE json_set(`+column+`, '$.sub', COALESCE(json_extract(`+column+`, '$.sub'), 0) - 1) END
					WHERE json_extract(`+column+`, '$.type') = 'sub_server'
					AND COALESCE(json_extract(`+column+`, '$.sub'), 0) >= ?`, index, index); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(`DELETE FROM nodes WHERE bucket = ? AND key = ?`, nodeBucket, subscriptionServerKey(id)); err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM subscriptions WHERE idx = ?`, index); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE subscriptions SET idx = idx - 1 WHERE idx > ?`, index); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		invalidateGroupRefIndex()
	}
	return err
}

func setSubscriptionWithID(index int, sub *SubscriptionRaw, refresh bool) error {
	servers := append([]ServerRaw{}, sub.Servers...)
	groupID := sub.GroupID
	err := db.WriteTx(func(tx *sql.Tx) error {
		var currentID, currentURL, currentFormat, currentGroupID string
		if err := tx.QueryRow(`SELECT id, url, format, group_id FROM subscriptions WHERE idx = ?`, index).
			Scan(&currentID, &currentURL, &currentFormat, &currentGroupID); err != nil {
			return err
		}
		if currentID != sub.ID || (refresh && (currentURL != sub.URL ||
			(currentFormat != string(FormatAuto) && currentFormat != string(sub.Format)))) {
			return fmt.Errorf("subscription changed; stale update discarded")
		}
		rows, err := tx.Query(`SELECT `+nodeSelectColumns()+` FROM nodes WHERE bucket = ? AND key = ? ORDER BY idx`,
			nodeBucket, subscriptionServerKey(currentID))
		if err != nil {
			return err
		}
		current := make([]ServerRaw, 0)
		for rows.Next() {
			server, err := scanServerRow(rows.Scan)
			if err != nil {
				rows.Close()
				return err
			}
			current = append(current, server)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		PreserveServerRuntimeResults(current, servers)
		if refresh {
			servers, err = preserveActiveSubscriptionServersTx(tx, index, current, servers)
			if err != nil {
				return err
			}
			groupID = currentGroupID
			if _, err := tx.Exec(`UPDATE subscriptions SET updated_at = ?,
				format = CASE WHEN format = 'auto' THEN ? ELSE format END WHERE idx = ?`,
				timeToInt(sub.UpdatedAt), string(sub.Format), index); err != nil {
				return err
			}
		} else if err := subscriptionDAO.setTx(tx, index, subscriptionDocFrom(sub)); err != nil {
			return err
		}
		if err := nodeReplaceAllTx(tx, nodeBucket, subscriptionServerKey(currentID), servers); err != nil {
			return err
		}
		if refresh && groupID != "" {
			var found string
			err := tx.QueryRow(`SELECT id FROM groups WHERE id = ? AND from_sub = 1 AND sub_id = ?`,
				groupID, currentID).Scan(&found)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil {
				refs := make([]ServerRef, len(servers))
				for nodeIndex := range servers {
					refs[nodeIndex] = ServerRef{Type: "sub_server", Index: nodeIndex, Sub: index}
				}
				if err := groupServersReplaceTx(tx, groupID, refs); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		sub.Servers = servers
		sub.GroupID = groupID
		invalidateGroupRefIndex()
	}
	return err
}

func preserveActiveSubscriptionServersTx(tx *sql.Tx, subIndex int, current, incoming []ServerRaw) ([]ServerRaw, error) {
	rows, err := tx.Query(`SELECT name, node_ref, active_node_ref FROM outbounds
		WHERE (json_extract(node_ref, '$.type') = 'sub_server' AND COALESCE(json_extract(node_ref, '$.sub'), 0) = ?)
		OR (json_extract(active_node_ref, '$.type') = 'sub_server' AND COALESCE(json_extract(active_node_ref, '$.sub'), 0) = ?)`,
		subIndex, subIndex)
	if err != nil {
		return nil, err
	}
	type activeTarget struct {
		name   string
		node   *NodeRef
		active *NodeRef
	}
	targets := make([]activeTarget, 0)
	for rows.Next() {
		var target activeTarget
		var node, active sql.NullString
		if err := rows.Scan(&target.name, &node, &active); err != nil {
			rows.Close()
			return nil, err
		}
		if node.Valid {
			if err := json.Unmarshal([]byte(node.String), &target.node); err != nil {
				rows.Close()
				return nil, err
			}
		}
		if active.Valid {
			if err := json.Unmarshal([]byte(active.String), &target.active); err != nil {
				rows.Close()
				return nil, err
			}
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	matches := matchOldServerIndexes(current, incoming)
	appended := make(map[int]int)
	for _, target := range targets {
		nodeChanged := remapActiveSubscriptionRef(target.node, subIndex, current, &incoming, matches, appended)
		activeChanged := remapActiveSubscriptionRef(target.active, subIndex, current, &incoming, matches, appended)
		if !nodeChanged && !activeChanged {
			continue
		}
		if _, err := tx.Exec(`UPDATE outbounds SET node_ref = ?, active_node_ref = ? WHERE name = ?`,
			jsonOrNull(target.node), jsonOrNull(target.active), target.name); err != nil {
			return nil, err
		}
	}
	return incoming, nil
}
