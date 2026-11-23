package configure

import (
	"reflect"
	"testing"
)

func TestGroupReverseIndexReflectsMutations(t *testing.T) {
	setupSubscriptionAtomicTestDB(t)
	if err := SetServers(identityGroupTestServers()); err != nil {
		t.Fatal(err)
	}
	ref := ServerRef{Type: "server", Index: 0}
	other := ServerRef{Type: "server", Index: 1}

	if err := AppendGroup(&Group{ID: "g1", Name: "A", Servers: []ServerRef{ref}}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	// 第一次调用构建索引，后续调用复用缓存。
	if got := GetGroupNamesForRefs([]ServerRef{ref})[ref]; !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("initial names = %v, want [A]", got)
	}
	if got := GetGroupNamesForRefs([]ServerRef{ref})[ref]; !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("cached names = %v, want [A]", got)
	}

	// 新增分组后缓存必须失效。
	if err := AppendGroup(&Group{ID: "g2", Name: "B", Servers: []ServerRef{ref}}); err != nil {
		t.Fatalf("append group: %v", err)
	}
	cloned := GetGroup(1).Servers[0]
	if cloned == ref {
		t.Fatal("different groups must not share the same node record")
	}
	if got := GetGroupNamesForRefs([]ServerRef{ref})[ref]; !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("source names = %v, want [A]", got)
	}
	if got := GetGroupNamesForRefs([]ServerRef{cloned})[cloned]; !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("clone names = %v, want [B]", got)
	}

	// 重命名分组后缓存必须失效。
	groups := GetGroups()
	groups[0].Name = "A2"
	if err := SetGroup(0, &groups[0]); err != nil {
		t.Fatalf("set group: %v", err)
	}
	if got := GetGroupNamesForRefs([]ServerRef{ref})[ref]; !reflect.DeepEqual(got, []string{"A2"}) {
		t.Fatalf("after rename names = %v, want [A2]", got)
	}

	// 从分组移除引用后缓存必须失效。
	if err := RemoveServersFromGroup(1, []ServerRef{cloned}); err != nil {
		t.Fatalf("remove from group: %v", err)
	}
	if got := GetGroupNamesForRefs([]ServerRef{ref})[ref]; !reflect.DeepEqual(got, []string{"A2"}) {
		t.Fatalf("after remove names = %v, want [A2]", got)
	}
	if _, ok := GetGroupNamesForRefs([]ServerRef{other})[other]; ok {
		t.Fatalf("unexpected membership for untouched ref")
	}
	if _, ok := GetGroupNamesForRefs([]ServerRef{cloned})[cloned]; ok {
		t.Fatal("removed clone must not retain stale group membership")
	}
}
