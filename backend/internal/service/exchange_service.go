package service

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/dto"
	"github.com/marketpal/marketpal/internal/model"
	"github.com/marketpal/marketpal/internal/repository"
	"github.com/marketpal/marketpal/internal/util"
	"gorm.io/gorm"
)

// ExchangeService 换物提案业务服务。
// 并发模型：涉及物品统一在事务内按 id 升序 SELECT ... FOR UPDATE 加锁；
// 提案行状态流转使用“条件更新（CAS：WHERE status IN ...）”，保证并发接受只有一个成功。
type ExchangeService struct {
	exchangeRepo repository.ExchangeRepository
	productRepo  repository.ProductRepository
	db           *gorm.DB
	logger       *slog.Logger
}

// NewExchangeService 构造换物提案服务。
func NewExchangeService(db *gorm.DB, exchangeRepo repository.ExchangeRepository, productRepo repository.ProductRepository, logger *slog.Logger) *ExchangeService {
	return &ExchangeService{db: db, exchangeRepo: exchangeRepo, productRepo: productRepo, logger: logger}
}

// Create 发起换物提案（事务）：
// 锁全部涉及物品 → 校验归属/在售/去重/无重叠 → 校验无其他生效提案占用 → 建提案+快照+历史。
func (s *ExchangeService) Create(offerorID uint, req dto.ExchangeCreateRequest) (*model.ExchangeProposal, error) {
	offerIDs := dedupPositive(req.OfferProductIDs)
	targetIDs := dedupPositive(req.TargetProductIDs)
	if len(offerIDs) == 0 || len(targetIDs) == 0 {
		return nil, utilAppError(constants.CodeExchangeInvalidItems,
			"发起换物提案失败：换出物品与换入物品都至少需要一件", nil)
	}
	if overlap(offerIDs, targetIDs) {
		return nil, utilAppError(constants.CodeExchangeInvalidItems,
			"发起换物提案失败：换出与换入物品不能重复（用户 id="+fmt.Sprint(offerorID)+"）", nil)
	}
	topUpPayer := req.TopUpPayer
	if topUpPayer == "" {
		topUpPayer = constants.ExchangePartyOfferor
	}
	ttl := req.TTLHours
	if ttl <= 0 {
		ttl = constants.ExchangeDefaultTTLHours
	}

	var proposal *model.ExchangeProposal
	err := s.db.Transaction(func(tx *gorm.DB) error {
		allIDs := append(append([]uint{}, offerIDs...), targetIDs...)
		sort.Slice(allIDs, func(i, j int) bool { return allIDs[i] < allIDs[j] })
		allIDs = dedupPositive(allIDs)

		// 统一对涉及物品加行锁（按 id 升序，避免跨事务死锁）。
		products, err := s.productRepo.ListByIDsForUpdate(tx, allIDs)
		if err != nil {
			return fmt.Errorf("lock exchange products %v: %w", allIDs, err)
		}
		productMap := make(map[uint]*model.Product, len(products))
		for i := range products {
			productMap[products[i].ID] = &products[i]
		}
		offereeID, err := s.validateCreateItems(offerorID, offerIDs, targetIDs, productMap)
		if err != nil {
			return err
		}
		// 行锁内复查：任一物品已被其他生效提案占用则拒绝（同一物品只能有一个生效提案）。
		busy, err := s.exchangeRepo.CountActiveByProductIDsTx(tx, allIDs, 0)
		if err != nil {
			return fmt.Errorf("check busy exchange products: %w", err)
		}
		for pid, cnt := range busy {
			if cnt > 0 {
				s.logger.Warn(constants.LogExchangeItemsBusy, "offeror_id", offerorID, "product_ids", pid)
				return utilAppError(constants.CodeExchangeItemConflict,
					fmt.Sprintf("发起换物提案失败：物品 id=%d 已存在生效中的换物提案，同一物品同时只能有一个生效提案", pid), nil)
			}
		}

		now := time.Now()
		p := &model.ExchangeProposal{
			ProposalNo:   genProposalNo(),
			OfferorID:    offerorID,
			OffereeID:    offereeID,
			Status:       constants.ExchangeStatusPending,
			Turn:         constants.ExchangePartyOfferee,
			Round:        0,
			Note:         strings.TrimSpace(req.Note),
			TopUpAmount:  req.TopUpAmount,
			TopUpPayer:   topUpPayer,
			ExpiresAt:    now.Add(time.Duration(ttl) * time.Hour),
			LastActionBy: offerorID,
		}
		items := buildExchangeItems(offerIDs, targetIDs, productMap)
		if err := s.exchangeRepo.CreateWithTx(tx, p, items); err != nil {
			return fmt.Errorf("create exchange proposal offeror=%d: %w", offerorID, err)
		}
		proposal = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogExchangeCreated,
		"proposal_no", proposal.ProposalNo, "offeror_id", proposal.OfferorID, "offeree_id", proposal.OffereeID,
		"offer_items", len(offerIDs), "target_items", len(targetIDs),
		"top_up", proposal.TopUpAmount, "payer", proposal.TopUpPayer, "status", proposal.Status)
	return s.exchangeRepo.GetByID(proposal.ID)
}

// Accept 接受提案（事务）：
// 锁物品校验在售 → CAS 终态化（并发接受仅一个成功）→ 物品置 sold → 原子结束其他生效提案。
func (s *ExchangeService) Accept(userID, proposalID uint) (*model.ExchangeProposal, error) {
	var proposal *model.ExchangeProposal
	var closedOthers int64
	var lockedItems int
	err := s.runActionTx(proposalID, func(tx *gorm.DB, now time.Time) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, constants.ExchangeActionAccepted, now)
		if err != nil {
			return err
		}
		productIDs, err := s.exchangeRepo.ListProductIDsByProposalTx(tx, p.ID)
		if err != nil {
			return err
		}
		products, err := s.productRepo.ListByIDsForUpdate(tx, productIDs)
		if err != nil {
			return fmt.Errorf("lock exchange products %v on accept: %w", productIDs, err)
		}
		for _, prod := range products {
			if prod.Status != constants.ProductStatusOnSale {
				return utilAppError(constants.CodeExchangeItemConflict,
					fmt.Sprintf("接受换物提案失败：物品 id=%d 当前状态为 %s，已无法交换", prod.ID, prod.Status), nil)
			}
		}
		// CAS：仅 pending/countered 可接受；并发下只有一个动作能把行改为 accepted。
		ok, err := s.exchangeRepo.CompareAndUpdateStatusTx(tx, p.ID,
			constants.ExchangeStatusTransitions[constants.ExchangeStatusAccepted],
			map[string]interface{}{
				"status":         constants.ExchangeStatusAccepted,
				"accepted_at":    &now,
				"last_action_by": userID,
			})
		if err != nil {
			return err
		}
		if !ok {
			return utilAppError(constants.CodeExchangeStateInvalid,
				"接受换物提案失败：提案 "+p.ProposalNo+" 已被处理或状态已变更", nil)
		}
		// 成交：锁定（置为已售出）涉及物品。
		for _, prod := range products {
			if err := s.productRepo.UpdateStatusForUpdate(tx, prod.ID, constants.ProductStatusSold); err != nil {
				return fmt.Errorf("lock product %d sold on exchange accept: %w", prod.ID, err)
			}
		}
		// 原子结束涉及任一相同物品的其他生效提案，释放其占用语义。
		closed, err := s.exchangeRepo.AutoCloseOthersByProductIDsTx(tx, productIDs, p.ID, constants.ExchangeStatusCancelled)
		if err != nil {
			return err
		}
		if err := s.exchangeRepo.CreateHistoryWithTx(tx, &model.ExchangeProposalHistory{
			ProposalID:  p.ID,
			ActorID:     userID,
			ActorRole:   actorRole,
			Action:      constants.ExchangeActionAccepted,
			Note:        p.Note,
			TopUpAmount: p.TopUpAmount,
			TopUpPayer:  p.TopUpPayer,
		}); err != nil {
			return err
		}
		closedOthers = closed
		lockedItems = len(products)
		proposal = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogExchangeAccepted,
		"proposal_no", proposal.ProposalNo, "offeror_id", proposal.OfferorID, "offeree_id", proposal.OffereeID,
		"locked_items", lockedItems, "cancelled_others", closedOthers, "status", constants.ExchangeStatusAccepted)
	return s.exchangeRepo.GetByID(proposal.ID)
}

