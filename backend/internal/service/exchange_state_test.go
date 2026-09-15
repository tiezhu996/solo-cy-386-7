package service

import "testing"

// TestCanExchangeTransition 换物提案状态机流转表驱动测试。
func TestCanExchangeTransition(t *testing.T) {
	cases := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{"对方接受初始提案", "pending", "accepted", true},
		{"对方拒绝初始提案", "pending", "rejected", true},
		{"对方还价", "pending", "countered", true},
		{"发起人取消", "pending", "cancelled", true},
		{"初始提案超时", "pending", "expired", true},
		{"发起人接受还价", "countered", "accepted", true},
		{"发起人拒绝还价", "countered", "rejected", true},
		{"发起人取消还价提案", "countered", "cancelled", true},
		{"还价提案超时", "countered", "expired", true},
		{"拒绝后不可再接受", "rejected", "accepted", false},
		{"取消后不可复活", "cancelled", "pending", false},
		{"成交后不可还价", "accepted", "countered", false},
		{"不可重复还价", "countered", "countered", false},
		{"终态不可超时", "expired", "expired", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canExchangeTransition(tc.from, tc.to); got != tc.want {
				t.Fatalf("canExchangeTransition(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// TestTargetStatusByAction 动作到目标状态映射。
func TestTargetStatusByAction(t *testing.T) {
	cases := map[string]string{
		"accepted":  "accepted",
		"rejected":  "rejected",
		"cancelled": "cancelled",
		"countered": "countered",
		"expired":   "expired",
		"created":   "",
		"unknown":   "",
	}
	for action, want := range cases {
		if got := targetStatusByAction(action); got != want {
			t.Fatalf("targetStatusByAction(%s) = %q, want %q", action, got, want)
		}
	}
}

// TestGenProposalNo 提案号非空且唯一。
func TestGenProposalNo(t *testing.T) {
	a := genProposalNo()
	b := genProposalNo()
	if a == "" || b == "" || a == b {
		t.Fatalf("proposal no should be non-empty and unique, got %q %q", a, b)
	}
}

// TestDedupPositive 去重并剔除 0。
func TestDedupPositive(t *testing.T) {
	got := dedupPositive([]uint{0, 3, 1, 3, 2, 1, 0})
	want := []uint{3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("dedupPositive len = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupPositive = %v, want %v", got, want)
		}
	}
}

// TestOverlap 两集合交集判断。
func TestOverlap(t *testing.T) {
	if !overlap([]uint{1, 2}, []uint{2, 3}) {
		t.Fatal("expected overlap between {1,2} and {2,3}")
	}
	if overlap([]uint{1, 2}, []uint{3, 4}) {
		t.Fatal("did not expect overlap between {1,2} and {3,4}")
	}
}
