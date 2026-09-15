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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, constants.ExchangeActionAccepted)
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
		now := time.Now()
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, action)
		if err != nil {
			return err
		}
		now := time.Now()
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
	err := s.db.Transaction(func(tx *gorm.DB) error {
		p, actorRole, err := s.loadActiveForAction(tx, userID, proposalID, constants.ExchangeActionCountered)
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

// ExpireDue 超时扫描：原子结束已到期的生效提案并释放物品占用（由后台 ticker 周期调用）。
func (s *ExchangeService) ExpireDue() (int64, error) {
	var n int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		closed, err := s.exchangeRepo.ExpireDueTx(tx, 200)
		if err != nil {
			return err
		}
		n = closed
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("expire due exchange proposals: %w", err)
	}
	if n > 0 {
		s.logger.Info(constants.LogExchangeExpired, "proposal_no", "-", "operator", 0, "released_items", n, "status", constants.ExchangeStatusExpired)
	}
	return n, nil
}

// loadActiveForAction 读取提案并校验：存在 → 参与方 → 未超时 → 动作权限（回合）→ 状态可流转。
// 返回加锁读取的提案与操作者角色（offeror/offeree）。
func (s *ExchangeService) loadActiveForAction(tx *gorm.DB, userID, proposalID uint, action string) (*model.ExchangeProposal, string, error) {
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
	// 惰性超时：到达动作时若已过期，先原子置为 expired 再拒绝本次操作。
	if constants.IsExchangeActive(p.Status) && time.Now().After(p.ExpiresAt) {
		ok, casErr := s.exchangeRepo.CompareAndUpdateStatusTx(tx, p.ID, constants.ExchangeActiveStatuses,
			map[string]interface{}{"status": constants.ExchangeStatusExpired, "last_action_by": 0})
		if casErr != nil {
			return nil, "", casErr
		}
		if ok {
			if histErr := s.exchangeRepo.CreateHistoryWithTx(tx, &model.ExchangeProposalHistory{
				ProposalID: p.ID, ActorID: 0, ActorRole: constants.ExchangePartySystem,
				Action: constants.ExchangeActionExpired, Note: "提案超过回应期限未处理，已自动失效并释放物品",
			}); histErr != nil {
				return nil, "", histErr
			}
		}
		return nil, "", utilAppError(constants.CodeExchangeExpired,
			"换物提案操作失败：提案 "+p.ProposalNo+" 已超过回应期限，自动失效", nil)
	}
	// 还价次数校验优先于状态/回合校验：countered 后再次还价明确提示“仅可还价一次”。
	if action == constants.ExchangeActionCountered && p.Round >= 1 {
		return nil, "", utilAppError(constants.CodeExchangeRoundExhausted,
			"还价失败：提案 "+p.ProposalNo+" 已还价过一次，无法再次还价", nil)
	}
	if !canExchangeTransition(p.Status, targetStatusByAction(action)) {
		// 已超时终态单独给出明确错误，其余为状态冲突。
		if p.Status == constants.ExchangeStatusExpired {
			return nil, "", utilAppError(constants.CodeExchangeExpired,
				"换物提案操作失败：提案 "+p.ProposalNo+" 已超过回应期限，自动失效", nil)
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