// Reject 接收人拒绝提案（仅 pending）：CAS 终态化，释放物品占用（不再有生效提案指向）。
func (s *ExchangeService) Reject(userID, proposalID uint, note string) (*model.ExchangeProposal, error) {
	return s.terminate(userID, proposalID, constants.ExchangeActionRejected,
		constants.ExchangeStatusRejected, "拒绝换物提案失败", note)
}

// Cancel 发起人取消提案：CAS 终态化并释放物品占用。
func (s *ExchangeService) Cancel(userID, proposalID uint, note string) (*model.ExchangeProposal, error) {
	return s.terminate(userID, proposalID, constants.ExchangeActionCancelled,
		constants.ExchangeStatusCancelled, "取消换物提案失败", note)
}

// terminate 拒绝/取消共用同一套事务与 CAS（两个接口复用同一 service 私有方法）。
func (s *ExchangeService) terminate(userID, proposalID uint, action, targetStatus, errPrefix, note string) (*model.ExchangeProposal, error) {
	var proposal *model.ExchangeProposal
	err := s.runActionTx(proposalID, func(tx *gorm.DB, now time.Time) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, action, now)
		if err != nil {
			return err
		}
		fields := map[string]interface{}{
			"status":         targetStatus,
			"last_action_by": userID,
		}
		if targetStatus == constants.ExchangeStatusRejected {
			fields["rejected_at"] = &now
		} else {
			fields["cancelled_at"] = &now
		}
		ok, err := s.exchangeRepo.CompareAndUpdateStatusTx(tx, p.ID,
			constants.ExchangeStatusTransitions[targetStatus],
			fields)
		if err != nil {
			return err
		}
		if !ok {
			return utilAppError(constants.CodeExchangeStateInvalid,
				errPrefix+"：提案 "+p.ProposalNo+" 已被处理或状态已变更", nil)
		}
		if err := s.exchangeRepo.CreateHistoryWithTx(tx, &model.ExchangeProposalHistory{
			ProposalID: p.ID,
			ActorID:    userID,
			ActorRole:  actorRole,
			Action:     action,
			Note:       strings.TrimSpace(note),
		}); err != nil {
			return err
		}
		proposal = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	logTpl := constants.LogExchangeRejected
	if targetStatus == constants.ExchangeStatusCancelled {
		logTpl = constants.LogExchangeCancelled
	}
	s.logger.Info(logTpl, "proposal_no", proposal.ProposalNo, "operator", userID, "status", targetStatus)
	return s.exchangeRepo.GetByID(proposal.ID)
}

