package configure

import "testing"

// CopyCount 决定每条规则复制几个节点，0 表示不限
func TestCopyCount(t *testing.T) {
	tests := []struct {
		name string
		rule SubscriptionNodeCopyRule
		want int
	}{
		{"仅最快", SubscriptionNodeCopyRule{Mode: CopyModeBest}, 1},
		{"前 N 个", SubscriptionNodeCopyRule{Mode: CopyModeTopN, Count: 5}, 5},
		{"全部连通", SubscriptionNodeCopyRule{Mode: CopyModeAll}, 0},
		// 历史数据没有 Mode 字段，必须保持旧行为（只复制最快的一个），
		// 否则升级后目标分组会突然涌入大量节点
		{"空 Mode 回落到仅最快", SubscriptionNodeCopyRule{}, 1},
		{"topN 未填数量时至少 1 个", SubscriptionNodeCopyRule{Mode: CopyModeTopN}, 1},
		{"topN 数量为负时至少 1 个", SubscriptionNodeCopyRule{Mode: CopyModeTopN, Count: -3}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rule.CopyCount(); got != tc.want {
				t.Errorf("CopyCount() = %d, 期望 %d", got, tc.want)
			}
		})
	}
}

// 旧版单来源分组字段要能折叠进 SourceGroupIDs，且不能丢配置
func TestMigrateNodeCopyRules(t *testing.T) {
	rules := []SubscriptionNodeCopyRule{
		{LegacySourceGroupID: "sub-1", TargetGroupIDs: []string{"local-1"}},
	}
	if !migrateNodeCopyRules(rules) {
		t.Fatal("存在旧字段时应返回 changed=true")
	}
	got := rules[0]
	if len(got.SourceGroupIDs) != 1 || got.SourceGroupIDs[0] != "sub-1" {
		t.Errorf("旧字段未折叠进 SourceGroupIDs: %v", got.SourceGroupIDs)
	}
	if got.LegacySourceGroupID != "" {
		t.Errorf("折叠后应清空旧字段，实际 %q", got.LegacySourceGroupID)
	}
	if len(got.TargetGroupIDs) != 1 {
		t.Errorf("目标分组不应受影响: %v", got.TargetGroupIDs)
	}
}

// 迁移必须幂等：第二次调用不应再报改动，也不能重复追加
func TestMigrateNodeCopyRulesIdempotent(t *testing.T) {
	rules := []SubscriptionNodeCopyRule{
		{LegacySourceGroupID: "sub-1"},
	}
	migrateNodeCopyRules(rules)
	if migrateNodeCopyRules(rules) {
		t.Error("已迁移过的数据不应再报 changed=true")
	}
	if len(rules[0].SourceGroupIDs) != 1 {
		t.Errorf("重复迁移导致来源分组重复: %v", rules[0].SourceGroupIDs)
	}
}

// 旧字段与新字段并存时不应产生重复项
func TestMigrateNodeCopyRulesNoDuplicate(t *testing.T) {
	rules := []SubscriptionNodeCopyRule{
		{LegacySourceGroupID: "sub-1", SourceGroupIDs: []string{"sub-1", "sub-2"}},
	}
	migrateNodeCopyRules(rules)
	if len(rules[0].SourceGroupIDs) != 2 {
		t.Errorf("已存在的 ID 不应重复追加: %v", rules[0].SourceGroupIDs)
	}
}

// 没有旧字段的规则不应被判定为有改动，避免每次读设置都触发一次写库
func TestMigrateNodeCopyRulesNoChange(t *testing.T) {
	rules := []SubscriptionNodeCopyRule{
		{SourceGroupIDs: []string{"sub-1"}, Mode: CopyModeAll},
	}
	if migrateNodeCopyRules(rules) {
		t.Error("无旧字段时不应报 changed=true")
	}
}
