package configure

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type GroupDeduplicationResult struct {
	ID             string
	Name           string
	Before         int
	After          int
	Retargeted     int
	Repaired       int
	Missing        int
	Cloned         int
	fromSub        bool
	subIndex       int
	refs           []ServerRef
	active         map[string]ServerRef
	originalActive map[string]ServerRef
	missingRefs    map[ServerRef]bool
}

func normalizedGroupRef(ref ServerRef) ServerRef {
	if ref.Type == "server" {
		ref.Sub = 0
	}
	return ref
}

func groupActiveRefsTx(tx *sql.Tx, groupID string) (map[string]ServerRef, error) {
	rows, err := tx.Query(`SELECT name, active_node_ref FROM outbounds
		WHERE target_type = 'group' AND group_id = ? AND active_node_ref IS NOT NULL`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	active := make(map[string]ServerRef)
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return nil, err
		}
		var ref *NodeRef
		if err := json.Unmarshal([]byte(content), &ref); err != nil {
			return nil, fmt.Errorf("invalid active node reference for outbound %q", name)
		}
		if ref != nil {
			active[name] = normalizedGroupRef(ServerRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
		}
	}
	return active, rows.Err()
}

func deduplicateGroupRefsTx(tx *sql.Tx, groupID string, refs []ServerRef) ([]ServerRef, map[string]ServerRef, error) {
	configs, err := nodeConfigsByServerRefsTx(tx, refs)
	if err != nil {
		return nil, nil, err
	}
	outbounds, err := groupActiveRefsTx(tx, groupID)
	if err != nil {
		return nil, nil, err
	}
	unique, retargets := deduplicateGroupRefs(refs, configs, outbounds)
	return unique, retargets, nil
}

func deduplicateGroupRefs(refs []ServerRef, configs map[ServerRef]ServerRaw, outbounds map[string]ServerRef) ([]ServerRef, map[string]ServerRef) {
	active := make(map[ServerRef]bool, len(outbounds))
	for _, ref := range outbounds {
		active[ref] = true
	}
	fingerprints := make(map[ServerRef]string, len(configs))
	keepers := make(map[string]ServerRef, len(configs))
	for _, original := range refs {
		ref := normalizedGroupRef(original)
		server, exists := configs[ref]
		if !exists {
			continue
		}
		fingerprint, valid := NodeConfigFingerprint(server)
		if !valid {
			continue
		}
		fingerprints[ref] = fingerprint
		keeper, found := keepers[fingerprint]
		if !found || (active[ref] && !active[keeper]) {
			keepers[fingerprint] = ref
		}
	}
	unique := make([]ServerRef, 0, len(refs))
	seenRefs := make(map[ServerRef]bool, len(refs))
	for _, original := range refs {
		ref := normalizedGroupRef(original)
		if seenRefs[ref] {
			continue
		}
		seenRefs[ref] = true
		if fingerprint, valid := fingerprints[ref]; valid && keepers[fingerprint] != ref {
			continue
		}
		unique = append(unique, ref)
	}
	retargets := make(map[string]ServerRef)
	for name, ref := range outbounds {
		if fingerprint, valid := fingerprints[ref]; valid && keepers[fingerprint] != ref {
			retargets[name] = keepers[fingerprint]
		}
	}
	return unique, retargets
}

