package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/model"
	"gorm.io/gorm"
)

// ExchangeRepository 换物提案仓储接口。
type ExchangeRepository interface {
	// CreateWithTx 事务内创建提案、物品快照与首条历史，全部成功或全部回滚。
	CreateWithTx(tx *gorm.DB, proposal *model.ExchangeProposal, items []model.ExchangeProposalItem) error
	GetByID(id uint) (*model.ExchangeProposal, error)
	GetByIDForUpdate(tx *gorm.DB, id uint) (*model.ExchangeProposal, error)
	ListByParty(partyID uint, role string, status string, page, pageSize int) ([]model.ExchangeProposal, int64, error)
	ListProductIDsByProposalTx(tx *gorm.DB, proposalID uint) ([]uint, error)
	// CompareAndUpdateStatusTx 条件更新：仅当提案当前状态 ∈ fromStatuses 时写入 fields（须含 status），返回是否命中。
	// 并发接受/拒绝/取消/超时通过该 CAS 保证同一时刻只有一个终态动作成功。
	CompareAndUpdateStatusTx(tx *gorm.DB, id uint, fromStatuses []string, fields map[string]interface{}) (bool, error)
	CreateHistoryWithTx(tx *gorm.DB, history *model.ExchangeProposalHistory) error
	// CountActiveByProductIDsTx 统计给定物品当前被多少个生效提案占用（须在物品行锁后调用，保证并发准确）。
	CountActiveByProductIDsTx(tx *gorm.DB, productIDs []uint, excludeProposalID uint) (map[uint]int64, error)
	// AutoCloseOthersByProductIDsTx 将涉及任一给定物品的其他生效提案置为 status 并补系统历史（接受时原子结束其他提案）。
	AutoCloseOthersByProductIDsTx(tx *gorm.DB, productIDs []uint, winnerProposalID uint, status string) (int64, error)
	// ExpireDueTx 原子结束所有已到期生效提案（SKIP LOCKED 防并发重复处理），补系统历史，返回处理数量。
	ExpireDueTx(tx *gorm.DB, limit int) (int64, error)
}

type exchangeRepo struct {
	db *gorm.DB
}

// NewExchangeRepository 构造换物提案仓储。
func NewExchangeRepository(db *gorm.DB) ExchangeRepository {
	return &exchangeRepo{db: db}
}

func (r *exchangeRepo) CreateWithTx(tx *gorm.DB, proposal *model.ExchangeProposal, items []model.ExchangeProposalItem) error {
	if err := tx.Create(proposal).Error; err != nil {
		return fmt.Errorf("create exchange proposal: %w", err)
	}
	for i := range items {
		items[i].ProposalID = proposal.ID
	}
	if len(items) > 0 {
		if err := tx.Create(&items).Error; err != nil {
			return fmt.Errorf("create exchange proposal items: %w", err)
		}
	}
	history := model.ExchangeProposalHistory{
		ProposalID:  proposal.ID,
		ActorID:     proposal.OfferorID,
		ActorRole:   constants.ExchangePartyOfferor,
		Action:      constants.ExchangeActionCreated,
		Note:        proposal.Note,
		TopUpAmount: proposal.TopUpAmount,
		TopUpPayer:  proposal.TopUpPayer,
	}
	if err := tx.Create(&history).Error; err != nil {
		return fmt.Errorf("create exchange proposal history: %w", err)
	}
	return nil
}

func (r *exchangeRepo) GetByID(id uint) (*model.ExchangeProposal, error) {
	var p model.ExchangeProposal
	err := r.db.
		Preload("Offeror").Preload("Offeree").
		Preload("Items").Preload("Items.Product").
		Preload("History").Preload("History.Actor").
		First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get exchange proposal by id %d: %w", id, err)
	}
	return &p, nil
}

func (r *exchangeRepo) GetByIDForUpdate(tx *gorm.DB, id uint) (*model.ExchangeProposal, error) {
	var p model.ExchangeProposal
	err := tx.Clauses(clauseLocking()).First(&p, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get exchange proposal by id %d for update: %w", id, err)
	}
	return &p, nil
}

func (r *exchangeRepo) ListByParty(partyID uint, role string, status string, page, pageSize int) ([]model.ExchangeProposal, int64, error) {
	var proposals []model.ExchangeProposal
	var total int64
	q := r.db.Model(&model.ExchangeProposal{})
	switch role {
	case "initiator":
		q = q.Where("offeror_id = ?", partyID)
	case "recipient":
		q = q.Where("offeree_id = ?", partyID)
	default:
		q = q.Where("offeror_id = ? OR offeree_id = ?", partyID, partyID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count exchange proposals party=%d: %w", partyID, err)
	}
	if err := q.
		Preload("Offeror").Preload("Offeree").
		Preload("Items").Preload("Items.Product").
		Preload("History").
		Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&proposals).Error; err != nil {
		return nil, 0, fmt.Errorf("list exchange proposals party=%d: %w", partyID, err)
	}
	return proposals, total, nil
}

func (r *exchangeRepo) ListProductIDsByProposalTx(tx *gorm.DB, proposalID uint) ([]uint, error) {
	ids := make([]uint, 0)
	if err := tx.Model(&model.ExchangeProposalItem{}).
		Where("proposal_id = ?", proposalID).
		Order("product_id ASC").
		Pluck("product_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("list product ids of exchange proposal %d: %w", proposalID, err)
	}
	return ids, nil
}