// Counter 还价一次（事务）：pending 由接收人还价 → countered（动作权回到发起人）。
// 仅可还价一次：countered 后不再允许还价，只能接受/拒绝/取消。
func (s *ExchangeService) Counter(userID, proposalID uint, req dto.ExchangeCounterRequest) (*model.ExchangeProposal, error) {
	var proposal *model.ExchangeProposal
	err := s.runActionTx(proposalID, func(tx *gorm.DB, now time.Time) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, constants.ExchangeActionCountered, now)
		if err != nil {
			return err
		}
		note := strings.TrimSpace(req.Note)
		if note == "" {
			note = p.Note
		}
		payer := req.TopUpPayer
		if payer == "" {
			payer = p.TopUpPayer
		}
		// CAS：仅 pending → countered。
		ok, err := s.exchangeRepo.CompareAndUpdateStatusTx(tx, p.ID,
			constants.ExchangeStatusTransitions[constants.ExchangeStatusCountered],
			map[string]interface{}{
				"status":         constants.ExchangeStatusCountered,
				"turn":           constants.ExchangePartyOfferor,
				"round":          1,
				"note":           note,
				"top_up_amount":  req.TopUpAmount,
				"top_up_payer":   payer,
				"last_action_by": userID,
			})
		if err != nil {
			return err
		}
		if !ok {
			return utilAppError(constants.CodeExchangeStateInvalid,
				"还价失败：提案 "+p.ProposalNo+" 已被处理或状态已变更", nil)
		}
		if err := s.exchangeRepo.CreateHistoryWithTx(tx, &model.ExchangeProposalHistory{
			ProposalID:  p.ID,
			ActorID:     userID,
			ActorRole:   actorRole,
			Action:      constants.ExchangeActionCountered,
			Note:        note,
			TopUpAmount: req.TopUpAmount,
			TopUpPayer:  payer,
		}); err != nil {
			return err
		}
		p.Status = constants.ExchangeStatusCountered
		proposal = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.logger.Info(constants.LogExchangeCountered,
		"proposal_no", proposal.ProposalNo, "operator", userID, "round", 1,
		"top_up", req.TopUpAmount, "payer", req.TopUpPayer, "status", constants.ExchangeStatusCountered)
	return s.exchangeRepo.GetByID(proposal.ID)
}

