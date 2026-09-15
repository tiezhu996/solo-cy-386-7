package dto

// ExchangeCreateRequest 发起换物提案入参。
// OfferProductIDs 为发起人（登录用户）自己的在售物品；TargetProductIDs 为对方的在售物品。
type ExchangeCreateRequest struct {
	OfferProductIDs  []uint  `json:"offer_product_ids" binding:"required,min=1,max=20,dive,gt=0"`
	TargetProductIDs []uint  `json:"target_product_ids" binding:"required,min=1,max=20,dive,gt=0"`
	Note             string  `json:"note" binding:"omitempty,max=500"`
	TopUpAmount      float64 `json:"top_up_amount" binding:"gte=0,lte=99999999"`
	TopUpPayer       string  `json:"top_up_payer" binding:"omitempty,oneof=offeror offeree"`
	TTLHours         int     `json:"ttl_hours" binding:"omitempty,min=1,max=720"`
}

// ExchangeCounterRequest 换价入参（双方仅可还价一次）。
// 还价后两边物品清单不变，仅覆盖说明与补差价；TopUpPayer 为空表示维持原支付方。
type ExchangeCounterRequest struct {
	Note        string  `json:"note" binding:"omitempty,max=500"`
	TopUpAmount float64 `json:"top_up_amount" binding:"gte=0,lte=99999999"`
	TopUpPayer  string  `json:"top_up_payer" binding:"omitempty,oneof=offeror offeree"`
}

// ExchangeRejectRequest 拒绝入参（可选附言，写入历史）。
type ExchangeRejectRequest struct {
	Note string `json:"note" binding:"omitempty,max=500"`
}

// ExchangeQuery 提案列表查询：role=initiator 我发起的，role=recipient 我收到的（默认全部参与）。
type ExchangeQuery struct {
	Status   string `form:"status" binding:"omitempty,oneof=pending countered accepted rejected cancelled expired"`
	Role     string `form:"role" binding:"omitempty,oneof=initiator recipient"`
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"page_size" binding:"omitempty,min=1,max=50"`
}

// ExchangeItemVO 提案物品快照视图。
type ExchangeItemVO struct {
	ID        uint       `json:"id"`
	ProductID uint       `json:"product_id"`
	OwnerID   uint       `json:"owner_id"`
	Side      string     `json:"side"`
	Title     string     `json:"title"`
	Price     float64    `json:"price"`
	Image     string     `json:"image"`
	Product   *ProductVO `json:"product,omitempty"`
}

// ExchangeHistoryVO 提案历史视图。
type ExchangeHistoryVO struct {
	ID          uint    `json:"id"`
	ActorID     uint    `json:"actor_id"`
	ActorRole   string  `json:"actor_role"`
	Action      string  `json:"action"`
	Note        string  `json:"note"`
	TopUpAmount float64 `json:"top_up_amount"`
	TopUpPayer  string  `json:"top_up_payer"`
	CreatedAt   string  `json:"created_at"`
	Actor       *UserVO `json:"actor,omitempty"`
}

// ExchangeProposalVO 换物提案视图对象（含双方、物品快照、完整历史）。
type ExchangeProposalVO struct {
	ID          uint    `json:"id"`
	ProposalNo  string  `json:"proposal_no"`
	OfferorID   uint    `json:"offeror_id"`
	OffereeID   uint    `json:"offeree_id"`
	Status      string  `json:"status"`
	Turn        string  `json:"turn"`
	Round       int     `json:"round"`
	Note        string  `json:"note"`
	TopUpAmount float64 `json:"top_up_amount"`
	TopUpPayer  string  `json:"top_up_payer"`
	ExpiresAt   string  `json:"expires_at"`
	AcceptedAt  *string `json:"accepted_at,omitempty"`
	RejectedAt  *string `json:"rejected_at,omitempty"`
	CancelledAt *string `json:"cancelled_at,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`

	Offeror     *UserVO             `json:"offeror,omitempty"`
	Offeree     *UserVO             `json:"offeree,omitempty"`
	OfferItems  []ExchangeItemVO    `json:"offer_items"`
	TargetItems []ExchangeItemVO    `json:"target_items"`
	History     []ExchangeHistoryVO `json:"history"`
}

// ExchangeListResponse 提案列表分页响应。
type ExchangeListResponse struct {
	List  []ExchangeProposalVO `json:"list"`
	Total int64                `json:"total"`
	Page  int                  `json:"page"`
	Size  int                  `json:"page_size"`
}
