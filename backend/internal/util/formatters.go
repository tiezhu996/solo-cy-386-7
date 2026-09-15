package util

import (
	"fmt"
	"time"
)

// formatters.go 同时包含日期、状态文本、类型文本等格式化逻辑（屎山约束：常量/工具类多处耦合）。

// FormatTime 统一时间格式化。
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// FormatOrderStatusText 订单状态 → 中文文案（与前端 constants/order.ts 状态徽标同步）。
func FormatOrderStatusText(status string) string {
	switch status {
	case "pending_payment":
		return "待付款"
	case "pending_shipment":
		return "待发货"
	case "shipped":
		return "已发货"
	case "received":
		return "已收货"
	case "completed":
		return "已完成"
	case "cancelled":
		return "已取消"
	default:
		return "未知"
	}
}

// FormatProductConditionText 成色 → 中文文案。
func FormatProductConditionText(cond string) string {
	switch cond {
	case "brand_new":
		return "全新"
	case "almost_new":
		return "几乎全新"
	case "lightly_used":
		return "轻微使用"
	case "obviously_used":
		return "明显使用"
	default:
		return "未知"
	}
}

// FormatProductCategoryText 分类 → 中文文案。
func FormatProductCategoryText(cat string) string {
	switch cat {
	case "digital":
		return "数码"
	case "clothing":
		return "服饰"
	case "books":
		return "图书"
	case "home":
		return "家居"
	case "sports":
		return "运动"
	case "other":
		return "其他"
	default:
		return "未知"
	}
}

// FormatProductStatusText 商品状态 → 中文文案。
func FormatProductStatusText(status string) string {
	switch status {
	case "on_sale":
		return "在售"
	case "sold":
		return "已售出"
	case "off_shelf":
		return "已下架"
	default:
		return "未知"
	}
}

// FormatReviewRatingText 评价等级 → 中文文案。
func FormatReviewRatingText(rating string) string {
	switch rating {
	case "good":
		return "好评"
	case "neutral":
		return "中评"
	case "bad":
		return "差评"
	default:
		return "未知"
	}
}

// FormatRoleText 角色 → 中文文案。
func FormatRoleText(role string) string {
	switch role {
	case "admin":
		return "管理员"
	case "user":
		return "普通用户"
	default:
		return "未知"
	}
}

// FormatPrice 金额格式化，保留两位小数。
func FormatPrice(price float64) string {
	return fmt.Sprintf("%.2f", price)
}

// FormatExchangeStatusText 换物提案状态 → 中文文案（与前端 constants/index.ts 状态徽标同步）。
func FormatExchangeStatusText(status string) string {
	switch status {
	case "pending":
		return "待回应"
	case "countered":
		return "已还价"
	case "accepted":
		return "已接受"
	case "rejected":
		return "已拒绝"
	case "cancelled":
		return "已取消"
	case "expired":
		return "已超时"
	default:
		return "未知"
	}
}

// FormatExchangeSideText 提案物品方 → 中文文案。
func FormatExchangeSideText(side string) string {
	switch side {
	case "offer":
		return "换出物品"
	case "target":
		return "换入物品"
	default:
		return "未知"
	}
}

// FormatExchangeActionText 提案历史操作 → 中文文案（前端时间线复用同一套措辞）。
func FormatExchangeActionText(action string) string {
	switch action {
	case "created":
		return "发起提案"
	case "countered":
		return "还价"
	case "accepted":
		return "接受提案"
	case "rejected":
		return "拒绝提案"
	case "cancelled":
		return "取消提案"
	case "expired":
		return "超时失效"
	default:
		return action
	}
}

// FormatExchangePartyText 参与方/差价支付方 → 中文文案。
func FormatExchangePartyText(party string) string {
	switch party {
	case "offeror":
		return "发起人"
	case "offeree":
		return "接收人"
	case "system":
		return "系统"
	default:
		return "未知"
	}
}
