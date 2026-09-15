// Package constants centralizes business enums, error codes, messages and log templates.
package constants

// OrderStatus 订单状态枚举（前端 constants/order.ts 同步维护）。
const (
	OrderStatusPendingPayment  string = "pending_payment"  // 待付款
	OrderStatusPendingShipment string = "pending_shipment" // 待发货
	OrderStatusShipped         string = "shipped"          // 已发货
	OrderStatusReceived        string = "received"         // 已收货
	OrderStatusCompleted       string = "completed"        // 已完成
	OrderStatusCancelled       string = "cancelled"        // 已取消
)

// ProductCondition 商品成色枚举。
const (
	ProductConditionBrandNew      string = "brand_new"      // 全新
	ProductConditionAlmostNew     string = "almost_new"     // 几乎全新
	ProductConditionLightlyUsed   string = "lightly_used"   // 轻微使用
	ProductConditionObviouslyUsed string = "obviously_used" // 明显使用
)

// ProductCategory 商品分类枚举。
const (
	ProductCategoryDigital  string = "digital"  // 数码
	ProductCategoryClothing string = "clothing" // 服饰
	ProductCategoryBooks    string = "books"    // 图书
	ProductCategoryHome     string = "home"     // 家居
	ProductCategorySports   string = "sports"   // 运动
	ProductCategoryOther    string = "other"    // 其他
)

// ProductStatus 商品上下架状态枚举。
const (
	ProductStatusOnSale   string = "on_sale"   // 在售
	ProductStatusSold     string = "sold"      // 已售出
	ProductStatusOffShelf string = "off_shelf" // 已下架
)

// UserRole 用户角色枚举。
const (
	UserRoleUser  string = "user"  // 普通用户
	UserRoleAdmin string = "admin" // 管理员
)

// ReviewRating 评价等级枚举。
const (
	ReviewRatingGood    string = "good"    // 好评
	ReviewRatingNeutral string = "neutral" // 中评
	ReviewRatingBad     string = "bad"     // 差评
)

// ExchangeStatus 换物提案状态枚举（前端 constants/index.ts 同步维护）。
const (
	ExchangeStatusPending   string = "pending"   // 待对方回应（生效中，占用物品）
	ExchangeStatusCountered string = "countered" // 对方已还价（生效中，占用物品）
	ExchangeStatusAccepted  string = "accepted"  // 已接受（成交，物品锁定）
	ExchangeStatusRejected  string = "rejected"  // 已拒绝（终态，释放物品）
	ExchangeStatusCancelled string = "cancelled" // 已取消（终态，释放物品）
	ExchangeStatusExpired   string = "expired"   // 已超时（终态，释放物品）
)

// ExchangeSide 提案物品归属方枚举。
const (
	ExchangeSideOffer  string = "offer"  // 发起人拿出的物品
	ExchangeSideTarget string = "target" // 希望换取的对方物品
)

// ExchangeParty 提案参与方/动作权枚举（同时用于 top_up_payer 差价支付方）。
const (
	ExchangePartyOfferor string = "offeror" // 提案发起人
	ExchangePartyOfferee string = "offeree" // 提案接收人
	ExchangePartySystem  string = "system"  // 系统（超时等）
)

// ExchangeAction 提案历史操作类型枚举。
const (
	ExchangeActionCreated   string = "created"   // 发起提案
	ExchangeActionCountered string = "countered" // 还价（仅一次）
	ExchangeActionAccepted  string = "accepted"  // 接受
	ExchangeActionRejected  string = "rejected"  // 拒绝
	ExchangeActionCancelled string = "cancelled" // 取消
	ExchangeActionExpired   string = "expired"   // 超时自动结束
)

// ExchangeActiveStatuses 生效中状态：处于这些状态的提案会占用涉及物品。
var ExchangeActiveStatuses = []string{ExchangeStatusPending, ExchangeStatusCountered}

