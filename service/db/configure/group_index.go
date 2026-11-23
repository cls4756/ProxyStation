package configure

import "sync"

// 分组的反向索引：引用 -> 所在分组名。用于节点搜索结果里展示“所在分组”。
// 分组是用户选定的有界集合，但订阅分组可能包含上万条引用，逐次请求反序列化
// 整份分组代价偏高，因此缓存一份反向索引，任何分组写入后立即失效重建。
var (
	groupRefIndexMu    sync.Mutex
	groupRefIndexCache map[ServerRef][]string
	groupRefIndexValid bool
)

func invalidateGroupRefIndex() {
	groupRefIndexMu.Lock()
	groupRefIndexValid = false
	groupRefIndexMu.Unlock()
}

func groupRefIndex() map[ServerRef][]string {
	groupRefIndexMu.Lock()
	defer groupRefIndexMu.Unlock()
	if groupRefIndexValid && groupRefIndexCache != nil {
		return groupRefIndexCache
	}
	index := make(map[ServerRef][]string)
	for _, group := range GetGroups() {
		for _, ref := range group.Servers {
			index[ref] = append(index[ref], group.Name)
		}
	}
	groupRefIndexCache = index
	groupRefIndexValid = true
	return index
}

// GetGroupNamesForRefs 返回给定引用所在的分组名（按分组顺序），只读取一次反向索引。
func GetGroupNamesForRefs(refs []ServerRef) map[ServerRef][]string {
	result := make(map[ServerRef][]string, len(refs))
	if len(refs) == 0 {
		return result
	}
	index := groupRefIndex()
	for _, ref := range refs {
		if names, ok := index[ref]; ok {
			result[ref] = names
		}
	}
	return result
}