func (r *exchangeRepo) CompareAndUpdateStatusTx(tx *gorm.DB, id uint, fromStatuses []string, fields map[string]interface{}) (bool, error) {
	if _, ok := fields["status"]; !ok {
		return false, fmt.Errorf("compare-and-update exchange proposal %d: fields must contain status", id)
	}
	res := tx.Model(&model.ExchangeProposal{}).
		Where("id = ?", id).
		Where("status IN ?", fromStatuses).
		Updates(fields)
	if res.Error != nil {
		return false, fmt.Errorf("compare-and-update exchange proposal %d: %w", id, res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (r *exchangeRepo) CreateHistoryWithTx(tx *gorm.DB, history *model.ExchangeProposalHistory) error {
	if err := tx.Create(history).Error; err != nil {
		return fmt.Errorf("create exchange history: %w", err)
	}
	return nil
}

func (r *exchangeRepo) CountActiveByProductIDsTx(tx *gorm.DB, productIDs []uint, excludeProposalID uint) (map[uint]int64, error) {
	result := make(map[uint]int64)
	if len(productIDs) == 0 {
		return result, nil
	}
	type row struct {
		ProductID uint
		Cnt       int64
	}
	var rows []row
	q := tx.Model(&model.ExchangeProposalItem{}).
		Select("exchange_proposal_items.product_id, COUNT(DISTINCT exchange_proposal_items.proposal_id) AS cnt").
		Joins("JOIN exchange_proposals ON exchange_proposals.id = exchange_proposal_items.proposal_id").
		Where("exchange_proposal_items.product_id IN ?", productIDs).
		Where("exchange_proposals.status IN ?", constants.ExchangeActiveStatuses).
		Where("exchange_proposals.deleted_at IS NULL")
	if excludeProposalID > 0 {
		q = q.Where("exchange_proposal_items.proposal_id <> ?", excludeProposalID)
	}
	if err := q.Group("exchange_proposal_items.product_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count active exchange proposals by products: %w", err)
	}
	for _, rw := range rows {
		result[rw.ProductID] = rw.Cnt
	}
	return result, nil
}

func (r *exchangeRepo) AutoCloseOthersByProductIDsTx(tx *gorm.DB, productIDs []uint, winnerProposalID uint, status string) (int64, error) {
	if len(productIDs) == 0 {
		return 0, nil
	}
	// 子查询找出涉及任一物品的其他生效提案 id（接受方事务已对物品加行锁，天然串行）。
	subQuery := tx.Model(&model.ExchangeProposalItem{}).
		Select("proposal_id").
		Where("product_id IN ?", productIDs)
	var ids []uint
	if err := tx.Model(&model.ExchangeProposal{}).
		Where("id <> ?", winnerProposalID).
		Where("status IN ?", constants.ExchangeActiveStatuses).
		Where("id IN (?)", subQuery).
		Pluck("id", &ids).Error; err != nil {
		return 0, fmt.Errorf("find other exchange proposals to close: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	res := tx.Model(&model.ExchangeProposal{}).Where("id IN ?", ids).
		Updates(map[string]interface{}{"status": status, "last_action_by": 0})
	if res.Error != nil {
		return 0, fmt.Errorf("auto close other exchange proposals: %w", res.Error)
	}
	// 为每个被结束的提案补一条系统历史，保证双方历史完整可回读。
	now := time.Now()
	histories := make([]model.ExchangeProposalHistory, 0, len(ids))
	for _, pid := range ids {
		histories = append(histories, model.ExchangeProposalHistory{
			ProposalID: pid,
			ActorID:    0,
			ActorRole:  constants.ExchangePartySystem,
			Action:     constants.ExchangeActionCancelled,
			Note:       "关联物品已在另一个换物提案中成交，本提案自动结束",
			CreatedAt:  now,
		})
	}
	if err := tx.Create(&histories).Error; err != nil {
		return 0, fmt.Errorf("write auto-close exchange histories: %w", err)
	}
	return int64(len(ids)), nil
}

func (r *exchangeRepo) ExpireDueTx(tx *gorm.DB, limit int) (int64, error) {
	now := time.Now()
	// 候选到期提案（不加锁；真正的串行化由下面的条件 UPDATE 行锁保证）。
	var ids []uint
	if err := tx.Model(&model.ExchangeProposal{}).
		Where("status IN ?", constants.ExchangeActiveStatuses).
		Where("expires_at < ?", now).
		Order("expires_at ASC").Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return 0, fmt.Errorf("list due exchange proposals: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	// 原子条件更新：仅生效中的候选会被置为 expired；并发接受/取消已提交的行不会被命中。
	res := tx.Model(&model.ExchangeProposal{}).
		Where("id IN ?", ids).
		Where("status IN ?", constants.ExchangeActiveStatuses).
		Where("expires_at < ?", now).
		Update("status", constants.ExchangeStatusExpired)
	if res.Error != nil {
		return 0, fmt.Errorf("expire due exchange proposals: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return 0, nil
	}
	// 仅为本事务刚失效且尚无 expired 历史的提案补系统历史（NOT EXISTS 防多实例重复补写）。
	insertSQL := `
		INSERT INTO exchange_proposal_histories
			(proposal_id, actor_id, actor_role, action, note, top_up_amount, top_up_payer, created_at)
		SELECT ep.id, 0, ?, ?, ?, 0, 'offeror', ?
		FROM exchange_proposals ep
		WHERE ep.id IN (?)
		  AND ep.status = ?
		  AND NOT EXISTS (
			SELECT 1 FROM exchange_proposal_histories h
			WHERE h.proposal_id = ep.id AND h.action = ?
		  )`
	insertRes := tx.Exec(insertSQL,
		constants.ExchangePartySystem,
		constants.ExchangeActionExpired,
		"提案超过回应期限未处理，已自动失效并释放物品",
		now,
		ids,
		constants.ExchangeStatusExpired,
		constants.ExchangeActionExpired,
	)
	if insertRes.Error != nil {
		return 0, fmt.Errorf("write expire exchange histories: %w", insertRes.Error)
	}
	return insertRes.RowsAffected, nil
}
