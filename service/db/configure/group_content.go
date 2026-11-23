package configure

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ProxyStation/proxystation/db"
)

const nodeRefQueryBatchSize = 250

func nodeConfigsByServerRefsTx(tx *sql.Tx, refs []ServerRef) (map[ServerRef]ServerRaw, error) {
	return nodesByServerRefsTx(tx, refs, false)
}

func nodesByServerRefsTx(tx *sql.Tx, refs []ServerRef, full bool) (map[ServerRef]ServerRaw, error) {
	result := make(map[ServerRef]ServerRaw, len(refs))
	unique := make([]ServerRef, 0, len(refs))
	seen := make(map[ServerRef]bool, len(refs))
	for _, ref := range refs {
		if ref.Type == "server" {
			ref.Sub = 0
		}
		if seen[ref] || ref.Index < 0 || (ref.Type != "server" && ref.Type != "sub_server") {
			continue
		}
		seen[ref] = true
		unique = append(unique, ref)
	}
	for offset := 0; offset < len(unique); offset += nodeRefQueryBatchSize {
		end := offset + nodeRefQueryBatchSize
		if end > len(unique) {
			end = len(unique)
		}
		query, args := nodeRefsQuery(unique[offset:end], full)
		rows, err := tx.Query(query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ref ServerRef
			var server ServerRaw
			var scanErr error
			if full {
				server, scanErr = scanServerRow(func(dest ...any) error {
					return rows.Scan(append([]any{&ref.Type, &ref.Index, &ref.Sub}, dest...)...)
				})
			} else {
				scanErr = rows.Scan(&ref.Type, &ref.Index, &ref.Sub,
					&server.Link, &server.Host, &server.Port, &server.Type, &server.FrontProxy)
			}
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			result[ref] = server
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}

func nodeRefsQuery(refs []ServerRef, full bool) (string, []any) {
	values := make([]string, 0, len(refs))
	args := make([]any, 0, len(refs)*3)
	for _, ref := range refs {
		values = append(values, "(?, ?, ?)")
		args = append(args, ref.Type, ref.Index, ref.Sub)
	}
	columns := "nodes.link, nodes.host, nodes.port, nodes.type, nodes.front_proxy"
	if full {
		columns = "nodes." + strings.ReplaceAll(nodeSelectColumns(), ", ", ", nodes.")
	}
	query := `WITH wanted(type, ref_index, sub) AS (VALUES ` + strings.Join(values, ",") + `)
		SELECT wanted.type, wanted.ref_index, wanted.sub, ` + columns + `
		FROM wanted
		LEFT JOIN subscriptions ON wanted.type = 'sub_server' AND subscriptions.idx = wanted.sub
		JOIN nodes ON nodes.bucket = 'touch' AND nodes.idx = wanted.ref_index
			AND nodes.key = CASE WHEN wanted.type = 'server' THEN 'servers'
				ELSE 'sub_servers.' || subscriptions.id END`
	return query, args
}

func uniqueGroupContentRefsTx(tx *sql.Tx, existing, incoming []ServerRef) ([]ServerRef, error) {
	configs, err := nodeConfigsByServerRefsTx(tx, append(append([]ServerRef{}, existing...), incoming...))
	if err != nil {
		return nil, err
	}
	seenRefs := make(map[ServerRef]bool, len(existing)+len(incoming))
	seenContent := make(map[string]bool, len(existing)+len(incoming))
	add := func(ref ServerRef) bool {
		if ref.Type == "server" {
			ref.Sub = 0
		}
		if seenRefs[ref] {
			return false
		}
		seenRefs[ref] = true
		if server, ok := configs[ref]; ok {
			if fingerprint, valid := NodeConfigFingerprint(server); valid {
				if seenContent[fingerprint] {
					return false
				}
				seenContent[fingerprint] = true
			}
		}
		return true
	}
	for _, ref := range existing {
		add(ref)
	}
	result := make([]ServerRef, 0, len(incoming))
	for _, ref := range incoming {
		if add(ref) {
			if ref.Type == "server" {
				ref.Sub = 0
			}
			result = append(result, ref)
		}
	}
	return result, nil
}

func CloneServersToGroup(groupID string, refs []ServerRef) ([]NodeRef, int, error) {
	created := make([]NodeRef, 0, len(refs))
	total := 0
	err := db.WriteTx(func(tx *sql.Tx) error {
		existing, err := groupRefsTx(tx, groupID)
		if err != nil {
			return err
		}
		unique, err := uniqueGroupContentRefsTx(tx, existing, refs)
		if err != nil {
			return err
		}
		total = len(existing)
		if len(unique) == 0 {
			return nil
		}
		clones, err := cloneServerRefsTx(tx, unique)
		if err != nil {
			return err
		}
		for _, ref := range clones {
			created = append(created, NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
		}
		var next int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(ref_idx), -1) + 1 FROM group_servers WHERE group_id = ?`,
			groupID).Scan(&next); err != nil {
			return err
		}
		if err := groupServersInsertTx(tx, groupID, clones, next); err != nil {
			return err
		}
		total = len(existing) + len(clones)
		return nil
	})
	if err == nil && len(created) > 0 {
		invalidateGroupRefIndex()
	}
	if err != nil {
		return nil, 0, err
	}
	return created, total, err
}

func groupRefsTx(tx *sql.Tx, groupID string) ([]ServerRef, error) {
	var found string
	if err := tx.QueryRow(`SELECT id FROM groups WHERE id = ?`, groupID).Scan(&found); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT type, ref_index, sub FROM group_servers WHERE group_id = ? ORDER BY ref_idx`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]ServerRef, 0)
	for rows.Next() {
		var ref ServerRef
		if err := rows.Scan(&ref.Type, &ref.Index, &ref.Sub); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func MoveServersToGroup(sourceID, targetID string, refs []ServerRef) (int, int, int, error) {
	sourceTotal, targetTotal, moved := 0, 0, 0
	err := db.WriteTx(func(tx *sql.Tx) error {
		source, err := groupRefsTx(tx, sourceID)
		if err != nil {
			return err
		}
		target, err := groupRefsTx(tx, targetID)
		if err != nil {
			return err
		}
		sourceTotal, targetTotal = len(source), len(target)
		if sourceID == targetID {
			return nil
		}
		membership := make(map[ServerRef]bool, len(source))
		for _, ref := range source {
			membership[ref] = true
		}
		for _, ref := range refs {
			if ref.Type == "server" {
				ref.Sub = 0
			}
			if !membership[ref] {
				return fmt.Errorf("node reference is not in source group: %w", sql.ErrNoRows)
			}
		}
		unique, err := uniqueGroupContentRefsTx(tx, target, refs)
		if err != nil {
			return err
		}
		var next int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(ref_idx), -1) + 1 FROM group_servers WHERE group_id = ?`,
			targetID).Scan(&next); err != nil {
			return err
		}
		if err := groupServersRemoveTx(tx, sourceID, unique); err != nil {
			return err
		}
		independent, remapped, err := independentGroupRefsTx(tx, targetID, unique)
		if err != nil {
			return err
		}
		if err := groupServersInsertTx(tx, targetID, independent, next); err != nil {
			return err
		}
		if err := retargetIndependentGroupOutboundsTx(tx, targetID, remapped, nil); err != nil {
			return err
		}
		moved = len(unique)
		if err := tx.QueryRow(`SELECT COUNT(*) FROM group_servers WHERE group_id = ?`, sourceID).Scan(&sourceTotal); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT COUNT(*) FROM group_servers WHERE group_id = ?`, targetID).Scan(&targetTotal); err != nil {
			return err
		}
		return nil
	})
	if err == nil && moved > 0 {
		invalidateGroupRefIndex()
	}
	return sourceTotal, targetTotal, moved, err
}

func RemoveGroupNodeRefs(groupIndex int, refs []ServerRef) (string, int, error) {
	row, err := groupDAO.get(groupIndex)
	if err != nil {
		return "", 0, err
	}
	var total int
	err = db.WriteTx(func(tx *sql.Tx) error {
		if err := groupServersRemoveTx(tx, row.id, refs); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM group_servers WHERE group_id = ?`, row.id).Scan(&total)
	})
	if err == nil {
		invalidateGroupRefIndex()
	}
	return row.id, total, err
}