// ExchangeStatusTransitions 提案状态机：允许的流转映射（新状态 → 允许的前置状态集合）。
var ExchangeStatusTransitions = map[string][]string{
	ExchangeStatusPending:   {ExchangeStatusPending},
	ExchangeStatusCountered: {ExchangeStatusPending},
	ExchangeStatusAccepted:  {ExchangeStatusPending, ExchangeStatusCountered},
	ExchangeStatusRejected:  {ExchangeStatusPending, ExchangeStatusCountered},
	ExchangeStatusCancelled: {ExchangeStatusPending, ExchangeStatusCountered},
	ExchangeStatusExpired:   {ExchangeStatusPending, ExchangeStatusCountered},
}

// ExchangeDefaultTTL 提案默认超时时间（小时），到期未接受/拒绝/还价即自动失效并释放物品。
const ExchangeDefaultTTLHours = 72

// ValidExchangeStatus 校验提案状态值是否合法。
func ValidExchangeStatus(status string) bool {
	switch status {
	case ExchangeStatusPending, ExchangeStatusCountered, ExchangeStatusAccepted,
		ExchangeStatusRejected, ExchangeStatusCancelled, ExchangeStatusExpired:
		return true
	}
	return false
}

// IsExchangeActive 提案是否处于生效（占用物品）状态。
func IsExchangeActive(status string) bool {
	return status == ExchangeStatusPending || status == ExchangeStatusCountered
}

// ValidExchangeSide 校验提案物品方枚举。
func ValidExchangeSide(side string) bool {
	return side == ExchangeSideOffer || side == ExchangeSideTarget
}

// ValidExchangeParty 校验参与方/差价支付方枚举。
func ValidExchangeParty(party string) bool {
	return party == ExchangePartyOfferor || party == ExchangePartyOfferee
}

// ValidExchangeAction 校验历史操作类型枚举。
func ValidExchangeAction(action string) bool {
	switch action {
	case ExchangeActionCreated, ExchangeActionCountered, ExchangeActionAccepted,
		ExchangeActionRejected, ExchangeActionCancelled, ExchangeActionExpired:
		return true
	}
	return false
}

// OrderStatusTransitions 订单状态机：允许的流转映射（新状态 → 允许的前置状态集合）。
var OrderStatusTransitions = map[string][]string{
	OrderStatusPendingPayment:  {OrderStatusPendingPayment},
	OrderStatusPendingShipment: {OrderStatusPendingPayment},
	OrderStatusShipped:         {OrderStatusPendingShipment},
	OrderStatusReceived:        {OrderStatusShipped},
	OrderStatusCompleted:       {OrderStatusReceived},
	OrderStatusCancelled:       {OrderStatusPendingPayment, OrderStatusPendingShipment},
}

// ValidOrderStatus 校验订单状态值是否合法。
func ValidOrderStatus(status string) bool {
	switch status {
	case OrderStatusPendingPayment, OrderStatusPendingShipment, OrderStatusShipped,
		OrderStatusReceived, OrderStatusCompleted, OrderStatusCancelled:
		return true
	}
	return false
}

// ValidProductCondition 校验成色值是否合法。
func ValidProductCondition(cond string) bool {
	switch cond {
	case ProductConditionBrandNew, ProductConditionAlmostNew, ProductConditionLightlyUsed, ProductConditionObviouslyUsed:
		return true
	}
	return false
}

// ValidProductCategory 校验分类值是否合法。
func ValidProductCategory(cat string) bool {
	switch cat {
	case ProductCategoryDigital, ProductCategoryClothing, ProductCategoryBooks,
		ProductCategoryHome, ProductCategorySports, ProductCategoryOther:
		return true
	}
	return false
}

// ValidProductStatus 校验商品状态值是否合法。
func ValidProductStatus(status string) bool {
	switch status {
	case ProductStatusOnSale, ProductStatusSold, ProductStatusOffShelf:
		return true
	}
	return false
}

// ValidUserRole 校验角色值是否合法。
func ValidUserRole(role string) bool {
	return role == UserRoleUser || role == UserRoleAdmin
}

// ValidReviewRating 校验评价等级是否合法。
func ValidReviewRating(rating string) bool {
	switch rating {
	case ReviewRatingGood, ReviewRatingNeutral, ReviewRatingBad:
		return true
	}
	return false
}