// GetDetail 提案详情（仅参与双方可查看，含完整历史）。
func (s *ExchangeService) GetDetail(userID, proposalID uint) (*model.ExchangeProposal, error) {
	p, err := s.exchangeRepo.GetByID(proposalID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, utilAppError(constants.CodeExchangeNotFound,
				"换物提案详情查询失败：提案 id="+fmt.Sprint(proposalID)+" 不存在", err)
		}
		return nil, fmt.Errorf("get exchange proposal detail %d: %w", proposalID, err)
	}
	if p.OfferorID != userID && p.OffereeID != userID {
		return nil, utilAppError(constants.CodeNotExchangeParty,
			"换物提案详情查询失败：用户 id="+fmt.Sprint(userID)+" 不是提案 "+p.ProposalNo+" 的参与方", nil)
	}
	return p, nil
}

// List 提案列表（initiator 我发起的 / recipient 我收到的，默认全部参与）。
func (s *ExchangeService) List(userID uint, q dto.ExchangeQuery) (*dto.ExchangeListResponse, error) {
	page, pageSize := q.Page, q.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	proposals, total, err := s.exchangeRepo.ListByParty(userID, q.Role, q.Status, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list exchange proposals user=%d role=%s: %w", userID, q.Role, err)
	}
	list := make([]dto.ExchangeProposalVO, 0, len(proposals))
	for i := range proposals {
		list = append(list, ToExchangeProposalVO(&proposals[i]))
	}
	return &dto.ExchangeListResponse{List: list, Total: total, Page: page, Size: pageSize}, nil
}

