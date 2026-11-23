package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProxyStation/proxystation/db"
	"github.com/ProxyStation/proxystation/db/configure"
)

func TestBackupDatabaseIncludesWALAndProtectsExistingBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	source, err := sql.Open("sqlite", sourcePath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	if _, err := source.Exec(`CREATE TABLE sample(value TEXT); INSERT INTO sample VALUES('wal-value')`); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(sourcePath + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("test must include live, uncheckpointed WAL data")
	}
	backupPath := filepath.Join(directory, "backup.db")
	if err := backupDatabase(context.Background(), source, backupPath); err != nil {
		t.Fatal(err)
	}
	backup, err := sql.Open("sqlite", backupPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var value string
	if err := backup.QueryRow(`SELECT value FROM sample`).Scan(&value); err != nil || value != "wal-value" {
		t.Fatalf("consistent backup lost WAL data: %v", err)
	}
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("backup must use owner-only permissions")
	}
	if err := backupDatabase(context.Background(), source, backupPath); err == nil {
		t.Fatal("existing backups must never be overwritten")
	}
}

func TestDedupeCommandDryRunAndApply(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "runtime.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "first", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#alias"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "group", Name: "group"}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO group_servers(group_id, ref_idx, type, ref_index, sub)
			VALUES('group', 0, 'server', 0, 0), ('group', 1, 'server', 1, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--db", path}); err != nil {
		t.Fatal(err)
	}
	if len(configure.GetGroup(0).Servers) != 2 {
		t.Fatal("default dry run must not remove duplicates")
	}
	if err := run([]string{"--db", path, "--apply"}); err == nil {
		t.Fatal("apply without a backup must be rejected")
	}
	backupPath := filepath.Join(directory, "backups", "before.db")
	if err := run([]string{"--db", path, "--apply", "--backup", backupPath}); err != nil {
		t.Fatal(err)
	}
	if len(configure.GetGroup(0).Servers) != 1 || configure.GetLenServers() != 2 {
		t.Fatal("apply must only remove duplicate group references")
	}
	backup, err := sql.Open("sqlite", backupPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var count int
	if err := backup.QueryRow(`SELECT COUNT(*) FROM group_servers`).Scan(&count); err != nil || count != 2 {
		t.Fatal("backup must preserve the pre-cleanup group references")
	}
}

func TestDedupeRepairCommandUsesStableBindingBeforeContentComparison(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "runtime.db")
	if err := db.Init(path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := configure.AppendSubscriptions([]*configure.SubscriptionRaw{{ID: "stable", Servers: []configure.ServerRaw{
		{Name: "first", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#alias"},
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := configure.AppendGroup(&configure.Group{ID: "group", Name: "group", FromSub: true, SubID: "stable"}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO group_servers(group_id, ref_idx, type, ref_index, sub)
			VALUES('group', 0, 'sub_server', 0, 1), ('group', 1, 'sub_server', 1, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--db", path}); err == nil {
		t.Fatal("content cleanup must reject stale subscription bindings without explicit repair")
	}
	if err := run([]string{"--db", path, "--repair-subscription-refs"}); err != nil {
		t.Fatal(err)
	}
	if configure.GetGroup(0).Servers[0].Sub != 1 || len(configure.GetGroup(0).Servers) != 2 {
		t.Fatal("repair preview must not change the database")
	}
	if err := run([]string{"--db", path, "--repair-subscription-refs", "--apply",
		"--backup", filepath.Join(directory, "backup.db")}); err != nil {
		t.Fatal(err)
	}
	if refs := configure.GetGroup(0).Servers; len(refs) != 1 || refs[0].Sub != 0 {
		t.Fatal("apply must repair references before deduplicating the correct subscription nodes")
	}
	if configure.GetLenSubscriptionServers(0) != 2 {
		t.Fatal("reference repair must preserve subscription node storage")
	}
}

func TestIsolateCommandClonesCrossGroupSharesAfterLocalDeduplication(testContext *testing.T) {
	directory := testContext.TempDir()
	path := filepath.Join(directory, "runtime.db")
	if err := db.Init(path); err != nil {
		testContext.Fatal(err)
	}
	defer db.Close()
	if err := configure.SetServers([]configure.ServerRaw{
		{Name: "first", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#first"},
		{Name: "alias", Host: "proxy.example", Port: 443, Type: "vless", Link: "vless://credential-a@proxy.example:443#alias"},
	}); err != nil {
		testContext.Fatal(err)
	}
	for _, groupID := range []string{"first", "second"} {
		if err := configure.AppendGroup(&configure.Group{ID: groupID, Name: groupID}); err != nil {
			testContext.Fatal(err)
		}
	}
	if err := db.WriteTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO group_servers(group_id, ref_idx, type, ref_index, sub) VALUES
			('first', 0, 'server', 0, 0), ('second', 0, 'server', 0, 0), ('second', 1, 'server', 1, 0)`)
		return err
	}); err != nil {
		testContext.Fatal(err)
	}
	args := []string{"--db", path, "--isolate-group-nodes"}
	if err := run(args); err != nil {
		testContext.Fatal(err)
	}
	if configure.GetLenServers() != 2 || len(configure.GetGroup(1).Servers) != 2 {
		testContext.Fatal("isolation preview must not create clones or remove members")
	}
	backupPath := filepath.Join(directory, "backups", "before.db")
	if err := run(append(args, "--apply", "--backup", backupPath)); err != nil {
		testContext.Fatal(err)
	}
	first := configure.GetGroup(0).Servers
	second := configure.GetGroup(1).Servers
	if len(first) != 1 || len(second) != 1 || first[0] == second[0] || configure.GetLenServers() != 3 {
		testContext.Fatal("migration must remove local aliases but preserve independent identical nodes across groups")
	}
	backup, err := sql.Open("sqlite", backupPath+"?mode=ro")
	if err != nil {
		testContext.Fatal(err)
	}
	defer backup.Close()
	var nodes, members int
	if err := backup.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&nodes); err != nil {
		testContext.Fatal(err)
	}
	if err := backup.QueryRow(`SELECT COUNT(*) FROM group_servers`).Scan(&members); err != nil {
		testContext.Fatal(err)
	}
	if nodes != 2 || members != 3 {
		testContext.Fatal("consistent backup must preserve all pre-migration nodes and memberships")
	}
	if err := run(args); err != nil {
		testContext.Fatal(err)
	}
	if configure.GetLenServers() != 3 {
		testContext.Fatal("running maintenance again must not clone already independent nodes")
	}
}