func retargetGroupOutboundsTx(tx *sql.Tx, groupID string, retargets map[string]ServerRef) error {
	for name, ref := range retargets {
		content, err := json.Marshal(NodeRef{Type: ref.Type, Index: ref.Index, Sub: ref.Sub})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE outbounds SET active_node_ref = ?
			WHERE name = ? AND target_type = 'group' AND group_id = ?`, string(content), name, groupID); err != nil {
			return err
		}
	}
	return nil
}

func DeduplicateGroups(tx *sql.Tx, apply bool) ([]GroupDeduplicationResult, error) {
	return maintainGroupRefs(tx, apply, false, false)
}

func RepairAndDeduplicateGroups(tx *sql.Tx, apply bool) ([]GroupDeduplicationResult, error) {
	return maintainGroupRefs(tx, apply, true, false)
}

func IsolateGroupNodes(tx *sql.Tx, apply bool) ([]GroupDeduplicationResult, error) {
	return maintainGroupRefs(tx, apply, false, true)
}

func RepairAndIsolateGroupNodes(tx *sql.Tx, apply bool) ([]GroupDeduplicationResult, error) {
	return maintainGroupRefs(tx, apply, true, true)
}

func maintainGroupRefs(tx *sql.Tx, apply, repair, isolate bool) ([]GroupDeduplicationResult, error) {
	rows, err := tx.Query(`SELECT groups.id, groups.name, groups.from_sub, COALESCE(subscriptions.idx, -1)
		FROM groups LEFT JOIN subscriptions ON groups.from_sub = 1 AND subscriptions.id = groups.sub_id
		ORDER BY groups.idx`)
	if err != nil {
		return nil, err
	}
	results := make([]GroupDeduplicationResult, 0)
	seen := make(map[string]bool)
	for rows.Next() {
		var result GroupDeduplicationResult
		if err := rows.Scan(&result.ID, &result.Name, &result.fromSub, &result.subIndex); err != nil {
			rows.Close()
			return nil, err
		}
		if seen[result.ID] {
			rows.Close()
			return nil, fmt.Errorf("ambiguous stable subscription binding for group %q", result.Name)
		}
		seen[result.ID] = true
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for index := range results {
		result := &results[index]
		refs, err := groupRefsTx(tx, result.ID)
		if err != nil {
			return nil, err
		}
		originalActive, err := groupActiveRefsTx(tx, result.ID)
		if err != nil {
			return nil, err
		}
		remapped := make(map[ServerRef]ServerRef)
		for refIndex, ref := range refs {
			if ref.Type == "server" && ref.Sub != 0 {
				refs[refIndex] = normalizedGroupRef(ref)
				result.Repaired++
			}
			if !result.fromSub || ref.Type != "sub_server" {
				continue
			}
			if result.subIndex < 0 {
				return nil, fmt.Errorf("missing stable subscription binding for group %q", result.Name)
			}
			if ref.Sub == result.subIndex {
				continue
			}
			if !repair {
				return nil, fmt.Errorf("stale subscription references in group %q; use --repair-subscription-refs", result.Name)
			}
			updated := ref
			updated.Sub = result.subIndex
			refs[refIndex] = updated
			remapped[ref] = updated
			result.Repaired += 1
		}
		projectedActive := make(map[string]ServerRef, len(originalActive))
		for name, ref := range originalActive {
			if updated, changed := remapped[ref]; changed {
				ref = updated
			}
			projectedActive[name] = ref
		}
		configs, err := nodeConfigsByServerRefsTx(tx, refs)
		if err != nil {
			return nil, err
		}
		unique, redirects := deduplicateGroupRefs(refs, configs, projectedActive)
		result.missingRefs = make(map[ServerRef]bool)
		for _, ref := range unique {
			if _, found := configs[ref]; !found {
				result.Missing += 1
				result.missingRefs[ref] = true
			}
		}
		for name := range projectedActive {
			if updated, changed := redirects[name]; changed {
				projectedActive[name] = updated
			}
		}
		result.Before, result.After = len(refs), len(unique)
		result.refs, result.active, result.originalActive = unique, projectedActive, originalActive
	}
	if isolate {
		if err := isolateMaintainedGroupNodesTx(tx, results, apply); err != nil {
			return nil, err
		}
	}
	for index := range results {
		result := &results[index]
		retargets := make(map[string]ServerRef)
		for name, ref := range result.active {
			if ref != result.originalActive[name] {
				retargets[name] = ref
			}
		}
		result.Retargeted = len(retargets)
		if !apply || (result.Before == result.After && result.Repaired == 0 && result.Cloned == 0 && result.Retargeted == 0) {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM group_servers WHERE group_id = ?`, result.ID); err != nil {
			return nil, err
		}
		if err := groupServersInsertTx(tx, result.ID, result.refs, 0); err != nil {
			return nil, err
		}
		if err := retargetGroupOutboundsTx(tx, result.ID, retargets); err != nil {
			return nil, err
		}
	}
	return results, nil
}