// ExpireDue 后台超时清理：与动作惰性收口共用仓储方法 SettleExpiredTx，到期提案 CAS 置 expired 并补幂等历史。
// 由后台 ticker 周期调用；重复执行不会重复写历史，并发接受与清理只能成功一个。
func (s *ExchangeService) ExpireDue() (int64, error) {
	var n int64
	err := s.runInTransaction(func(tx *gorm.DB) error {
		now := time.Now()
		ids, err := s.exchangeRepo.LockDueExpiredIDsTx(tx, now, 200)
		if err != nil {
			return err
		}
		n, err = s.exchangeRepo.SettleExpiredTx(tx, ids, now)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("expire due exchange proposals: %w", err)
	}
	if n > 0 {
		s.logger.Info(constants.LogExchangeExpired, "proposal_no", "-", "operator", 0, "released_items", n, "status", constants.ExchangeStatusExpired)
	}
	return n, nil
}

// runInTransaction 手工事务：fn 返回任何错误都回滚并原样返回。
// 用于动作场景：动作前置校验若发现提案到期会返回内部哨兵 errRollback，
// 由 runActionTx 捕获后另起事务提交到期收口，再转成明确的“已超时”业务错误。
// 不能使用 db.Transaction：它对任何返回 error（含正常的业务拒绝）都回滚，
// 会把同一事务内已完成的到期收口一并撤销，导致状态停留、物品不释放。
func (s *ExchangeService) runInTransaction(fn func(tx *gorm.DB) error) error {
	tx := s.db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin transaction: %w", tx.Error)
	}
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback().Error; rbErr != nil {
			return fmt.Errorf("rollback: %v (orig: %w)", rbErr, err)
		}
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// runActionTx 动作统一事务封装：在手工事务内执行 fn；
// 若 fn 因提案到期返回 errRollback，则回滚动作事务、另起事务提交到期收口（与后台清理共用 SettleExpiredTx），
// 再返回明确的“已超时”业务错误。并发接受与清理因此只有一个成功。
func (s *ExchangeService) runActionTx(proposalID uint, fn func(tx *gorm.DB, now time.Time) error) error {
	now := time.Now()
	err := s.runInTransaction(func(tx *gorm.DB) error {
		return fn(tx, now)
	})
	if !errors.Is(err, errRollback) {
		return err
	}
	// 动作事务已回滚；独立提交到期收口，保证状态转 expired、历史落库、物品立即释放。
	settled, sErr := s.settleExpiredProposal(proposalID, now)
	if sErr != nil {
		return sErr
	}
	p, _ := s.exchangeRepo.GetByID(proposalID)
	if settled || (p != nil && p.Status == constants.ExchangeStatusExpired) {
		if p != nil {
			s.logger.Info(constants.LogExchangeExpired,
				"proposal_no", p.ProposalNo, "operator", 0, "released_items", len(p.Items),
				"status", constants.ExchangeStatusExpired)
		}
		return exchangeExpiredError(p)
	}
	// 收口未命中且状态非 expired：动作事务与收口事务之间，提案已被并发接受/拒绝/取消提交。
	return utilAppError(constants.CodeExchangeStateInvalid,
		"换物提案操作失败：提案已被处理或状态已变更", nil)
}

// errRollback 内部哨兵：提案已到期但仍为生效态，动作必须中止并触发独立收口。
var errRollback = errors.New("exchange proposal due for expiry, action must abort")

// exchangeExpiredError 返回统一的“已超时”业务错误（动作必须明确失败）。
func exchangeExpiredError(p *model.ExchangeProposal) error {
	no := ""
	if p != nil {
		no = p.ProposalNo
	}
	return utilAppError(constants.CodeExchangeExpired,
		"换物提案操作失败：提案 "+no+" 已超过回应期限，自动失效，物品已释放", nil)
}

// loadActiveForAction 在调用方已开启的 tx 内读取提案并做动作前置校验：
// 存在 → 参与方 → 未超时 → 还价次数 → 状态可流转 → 回合/身份。
// 若提案已到期但仍为生效态，返回 errRollback：调用方应回滚当前 tx，
// 另起独立事务用 SettleExpiredTx 提交收口，再向调用方返回明确的“已超时”失败。
// 这样可避免“收口写操作与业务错误在同一事务被 GORM 一并回滚”的缺陷。
func (s *ExchangeService) loadActiveForAction(tx *gorm.DB, userID, proposalID uint, action string, now time.Time) (*model.ExchangeProposal, string, error) {
	p, err := s.exchangeRepo.GetByIDForUpdate(tx, proposalID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, "", utilAppError(constants.CodeExchangeNotFound,
				"换物提案操作失败：提案 id="+fmt.Sprint(proposalID)+" 不存在", err)
		}
		return nil, "", fmt.Errorf("lock exchange proposal %d: %w", proposalID, err)
	}
	var actorRole string
	switch userID {
	case p.OfferorID:
		actorRole = constants.ExchangePartyOfferor
	case p.OffereeID:
		actorRole = constants.ExchangePartyOfferee
	default:
		return nil, "", utilAppError(constants.CodeNotExchangeParty,
			"换物提案操作失败：用户 id="+fmt.Sprint(userID)+" 不是提案 "+p.ProposalNo+" 的参与方", nil)
	}
	// 已到期但仍是生效态：不在本事务收口（否则会随动作错误一起回滚），交由调用方独立提交。
	if constants.IsExchangeActive(p.Status) && now.After(p.ExpiresAt) {
		return p, actorRole, errRollback
	}
	// 还价次数校验优先于状态/回合校验：countered 后再次还价明确提示“仅可还价一次”。
	if action == constants.ExchangeActionCountered && p.Round >= 1 {
		return nil, "", utilAppError(constants.CodeExchangeRoundExhausted,
			"还价失败：提案 "+p.ProposalNo+" 已还价过一次，无法再次还价", nil)
	}
	if !canExchangeTransition(p.Status, targetStatusByAction(action)) {
		// 已是 expired 终态（后台已清理）单独给出明确错误，其余为状态冲突。
		if p.Status == constants.ExchangeStatusExpired {
			return nil, "", exchangeExpiredError(p)
		}
		return nil, "", utilAppError(constants.CodeExchangeStateInvalid,
			fmt.Sprintf("换物提案操作失败：提案 %s 当前状态 %s 不允许执行该动作", p.ProposalNo, p.Status), nil)
	}
	// 回合/身份校验。
	switch action {
	case constants.ExchangeActionAccepted, constants.ExchangeActionRejected, constants.ExchangeActionCountered:
		// 这三个动作只属于“当前回合方”：pending 时为接收人（接受/拒绝/还价），
		// countered 时回到发起人（接受/拒绝还价方案）。
		if actorRole != p.Turn {
			return nil, "", utilAppError(constants.CodeExchangeTurnInvalid,
				fmt.Sprintf("换物提案操作失败：当前应由 %s 回应提案 %s，%s 暂无动作权", p.Turn, p.ProposalNo, actorRole), nil)
		}
	case constants.ExchangeActionCancelled:
		// 取消始终只属于发起人（等待对方回应期间可主动撤回）。
		if actorRole != constants.ExchangePartyOfferor {
			return nil, "", utilAppError(constants.CodeNotExchangeParty,
				"换物提案操作失败：仅发起人可取消提案 "+p.ProposalNo, nil)
		}
	}
	return p, actorRole, nil
}

