package model

import (
	"time"

	"gorm.io/gorm"
)

// ExchangeProposal 换物提案实体：状态机见 internal/constants/enums.go 与 internal/service/exchange_service.go。
// 提案由发起人（offeror，用自己的在售物品）向接收人（offeree）发起，可补差价；
// 双方在 pending / countered 两个生效状态间最多还价一次，随后只能接受或拒绝。
type ExchangeProposal struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	ProposalNo string `gorm:"size:40;uniqueIndex;not null" json:"proposal_no"`
	OfferorID  uint   `gorm:"not null;index" json:"offeror_id"`
	OffereeID  uint   `gorm:"not null;index" json:"offeree_id"`
	// Status 提案状态：pending/countered/accepted/rejected/cancelled/expired，枚举见 constants。
	Status string `gorm:"size:32;not null;default:pending;index" json:"status"`
	// Turn 下一个有动作权的一方：offeror/offeree（pending 时为 offeree，countered 时为 offeror）。
	Turn         string         `gorm:"size:16;not null;default:offeree" json:"turn"`
	Round        int            `gorm:"not null;default:0" json:"round"`                            // 还价轮次：0=初始，1=已还价一次
	Note         string         `gorm:"type:text" json:"note"`                                      // 最新说明（还价时覆盖）
	TopUpAmount  float64        `gorm:"type:numeric(12,2);not null;default:0" json:"top_up_amount"` // 最新补差价（由 offeror 支付给 offeree；还价时覆盖）
	TopUpPayer   string         `gorm:"size:16;not null;default:offeror" json:"top_up_payer"`       // 差价支付方：offeror/offeree
	AcceptedAt   *time.Time     `json:"accepted_at,omitempty"`
	RejectedAt   *time.Time     `json:"rejected_at,omitempty"`
	CancelledAt  *time.Time     `json:"cancelled_at,omitempty"`
	ExpiresAt    time.Time      `gorm:"not null;index" json:"expires_at"`
	LastActionBy uint           `gorm:"not null;default:0" json:"last_action_by"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`

	Offeror *User `gorm:"foreignKey:OfferorID" json:"offeror,omitempty"`
	Offeree *User `gorm:"foreignKey:OffereeID" json:"offeree,omitempty"`
	// Items 涉及物品快照，Side 区分 offer（发起方换出）/ target（对方换入）。
	Items []ExchangeProposalItem `gorm:"foreignKey:ProposalID;constraint:OnDelete:CASCADE" json:"items,omitempty"`
	// History 完整操作历史，按时间升序。
	History []ExchangeProposalHistory `gorm:"foreignKey:ProposalID;constraint:OnDelete:CASCADE" json:"history,omitempty"`
}

// ExchangeProposalItem 提案物品清单：side 区分发起方物品(offer)/接收方物品(target)。
// 以创建/还价时的快照保存标题、价格、图片，物品事后下架或改名不影响提案回读。
type ExchangeProposalItem struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ProposalID uint      `gorm:"not null;index:idx_exchange_item_proposal_side,priority:1" json:"proposal_id"`
	ProductID  uint      `gorm:"not null" json:"product_id"`
	OwnerID    uint      `gorm:"not null" json:"owner_id"`
	Side       string    `gorm:"size:16;not null;index:idx_exchange_item_proposal_side,priority:2" json:"side"` // offer/target
	Title      string    `gorm:"size:128;not null" json:"title"`
	Price      float64   `gorm:"type:numeric(12,2);not null;default:0" json:"price"`
	Image      string    `gorm:"size:512" json:"image"`
	CreatedAt  time.Time `json:"created_at"`

	Product *Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
}

// ExchangeProposalHistory 提案操作历史：发起/还价/接受/拒绝/取消/超时均落一行，双方可完整回读。
type ExchangeProposalHistory struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ProposalID  uint      `gorm:"not null;index" json:"proposal_id"`
	ActorID     uint      `gorm:"not null" json:"actor_id"`
	ActorRole   string    `gorm:"size:16;not null" json:"actor_role"` // offeror/offeree/system
	Action      string    `gorm:"size:32;not null" json:"action"`     // created/countered/accepted/rejected/cancelled/expired
	Note        string    `gorm:"type:text" json:"note"`
	TopUpAmount float64   `gorm:"type:numeric(12,2);not null;default:0" json:"top_up_amount"`
	TopUpPayer  string    `gorm:"size:16;not null;default:offeror" json:"top_up_payer"`
	CreatedAt   time.Time `json:"created_at"`

	Actor *User `gorm:"foreignKey:ActorID" json:"actor,omitempty"`
}
