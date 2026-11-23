package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func initTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
	return path
}

func TestScalarRoundTrip(t *testing.T) {
	initTestDB(t)

	if err := Set("system", "running", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	var running bool
	if err := Get("system", "running", &running); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !running {
		t.Fatalf("expected running=true")
	}
	if !Exists("system", "running") {
		t.Fatalf("expected key to exist")
	}
	if got, _ := GetBucketLen("system"); got != 1 {
		t.Fatalf("GetBucketLen = %d, want 1", got)
	}
	if err := Set("system", "running", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if Exists("system", "running") {
		t.Fatalf("expected key deleted")
	}
}

func TestSetWithArrayUsesListStorage(t *testing.T) {
	initTestDB(t)

	servers := []map[string]interface{}{
		{"name": "a", "port": 1},
		{"name": "b", "port": 2},
	}
	if err := Set("touch", "servers", servers); err != nil {
		t.Fatalf("Set array: %v", err)
	}
	// ListGetAll must see it even though it was written via Set.
	got, err := ListGetAll("touch", "servers")
	if err != nil {
		t.Fatalf("ListGetAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListGetAll len = %d, want 2", len(got))
	}
	// Get must also reassemble it as an array.
	var reassembled []map[string]interface{}
	if err := Get("touch", "servers", &reassembled); err != nil {
		t.Fatalf("Get array: %v", err)
	}
	if len(reassembled) != 2 || reassembled[1]["name"] != "b" {
		t.Fatalf("unexpected reassembled value: %+v", reassembled)
	}
	if got, _ := ListLen("touch", "servers"); got != 2 {
		t.Fatalf("ListLen = %d, want 2", got)
	}
}

func TestListUpdateIsSingleRow(t *testing.T) {
	initTestDB(t)

	items := []map[string]interface{}{
		{"n": 0}, {"n": 1}, {"n": 2},
	}
	if err := ListAppend("touch", "groups", items); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}
	if err := ListUpdate("touch", "groups", 1, func(raw []byte) ([]byte, error) {
		var item map[string]interface{}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item["n"] = 99
		return json.Marshal(item)
	}); err != nil {
		t.Fatalf("ListUpdate: %v", err)
	}

	raw, err := ListGetMany("touch", "groups", []int{0, 1, 2})
	if err != nil {
		t.Fatalf("ListGetMany: %v", err)
	}
	assertN(t, raw[0], 0)
	assertN(t, raw[1], 99)
	assertN(t, raw[2], 2)
}

func TestListRemoveCompactsIndexes(t *testing.T) {
	initTestDB(t)

	if err := ListAppend("touch", "servers", []map[string]int{{"n": 0}, {"n": 1}, {"n": 2}, {"n": 3}, {"n": 4}}); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}
	if err := ListRemove("touch", "servers", []int{1, 3}); err != nil {
		t.Fatalf("ListRemove: %v", err)
	}
	if got, _ := ListLen("touch", "servers"); got != 3 {
		t.Fatalf("ListLen = %d, want 3", got)
	}
	for want, index := range []int{0, 2, 4} {
		raw, err := ListGet("touch", "servers", want)
		if err != nil {
			t.Fatalf("ListGet(%d): %v", want, err)
		}
		assertN(t, raw, index)
	}
	// Out-of-range removal is ignored, not an error.
	if err := ListRemove("touch", "servers", []int{99}); err != nil {
		t.Fatalf("out-of-range ListRemove: %v", err)
	}
}

func TestListSetOutOfRange(t *testing.T) {
	initTestDB(t)
	if err := ListAppend("touch", "servers", []map[string]int{{"n": 0}}); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}
	if err := ListSet("touch", "servers", 5, map[string]int{"n": 1}); err == nil {
		t.Fatalf("expected out-of-range error")
	}
}