// settleExpiredProposal 单个提案的到期收口（动作惰性路径）：
// 另起事务，行锁该提案后仅在仍生效且到期时 CAS 置 expired 并补幂等历史。
// 返回 settled=true 表示本调用完成了收口；false 表示提案已被并发接受/拒绝/取消（收口未命中）。
// 与后台 ExpireDue 共用同一仓储方法 SettleExpiredTx，保证收口逻辑一致。
func (s *ExchangeService) settleExpiredProposal(proposalID uint, now time.Time) (bool, error) {
	settled := false
	err := s.runInTransaction(func(tx *gorm.DB) error {
		// 先锁提案行，缩小候选；真正收口仍由 SettleExpiredTx 的条件 UPDATE 决定。
		p, err := s.exchangeRepo.GetByIDForUpdate(tx, proposalID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return utilAppError(constants.CodeExchangeNotFound,
					"换物提案操作失败：提案 id="+fmt.Sprint(proposalID)+" 不存在", err)
			}
			return fmt.Errorf("lock exchange proposal %d for expiry: %w", proposalID, err)
		}
		if !constants.IsExchangeActive(p.Status) || !now.After(p.ExpiresAt) {
			return nil // 已被并发接受/拒绝/取消/清理：无需收口。
		}
		n, err := s.exchangeRepo.SettleExpiredTx(tx, []uint{proposalID}, now)
		if err != nil {
			return err
		}
		settled = n > 0
		return nil
	})
	return settled, err
}

