package configure

import (
	"database/sql"
	"fmt"
	"strings"
)

func cloneServerRefsTx(tx *sql.Tx, refs []ServerRef) ([]ServerRef, error) {
	if len(refs) == 0 {
		return []ServerRef{}, nil
	}
	servers, err := nodesByServerRefsTx(tx, refs, true)
	if err != nil {
		return nil, err
	}
	var base int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) + 1 FROM nodes WHERE bucket = ? AND key = ?`,
		nodeBucket, manualServersKey).Scan(&base); err != nil {
		return nil, err
	}
	insert, err := tx.Prepare(`INSERT INTO nodes(bucket, key, idx, ` + nodeSelectColumns() +
		`) VALUES(` + placeholders(3+len(nodeColumns)) + `)`)
	if err != nil {
		return nil, err
	}
	defer insert.Close()
	clones := make([]ServerRef, 0, len(refs))
	for _, original := range refs {
		ref := normalizedGroupRef(original)
		server, found := servers[ref]
		if !found {
			return nil, fmt.Errorf("node reference not found: %w", sql.ErrNoRows)
		}
		server.Source = "manual"
		index := base + len(clones)
		args := append([]any{nodeBucket, manualServersKey, index}, serverRowArgs(&server)...)
		if _, err := insert.Exec(args...); err != nil {
			return nil, err
		}
		clones = append(clones, ServerRef{Type: "server", Index: index})
	}
	return clones, nil
}

func independentGroupRefsTx(tx *sql.Tx, groupID string, refs []ServerRef) ([]ServerRef, map[ServerRef]ServerRef, error) {
	result := make([]ServerRef, len(refs))
	remapped := make(map[ServerRef]ServerRef)
	if len(refs) == 0 {
		return result, remapped, nil
	}
	var fromSub bool
	var subIndex int
	if err := tx.QueryRow(`SELECT groups.from_sub, COALESCE(subscriptions.idx, -1)
		FROM groups LEFT JOIN subscriptions ON groups.from_sub = 1 AND subscriptions.id = groups.sub_id
		WHERE groups.id = ?`, groupID).Scan(&fromSub, &subIndex); err != nil {
		return nil, nil, err
	}
	candidates := make([]ServerRef, 0, len(refs))
	needsClone := make(map[ServerRef]bool)
	for index, original := range refs {
		ref := normalizedGroupRef(original)
		result[index] = ref
		if ref.Type == "sub_server" {
			if !fromSub || ref.Sub != subIndex {
				needsClone[ref] = true
			} else {
				candidates = append(candidates, ref)
			}
		} else if ref.Type == "server" {
			candidates = append(candidates, ref)
		}
	}
	for offset := 0; offset < len(candidates); offset += nodeRefQueryBatchSize {
		end := offset + nodeRefQueryBatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		values := make([]string, 0, end-offset)
		args := make([]any, 0, (end-offset)*3+1)
		for _, ref := range candidates[offset:end] {
			values = append(values, "(?, ?, ?)")
			args = append(args, ref.Type, ref.Index, ref.Sub)
		}
		args = append(args, groupID)
		rows, err := tx.Query(`WITH wanted(type, ref_index, sub) AS (VALUES `+strings.Join(values, ",")+`)
			SELECT wanted.type, wanted.ref_index, wanted.sub FROM wanted WHERE EXISTS (
				SELECT 1 FROM group_servers AS refs WHERE refs.type = wanted.type
				AND refs.ref_index = wanted.ref_index
				AND (refs.type = 'server' OR refs.sub = wanted.sub) AND refs.group_id != ?
			)`, args...)
		if err != nil {
			return nil, nil, err
		}
		for rows.Next() {
			var ref ServerRef
			if err := rows.Scan(&ref.Type, &ref.Index, &ref.Sub); err != nil {
				rows.Close()
				return nil, nil, err
			}
			needsClone[ref] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, err
		}
		rows.Close()
	}
	originals := make([]ServerRef, 0, len(needsClone))
	for _, ref := range result {
		if needsClone[ref] {
			originals = append(originals, ref)
		}
	}
	clones, err := cloneServerRefsTx(tx, originals)
	if err != nil {
		return nil, nil, err
	}
	for index, original := range originals {
		remapped[original] = clones[index]
	}
	for index, ref := range result {
		if clone, changed := remapped[ref]; changed {
			result[index] = clone
		}
	}
	return result, remapped, nil
}

func retargetIndependentGroupOutboundsTx(tx *sql.Tx, groupID string, remapped map[ServerRef]ServerRef, redirects map[string]ServerRef) error {
	if len(remapped) > 0 {
		active, err := groupActiveRefsTx(tx, groupID)
		if err != nil {
			return err
		}
		if redirects == nil {
			redirects = make(map[string]ServerRef)
		}
		for name, original := range active {
			ref := original
			if redirected, changed := redirects[name]; changed {
				ref = redirected
			}
			if clone, changed := remapped[ref]; changed {
				redirects[name] = clone
			}
		}
	}
	return retargetGroupOutboundsTx(tx, groupID, redirects)
}

func isolateMaintainedGroupNodesTx(tx *sql.Tx, results []GroupDeduplicationResult, apply bool) error {
	owners := make(map[ServerRef]int)
	preferred := make(map[ServerRef]bool)
	for groupIndex, result := range results {
		active := make(map[ServerRef]bool, len(result.active))
		for _, ref := range result.active {
			active[ref] = true
		}
		for _, ref := range result.refs {
			if ref.Type == "sub_server" && (!result.fromSub || ref.Sub != result.subIndex) {
				continue
			}
			_, found := owners[ref]
			if !found || (active[ref] && !preferred[ref]) {
				owners[ref] = groupIndex
				preferred[ref] = active[ref]
			}
		}
	}
	type cloneTarget struct {
		groupIndex int
		refIndex   int
		original   ServerRef
	}
	targets := make([]cloneTarget, 0)
	originals := make([]ServerRef, 0)
	for groupIndex := range results {
		result := &results[groupIndex]
		for refIndex, ref := range result.refs {
			foreign := ref.Type == "sub_server" && (!result.fromSub || ref.Sub != result.subIndex)
			if !foreign && owners[ref] == groupIndex {
				continue
			}
			if result.missingRefs[ref] {
				return fmt.Errorf("cannot clone missing node in group %q", result.Name)
			}
			targets = append(targets, cloneTarget{groupIndex: groupIndex, refIndex: refIndex, original: ref})
			originals = append(originals, ref)
			result.Cloned++
		}
	}
	if len(targets) == 0 {
		return nil
	}
	var clones []ServerRef
	if apply {
		var err error
		clones, err = cloneServerRefsTx(tx, originals)
		if err != nil {
			return err
		}
	} else {
		var base int
		if err := tx.QueryRow(`SELECT COALESCE(MAX(idx), -1) + 1 FROM nodes WHERE bucket = ? AND key = ?`,
			nodeBucket, manualServersKey).Scan(&base); err != nil {
			return err
		}
		clones = make([]ServerRef, len(targets))
		for index := range clones {
			clones[index] = ServerRef{Type: "server", Index: base + index}
		}
	}
	remapped := make(map[int]map[ServerRef]ServerRef)
	for index, target := range targets {
		result := &results[target.groupIndex]
		result.refs[target.refIndex] = clones[index]
		if remapped[target.groupIndex] == nil {
			remapped[target.groupIndex] = make(map[ServerRef]ServerRef)
		}
		remapped[target.groupIndex][target.original] = clones[index]
	}
	for groupIndex, mappings := range remapped {
		for name, ref := range results[groupIndex].active {
			if clone, changed := mappings[ref]; changed {
				results[groupIndex].active[name] = clone
			}
		}
	}
	return nil
}