func TestBucketKeysUnion(t *testing.T) {
	initTestDB(t)
	if err := Set("accounts", "admin", "pw"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := ListAppend("touch", "servers", []map[string]int{{"n": 0}}); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}
	keys, err := GetBucketKeys("accounts")
	if err != nil {
		t.Fatalf("GetBucketKeys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "admin" {
		t.Fatalf("unexpected account keys: %v", keys)
	}
	touchKeys, err := GetBucketKeys("touch")
	if err != nil {
		t.Fatalf("GetBucketKeys touch: %v", err)
	}
	if len(touchKeys) != 1 || touchKeys[0] != "servers" {
		t.Fatalf("unexpected touch keys: %v", touchKeys)
	}
}

func TestMigrateFromBolt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proxystation.db")

	// 构造一个旧版 bbolt 数据库：既有标量也有 JSON 数组。
	old, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	err = old.Update(func(tx *bolt.Tx) error {
		system, _ := tx.CreateBucketIfNotExists([]byte("system"))
		_ = system.Put([]byte("setting"), []byte(`{"logLevel":"info"}`))
		_ = system.Put([]byte("routing_rules"), []byte(`[{"id":"r1"},{"id":"r2"}]`))
		_ = system.Put([]byte("running"), []byte(`true`))

		touch, _ := tx.CreateBucketIfNotExists([]byte("touch"))
		_ = touch.Put([]byte("servers"), []byte(`[{"name":"n0"},{"name":"n1"},{"name":"n2"}]`))
		_ = touch.Put([]byte("subscriptions"), []byte(`[{"id":"s0"}]`))

		accounts, _ := tx.CreateBucketIfNotExists([]byte("accounts"))
		_ = accounts.Put([]byte("admin"), []byte(`"pw"`))
		return nil
	})
	if err != nil {
		t.Fatalf("seed bolt: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close bolt: %v", err)
	}

	if err := Init(path); err != nil {
		t.Fatalf("Init with legacy file: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	// 标量
	var setting map[string]interface{}
	if err := Get("system", "setting", &setting); err != nil {
		t.Fatalf("Get setting: %v", err)
	}
	if setting["logLevel"] != "info" {
		t.Fatalf("unexpected setting: %+v", setting)
	}
	// 数组（通过 Get 读回）
	var rules []map[string]string
	if err := Get("system", "routing_rules", &rules); err != nil {
		t.Fatalf("Get routing_rules: %v", err)
	}
	if len(rules) != 2 || rules[1]["id"] != "r2" {
		t.Fatalf("unexpected rules: %+v", rules)
	}
	// 数组（通过 List API 读回）
	servers, err := ListGetAll("touch", "servers")
	if err != nil {
		t.Fatalf("ListGetAll servers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers len = %d, want 3", len(servers))
	}
	if got, _ := ListLen("touch", "subscriptions"); got != 1 {
		t.Fatalf("subscriptions len = %d, want 1", got)
	}
	// accounts
	var pw string
	if err := Get("accounts", "admin", &pw); err != nil {
		t.Fatalf("Get account: %v", err)
	}
	if pw != "pw" {
		t.Fatalf("password = %q, want pw", pw)
	}
	// 原文件应已备份
	if _, err := os.Stat(path + ".bolt.bak"); err != nil {
		t.Fatalf("expected backup file: %v", err)
	}

	// 迁移后仍可正常做单条修改
	if err := ListUpdate("touch", "servers", 1, func(raw []byte) ([]byte, error) {
		return []byte(`{"name":"updated"}`), nil
	}); err != nil {
		t.Fatalf("ListUpdate after migration: %v", err)
	}
	updated, _ := ListGet("touch", "servers", 1)
	if string(updated) != `{"name":"updated"}` {
		t.Fatalf("unexpected updated value: %s", updated)
	}

	// 再次 Init 不应重复迁移
	if err := Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := Init(path); err != nil {
		t.Fatalf("reopen migrated file: %v", err)
	}
}

func assertN(t *testing.T, raw []byte, want int) {
	t.Helper()
	var item map[string]int
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if item["n"] != want {
		t.Fatalf("n = %d, want %d (raw=%s)", item["n"], want, raw)
	}
}

func TestListGetRange(t *testing.T) {
	initTestDB(t)
	items := make([]map[string]int, 5)
	for i := range items {
		items[i] = map[string]int{"n": i}
	}
	if err := ListAppend("touch", "servers", items); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}

	page, err := ListGetRange("touch", "servers", 1, 2)
	if err != nil {
		t.Fatalf("ListGetRange: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page len = %d, want 2", len(page))
	}
	assertN(t, page[0], 1)
	assertN(t, page[1], 2)

	// limit<=0 表示从 offset 起取全部
	tail, err := ListGetRange("touch", "servers", 3, 0)
	if err != nil {
		t.Fatalf("ListGetRange tail: %v", err)
	}
	if len(tail) != 2 {
		t.Fatalf("tail len = %d, want 2", len(tail))
	}
	// 越界 offset 返回空页
	empty, err := ListGetRange("touch", "servers", 99, 5)
	if err != nil {
		t.Fatalf("ListGetRange out of range: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty page, got %d", len(empty))
	}
}

// 读走独立连接池，写事务进行期间读不应被阻塞或死锁。
func TestReadsDoNotBlockBehindWrites(t *testing.T) {
	initTestDB(t)

	items := make([]map[string]interface{}, 200)
	for i := range items {
		items[i] = map[string]interface{}{"n": i}
	}
	if err := ListAppend("touch", "servers", items); err != nil {
		t.Fatalf("ListAppend: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			index := i % 200
			if err := ListUpdate("touch", "servers", index, func(raw []byte) ([]byte, error) {
				var item map[string]interface{}
				if err := json.Unmarshal(raw, &item); err != nil {
					return nil, err
				}
				item["n"] = i
				return json.Marshal(item)
			}); err != nil {
				t.Errorf("ListUpdate: %v", err)
				return
			}
		}
	}()

	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("concurrent read/write timed out (possible deadlock)")
		default:
			if _, err := ListGetAll("touch", "servers"); err != nil {
				t.Fatalf("ListGetAll: %v", err)
			}
		}
	}
}