// validateCreateItems 校验发起提案的物品归属、在售、去重，并返回接收人 id（对方物品的所有者）。
func (s *ExchangeService) validateCreateItems(offerorID uint, offerIDs, targetIDs []uint, products map[uint]*model.Product) (uint, error) {
	var offereeID uint
	for _, id := range offerIDs {
		p, ok := products[id]
		if !ok {
			return 0, utilAppError(constants.CodeProductNotFound,
				fmt.Sprintf("发起换物提案失败：换出物品 id=%d 不存在", id), nil)
		}
		if p.SellerID != offerorID {
			return 0, utilAppError(constants.CodeForbidden,
				fmt.Sprintf("发起换物提案失败：只能用自己发布的物品发起，物品 id=%d 的所有者为用户 %d，而非当前用户 %d", id, p.SellerID, offerorID), nil)
		}
		if p.Status != constants.ProductStatusOnSale {
			return 0, utilAppError(constants.CodeExchangeItemConflict,
				fmt.Sprintf("发起换物提案失败：自己的物品 id=%d 当前状态为 %s，非在售不可换", id, p.Status), nil)
		}
	}
	for _, id := range targetIDs {
		p, ok := products[id]
		if !ok {
			return 0, utilAppError(constants.CodeProductNotFound,
				fmt.Sprintf("发起换物提案失败：换入物品 id=%d 不存在", id), nil)
		}
		if p.SellerID == offerorID {
			return 0, utilAppError(constants.CodeExchangeInvalidItems,
				fmt.Sprintf("发起换物提案失败：不能用提案换取自己的物品 id=%d", id), nil)
		}
		if p.Status != constants.ProductStatusOnSale {
			return 0, utilAppError(constants.CodeExchangeItemConflict,
				fmt.Sprintf("发起换物提案失败：对方物品 id=%d 当前状态为 %s，非在售不可换", id, p.Status), nil)
		}
		if offereeID == 0 {
			offereeID = p.SellerID
		} else if offereeID != p.SellerID {
			return 0, utilAppError(constants.CodeExchangeInvalidItems,
				fmt.Sprintf("发起换物提案失败：换入物品必须同属于一个对方用户，物品 id=%d 属于用户 %d，与此前的用户 %d 不一致", id, p.SellerID, offereeID), nil)
		}
	}
	return offereeID, nil
}

// buildExchangeItems 根据锁定后的物品构造两侧快照。
func buildExchangeItems(offerIDs, targetIDs []uint, products map[uint]*model.Product) []model.ExchangeProposalItem {
	items := make([]model.ExchangeProposalItem, 0, len(offerIDs)+len(targetIDs))
	add := func(ids []uint, side string) {
		for _, id := range ids {
			p := products[id]
			img := ""
			if p.Images != "" {
				img = strings.Split(p.Images, ",")[0]
			}
			items = append(items, model.ExchangeProposalItem{
				ProductID: p.ID,
				OwnerID:   p.SellerID,
				Side:      side,
				Title:     p.Title,
				Price:     p.Price,
				Image:     img,
			})
		}
	}
	add(offerIDs, constants.ExchangeSideOffer)
	add(targetIDs, constants.ExchangeSideTarget)
	return items
}

// canExchangeTransition 提案状态机校验（与 constants.ExchangeStatusTransitions 对应）。
func canExchangeTransition(from, to string) bool {
	for _, f := range constants.ExchangeStatusTransitions[to] {
		if f == from {
			return true
		}
	}
	return false
}

