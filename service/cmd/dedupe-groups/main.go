package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ProxyStation/proxystation/db/configure"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("dedupe-groups", flag.ContinueOnError)
	dbPath := flags.String("db", "", "existing SQLite database path")
	apply := flags.Bool("apply", false, "remove duplicate group references")
	backupPath := flags.String("backup", "", "new consistent backup path; required with --apply")
	repairRefs := flags.Bool("repair-subscription-refs", false, "restore subscription group references using stable subscription IDs")
	isolateNodes := flags.Bool("isolate-group-nodes", false, "clone cross-group shared nodes and subscription nodes in manual groups")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || flags.NArg() != 0 {
		return fmt.Errorf("specify --db with an existing SQLite database")
	}
	if *apply && *backupPath == "" {
		return fmt.Errorf("--apply requires --backup; existing backups are never overwritten")
	}
	absolutePath, err := filepath.Abs(*dbPath)
	if err != nil {
		return err
	}
	absolutePath, err = filepath.EvalSymlinks(absolutePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("database must be a regular file")
	}
	query := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(10000)"}}
	if *apply {
		query.Set("mode", "rw")
		query.Set("_txlock", "immediate")
	}
	address := url.URL{Scheme: "file", Path: absolutePath, RawQuery: query.Encode()}
	database, err := sql.Open("sqlite", address.String())
	if err != nil {
		return err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		return err
	}
	if *apply {
		backup, err := filepath.Abs(*backupPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
			return err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(backup))
		if err != nil {
			return err
		}
		backup = filepath.Join(parent, filepath.Base(backup))
		for _, forbidden := range []string{absolutePath, absolutePath + "-wal", absolutePath + "-shm", absolutePath + "-journal"} {
			if backup == forbidden {
				return fmt.Errorf("backup must not replace the database or its journal files")
			}
		}
		if err := backupDatabase(ctx, database, backup); err != nil {
			return err
		}
		fmt.Printf("Backup: %s\n", backup)
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	beforeMissing, err := missingGroupActiveRefs(tx)
	if err != nil {
		return err
	}
	beforeMissingNodes, err := missingNodeRefs(tx)
	if err != nil {
		return err
	}
	var nodeCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&nodeCount); err != nil {
		return err
	}
	maintain := configure.DeduplicateGroups
	if *repairRefs {
		maintain = configure.RepairAndDeduplicateGroups
	}
	if *isolateNodes {
		maintain = configure.IsolateGroupNodes
		if *repairRefs {
			maintain = configure.RepairAndIsolateGroupNodes
		}
	}
	results, err := maintain(tx, *apply)
	if err != nil {
		return err
	}
	if *apply {
		cloned := 0
		for _, result := range results {
			cloned += result.Cloned
		}
		remaining, err := maintain(tx, false)
		if err != nil {
			return err
		}
		for _, result := range remaining {
			if result.Before != result.After || result.Repaired != 0 || result.Cloned != 0 {
				return fmt.Errorf("duplicate or shared nodes remain in group %q; transaction rolled back", result.Name)
			}
		}
		afterMissing, err := missingGroupActiveRefs(tx)
		if err != nil {
			return err
		}
		if afterMissing > beforeMissing {
			return fmt.Errorf("active node membership changed unexpectedly; transaction rolled back")
		}
		afterMissingNodes, err := missingNodeRefs(tx)
		if err != nil {
			return err
		}
		if afterMissingNodes > beforeMissingNodes {
			return fmt.Errorf("missing node references increased; transaction rolled back")
		}
		var remainingNodes int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&remainingNodes); err != nil {
			return err
		}
		if remainingNodes != nodeCount+cloned {
			return fmt.Errorf("node storage changed unexpectedly; transaction rolled back")
		}
		if err := checkIntegrity(ctx, tx); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	} else if err := tx.Rollback(); err != nil {
		return err
	}
	before, after, retargeted, repaired, missing, cloned := 0, 0, 0, 0, 0, 0
	for _, result := range results {
		before += result.Before
		after += result.After
		retargeted += result.Retargeted
		repaired += result.Repaired
		missing += result.Missing
		cloned += result.Cloned
		if result.Before != result.After || result.Repaired != 0 || result.Cloned != 0 {
			fmt.Printf("%s: %d -> %d, duplicates=%d, repaired_refs=%d, shared_nodes_cloned=%d, active_retargets=%d\n",
				result.Name, result.Before, result.After, result.Before-result.After, result.Repaired, result.Cloned, result.Retargeted)
		}
	}
	action := "Dry run; no database changes"
	if *apply {
		action = "Applied; source node records preserved"
	}
	fmt.Printf("%s. Groups=%d, references=%d -> %d, duplicates=%d, repaired_refs=%d, shared_nodes_cloned=%d, missing_refs=%d -> %d, active_retargets=%d\n",
		action, len(results), before, after, before-after, repaired, cloned, beforeMissingNodes, missing, retargeted)
	return nil
}

func backupDatabase(ctx context.Context, database *sql.DB, backupPath string) error {
	file, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, `VACUUM INTO ?`, backupPath); err != nil {
		return fmt.Errorf("consistent backup failed; database unchanged: %w", err)
	}
	address := url.URL{Scheme: "file", Path: backupPath, RawQuery: "mode=ro"}
	backup, err := sql.Open("sqlite", address.String())
	if err != nil {
		return err
	}
	defer backup.Close()
	return checkIntegrity(ctx, backup)
}

func checkIntegrity(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) error {
	var result string
	if err := query.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed")
	}
	return nil
}

func missingGroupActiveRefs(tx *sql.Tx) (int, error) {
	var missing int
	err := tx.QueryRow(`SELECT COUNT(*) FROM outbounds AS outbound
		WHERE outbound.target_type = 'group' AND outbound.active_node_ref IS NOT NULL
		AND json_type(outbound.active_node_ref) = 'object'
		AND NOT EXISTS (
			SELECT 1 FROM group_servers AS refs
			WHERE refs.group_id = outbound.group_id
			AND refs.type = json_extract(outbound.active_node_ref, '$.type')
			AND refs.ref_index = json_extract(outbound.active_node_ref, '$.index')
			AND (refs.type = 'server' OR refs.sub = COALESCE(json_extract(outbound.active_node_ref, '$.sub'), 0))
		)`).Scan(&missing)
	return missing, err
}

func missingNodeRefs(tx *sql.Tx) (int, error) {
	var missing int
	err := tx.QueryRow(`SELECT COUNT(*) FROM group_servers AS refs
		LEFT JOIN subscriptions ON refs.type = 'sub_server' AND subscriptions.idx = refs.sub
		LEFT JOIN nodes ON nodes.bucket = 'touch' AND nodes.idx = refs.ref_index
			AND nodes.key = CASE WHEN refs.type = 'server' THEN 'servers'
				ELSE 'sub_servers.' || subscriptions.id END
		WHERE nodes.idx IS NULL`).Scan(&missing)
	return missing, err
}