// targetStatusByAction 动作 → 目标状态。
func targetStatusByAction(action string) string {
	switch action {
	case constants.ExchangeActionAccepted:
		return constants.ExchangeStatusAccepted
	case constants.ExchangeActionRejected:
		return constants.ExchangeStatusRejected
	case constants.ExchangeActionCancelled:
		return constants.ExchangeStatusCancelled
	case constants.ExchangeActionCountered:
		return constants.ExchangeStatusCountered
	case constants.ExchangeActionExpired:
		return constants.ExchangeStatusExpired
	default:
		return ""
	}
}

// dedupPositive 去重并剔除 0，保持输入顺序。
func dedupPositive(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// overlap 两集合是否有交集。
func overlap(a, b []uint) bool {
	set := make(map[uint]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := set[x]; ok {
			return true
		}
	}
	return false
}

// genProposalNo 生成提案号：EX + yyyyMMddHHmmss + 6 位随机数。
func genProposalNo() string {
	return fmt.Sprintf("EX%s%06d", time.Now().Format("20060102150405"), rand.Intn(1000000))
}

// ToExchangeProposalVO model.ExchangeProposal → 视图对象（含物品按 side 分组与完整历史）。
// handler 与 service 列表共用同一转换方法（接口复用同一 service 方法）。
func ToExchangeProposalVO(p *model.ExchangeProposal) dto.ExchangeProposalVO {
	vo := dto.ExchangeProposalVO{
		ID:          p.ID,
		ProposalNo:  p.ProposalNo,
		OfferorID:   p.OfferorID,
		OffereeID:   p.OffereeID,
		Status:      p.Status,
		Turn:        p.Turn,
		Round:       p.Round,
		Note:        p.Note,
		TopUpAmount: p.TopUpAmount,
		TopUpPayer:  p.TopUpPayer,
		ExpiresAt:   util.FormatTime(p.ExpiresAt),
		CreatedAt:   util.FormatTime(p.CreatedAt),
		UpdatedAt:   util.FormatTime(p.UpdatedAt),
		OfferItems:  []dto.ExchangeItemVO{},
		TargetItems: []dto.ExchangeItemVO{},
		History:     []dto.ExchangeHistoryVO{},
	}
	if p.AcceptedAt != nil {
		t := util.FormatTime(*p.AcceptedAt)
		vo.AcceptedAt = &t
	}
	if p.RejectedAt != nil {
		t := util.FormatTime(*p.RejectedAt)
		vo.RejectedAt = &t
	}
	if p.CancelledAt != nil {
		t := util.FormatTime(*p.CancelledAt)
		vo.CancelledAt = &t
	}
	if p.Offeror != nil {
		u := dto.FromUser(p.Offeror)
		vo.Offeror = u
	}
	if p.Offeree != nil {
		u := dto.FromUser(p.Offeree)
		vo.Offeree = u
	}
	for i := range p.Items {
		item := &p.Items[i]
		iv := dto.ExchangeItemVO{
			ID:        item.ID,
			ProductID: item.ProductID,
			OwnerID:   item.OwnerID,
			Side:      item.Side,
			Title:     item.Title,
			Price:     item.Price,
			Image:     item.Image,
		}
		if item.Product != nil {
			prod := dto.FromProduct(item.Product, false)
			iv.Product = &prod
		}
		if item.Side == constants.ExchangeSideOffer {
			vo.OfferItems = append(vo.OfferItems, iv)
		} else {
			vo.TargetItems = append(vo.TargetItems, iv)
		}
	}
	for i := range p.History {
		h := &p.History[i]
		hv := dto.ExchangeHistoryVO{
			ID:          h.ID,
			ActorID:     h.ActorID,
			ActorRole:   h.ActorRole,
			Action:      h.Action,
			Note:        h.Note,
			TopUpAmount: h.TopUpAmount,
			TopUpPayer:  h.TopUpPayer,
			CreatedAt:   util.FormatTime(h.CreatedAt),
		}
		if h.Actor != nil {
			hv.Actor = dto.FromUser(h.Actor)
		}
		vo.History = append(vo.History, hv)
	}
	return vo
}
