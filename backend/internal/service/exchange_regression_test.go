package service

// 换物提案状态收口与并发行为回归测试（数据隔离）。
//
// 约定：
//   - 每个测试/子场景都通过 newRegEnv 重建一份全新的内存库与数据（唯一 DSN），互不污染、可重复运行；
//   - 断言同时基于 service 接口返回与数据库回读（regEnv.reload / 计数），不仅依赖内存对象；
//   - 每个关键步骤带“阶段(stage)”说明，失败信息能直接定位在哪个阶段偏离预期；
//   - 不新增任何功能，仅锁定现有的权限与状态规则。
//
// 说明：内存 SQLite 不支持 PostgreSQL 那样的行级锁（FOR UPDATE 被忽略），其多连接写并发会
// 返回驱动层 “database is deadlocked/locked”，无法据此稳定判定业务结果。因此：
//   - 并发接受用例（concurrent=true）把连接池收敛为 1，让两个“同时发起”的接受在 DB 层确定性串行；
//   - “恰好一个成功”的真正保证是仓储层 CAS（WHERE status IN ... 条件更新），与具体数据库无关，
//     PostgreSQL 下由行锁 + 同一 CAS 得到相同结果。
import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/dto"
	"github.com/marketpal/marketpal/internal/model"
	"github.com/marketpal/marketpal/internal/repository"
	"github.com/marketpal/marketpal/internal/util"
	"gorm.io/gorm"
)

var regDBCounter uint64

// regEnv 单个回归场景的隔离环境。
type regEnv struct {
	t          *testing.T
	db         *gorm.DB
	svc        *ExchangeService
	exRepo     repository.ExchangeRepository
	prodRepo   repository.ProductRepository
	aliceID    uint // 发起人
	bobID      uint // 接收人
	carolID    uint // 无关第三方
	offerIDs   []uint
	targetIDs  []uint
	carolOffer []uint
}

// newRegEnv 重建一份全新的、与其他用例完全隔离的数据：
// 3 个用户、Alice 2 件在售、Bob 2 件在售、Carol 1 件在售。
// concurrent=true 时启用多连接与忙等待，供并发用例使用。
func newRegEnv(t *testing.T, concurrent bool) *regEnv {
	t.Helper()
	n := atomic.AddUint64(&regDBCounter, 1)
	dsn := fmt.Sprintf("file:reg_%d_%s?mode=memory&cache=shared&_busy_timeout=5000", n, t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("[setup] 打开隔离内存库失败: %v", err)
	}
	if concurrent {
		// 单连接：两个并发接受在 DB 层确定性串行，避免 SQLite 多连接写锁/死锁噪声；
		// 业务不变量仍由 CAS 保证（见文件头说明）。
		sqlDB, _ := db.DB()
		sqlDB.SetMaxOpenConns(1)
	}
	models := []interface{}{
		&model.User{}, &model.Product{}, &model.Favorite{}, &model.Address{},
		&model.CartItem{}, &model.Order{}, &model.Message{}, &model.Review{}, &model.AuditLog{},
		&model.ExchangeProposal{}, &model.ExchangeProposalItem{}, &model.ExchangeProposalHistory{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("[setup] 迁移失败: %v", err)
	}

	env := &regEnv{t: t, db: db, prodRepo: repository.NewProductRepository(db)}
	env.exRepo = repository.NewExchangeRepository(db)
	env.svc = NewExchangeService(db, env.exRepo, env.prodRepo, testLogger())

	mkUser := func(name string) uint {
		u := &model.User{Username: name, PasswordHash: "h", Nickname: name, Role: "user", Status: "active"}
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("[setup] 创建用户 %s 失败: %v", name, err)
		}
		return u.ID
	}
	env.aliceID = mkUser("reg_alice")
	env.bobID = mkUser("reg_bob")
	env.carolID = mkUser("reg_carol")

	mkProduct := func(seller uint, title string, price float64) uint {
		p := &model.Product{
			SellerID: seller, Title: title, Description: title,
			OriginalPrice: price, Price: price,
			Condition: "almost_new", Category: "digital", Status: constants.ProductStatusOnSale,
		}
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("[setup] 创建物品 %s 失败: %v", title, err)
		}
		return p.ID
	}
	env.offerIDs = []uint{mkProduct(env.aliceID, "reg_alice_camera", 2000), mkProduct(env.aliceID, "reg_alice_keyboard", 200)}
	env.targetIDs = []uint{mkProduct(env.bobID, "reg_bob_tablet", 1800), mkProduct(env.bobID, "reg_bob_earphone", 500)}
	env.carolOffer = []uint{mkProduct(env.carolID, "reg_carol_watch", 1500)}
	return env
}

// createReq 构造标准发起请求（Alice 换 Bob）。
func (e *regEnv) createReq() dto.ExchangeCreateRequest {
	return dto.ExchangeCreateRequest{
		OfferProductIDs:  append([]uint{}, e.offerIDs...),
		TargetProductIDs: append([]uint{}, e.targetIDs...),
		Note:             "reg 换物说明",
		TopUpAmount:      100,
		TopUpPayer:       constants.ExchangePartyOfferor,
		TTLHours:         72,
	}
}

// mustCreate 由指定用户发起一个提案并断言成功（stage 标识调用阶段）。
func (e *regEnv) mustCreate(stage string, actor uint, req dto.ExchangeCreateRequest) *model.ExchangeProposal {
	e.t.Helper()
	p, err := e.svc.Create(actor, req)
	if err != nil {
		e.t.Fatalf("[%s] 发起提案应成功，实际失败: %v", stage, err)
	}
	return p
}

// reload 从数据库回读提案（含物品与历史）。
func (e *regEnv) reload(id uint) *model.ExchangeProposal {
	e.t.Helper()
	var p model.ExchangeProposal
	if err := e.db.
		Preload("Items").Preload("History").
		First(&p, id).Error; err != nil {
		e.t.Fatalf("[reload] 回读提案 %d 失败: %v", id, err)
	}
	return &p
}

func (e *regEnv) assertStatus(stage string, id uint, want string) {
	e.t.Helper()
	got := e.reload(id).Status
	if got != want {
		e.t.Fatalf("[%s] 提案状态应为 %s，回读为 %s", stage, want, got)
	}
}

func (e *regEnv) countActions(id uint, action string) int64 {
	e.t.Helper()
	var n int64
	if err := e.db.Model(&model.ExchangeProposalHistory{}).
		Where("proposal_id = ? AND action = ?", id, action).Count(&n).Error; err != nil {
		e.t.Fatalf("[countActions] 统计历史失败: %v", err)
	}
	return n
}

func (e *regEnv) assertProductStatus(stage string, pid uint, want string) {
	e.t.Helper()
	var s string
	if err := e.db.Model(&model.Product{}).Where("id = ?", pid).
		Pluck("status", &s).Error; err != nil {
		e.t.Fatalf("[%s] 回读物品 %d 状态失败: %v", stage, pid, err)
	}
	if s != want {
		e.t.Fatalf("[%s] 物品 %d 状态应为 %s，回读为 %s", stage, pid, want, s)
	}
}

func (e *regEnv) allProductStatusesOnSale(stage string, ids []uint) {
	e.t.Helper()
	for _, pid := range ids {
		e.assertProductStatus(stage, pid, constants.ProductStatusOnSale)
	}
}

// backdateExpiry 直接把提案到期时间回溯到过去（绕过等待）。
func (e *regEnv) backdateExpiry(id uint) {
	e.t.Helper()
	if err := e.db.Model(&model.ExchangeProposal{}).Where("id = ?", id).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		e.t.Fatalf("[backdate] 回溯到期时间失败: %v", err)
	}
}

func codeOf(err error) (int, bool) {
	var appErr *util.AppError
	if errors.As(err, &appErr) {
		return appErr.Code, true
	}
	return 0, false
}

func (e *regEnv) assertCode(stage string, err error, wantCode int) {
	e.t.Helper()
	code, ok := codeOf(err)
	if !ok {
		e.t.Fatalf("[%s] 应返回业务错误码 %d，实际为非业务错误: %v", stage, wantCode, err)
	}
	if code != wantCode {
		e.t.Fatalf("[%s] 应返回错误码 %d，实际 %d（%v）", stage, wantCode, code, err)
	}
}

// ---------------------------------------------------------------------------
// 1) 发起
// ---------------------------------------------------------------------------

func TestRegCreate(t *testing.T) {
	t.Run("正常发起落库且可回读", func(t *testing.T) {
		env := newRegEnv(t, false)
		const stage = "发起-成功"
		p := env.mustCreate(stage, env.aliceID, env.createReq())

		// 接口与回读双重断言。
		if p.Status != constants.ExchangeStatusPending || p.Turn != constants.ExchangePartyOfferee || p.Round != 0 {
			t.Fatalf("[%s] 初始状态字段异常: status=%s turn=%s round=%d", stage, p.Status, p.Turn, p.Round)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusPending)

		got := env.reload(p.ID)
		if len(got.Items) != len(env.offerIDs)+len(env.targetIDs) {
			t.Fatalf("[%s] 物品快照数应为 %d，回读 %d", stage, len(env.offerIDs)+len(env.targetIDs), len(got.Items))
		}
		var offer, target int
		for _, it := range got.Items {
			switch it.Side {
			case constants.ExchangeSideOffer:
				offer++
			case constants.ExchangeSideTarget:
				target++
			}
		}
		if offer != len(env.offerIDs) || target != len(env.targetIDs) {
			t.Fatalf("[%s] 物品分组数异常 offer=%d target=%d", stage, offer, target)
		}
		if len(got.History) != 1 || got.History[0].Action != constants.ExchangeActionCreated {
			t.Fatalf("[%s] 应有且仅有 1 条 created 历史，回读 %+v", stage, got.History)
		}
		// 发起不锁定物品：全部保持在售。
		env.allProductStatusesOnSale(stage, append(append([]uint{}, env.offerIDs...), env.targetIDs...))
	})

	t.Run("非物品所有者不能发起", func(t *testing.T) {
		env := newRegEnv(t, false)
		req := env.createReq()
		// Carol 用 Alice 的物品作为换出 → 403。
		req.OfferProductIDs = append([]uint{}, env.offerIDs...)
		_, err := env.svc.Create(env.carolID, req)
		env.assertCode("发起-非所有者", err, constants.CodeForbidden)
	})

	t.Run("不能换自己的物品", func(t *testing.T) {
		env := newRegEnv(t, false)
		req := dto.ExchangeCreateRequest{OfferProductIDs: []uint{env.offerIDs[0]}, TargetProductIDs: []uint{env.offerIDs[1]}}
		_, err := env.svc.Create(env.aliceID, req)
		env.assertCode("发起-换自己", err, constants.CodeExchangeInvalidItems)
	})

	t.Run("同一物品同时只能有一个生效提案", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("发起-首个提案", env.aliceID, env.createReq())

		// Carol 也想换 Bob 的平板（target 重叠）→ 占用冲突。
		_, err := env.svc.Create(env.carolID, dto.ExchangeCreateRequest{
			OfferProductIDs:  append([]uint{}, env.carolOffer...),
			TargetProductIDs: []uint{env.targetIDs[0]},
		})
		env.assertCode("发起-第三方占用冲突", err, constants.CodeExchangeItemConflict)

		// Alice 复用自己已占用的相机再发一个 → 占用冲突。
		_, err = env.svc.Create(env.aliceID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{env.offerIDs[0]}, TargetProductIDs: []uint{env.targetIDs[1]},
		})
		env.assertCode("发起-自己重复占用", err, constants.CodeExchangeItemConflict)

		// 原提案仍待回应、物品仍在售（仅占用，未售出）。
		env.assertStatus("发起-冲突后原提案不变", p.ID, constants.ExchangeStatusPending)
		env.allProductStatusesOnSale("发起-冲突后物品仍在售", env.offerIDs)
	})
}

// ---------------------------------------------------------------------------
// 2) 还价（仅一次）
// ---------------------------------------------------------------------------

func TestRegCounter(t *testing.T) {
	t.Run("接收人还价后动作权回到发起人", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("还价-前置", env.aliceID, env.createReq())
		const stage = "还价-成功"
		cp, err := env.svc.Counter(env.bobID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 50, Note: "少点"})
		if err != nil {
			t.Fatalf("[%s] 还价应成功: %v", stage, err)
		}
		if cp.Status != constants.ExchangeStatusCountered || cp.Turn != constants.ExchangePartyOfferor || cp.Round != 1 {
			t.Fatalf("[%s] 还价后字段异常 status=%s turn=%s round=%d", stage, cp.Status, cp.Turn, cp.Round)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusCountered)
		got := env.reload(p.ID)
		if got.TopUpAmount != 50 {
			t.Fatalf("[%s] 还价后补差价应回读为 50，实际 %v", stage, got.TopUpAmount)
		}
		if env.countActions(p.ID, constants.ExchangeActionCountered) != 1 {
			t.Fatalf("[%s] 应有 1 条 countered 历史", stage)
		}
		env.allProductStatusesOnSale(stage, append(append([]uint{}, env.offerIDs...), env.targetIDs...))
	})

	t.Run("不能还价两次", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("二次还价-前置", env.aliceID, env.createReq())
		if _, err := env.svc.Counter(env.bobID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 50}); err != nil {
			t.Fatalf("[还价-首次] 应成功: %v", err)
		}
		// 轮到发起人后，任何一方再还价都被拒绝（明确“仅可还价一次”）。
		if _, err := env.svc.Counter(env.aliceID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 10}); err == nil {
			t.Fatal("[还价-发起人二次] 不应成功")
		} else {
			env.assertCode("还价-发起人二次", err, constants.CodeExchangeRoundExhausted)
		}
		if _, err := env.svc.Counter(env.bobID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 10}); err == nil {
			t.Fatal("[还价-接收人二次] 不应成功")
		} else {
			env.assertCode("还价-接收人二次", err, constants.CodeExchangeRoundExhausted)
		}
	})

	t.Run("非回合方不能还价", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("还价回合-前置", env.aliceID, env.createReq())
		// pending 阶段回合在 Bob，Alice 不能还价。
		_, err := env.svc.Counter(env.aliceID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 1})
		env.assertCode("还价-非回合方", err, constants.CodeExchangeTurnInvalid)
		// 无关第三方不能还价。
		_, err = env.svc.Counter(env.carolID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 1})
		env.assertCode("还价-非参与方", err, constants.CodeNotExchangeParty)
	})
}

// ---------------------------------------------------------------------------
// 3) 接受（含并发接受、接受锁物、自动结束其他提案）
// ---------------------------------------------------------------------------

func TestRegAccept(t *testing.T) {
	t.Run("初始提案被接收人接受即成交锁定", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("接受-前置", env.aliceID, env.createReq())
		const stage = "接受-pending"
		ap, err := env.svc.Accept(env.bobID, p.ID)
		if err != nil {
			t.Fatalf("[%s] 接受应成功: %v", stage, err)
		}
		if ap.Status != constants.ExchangeStatusAccepted || ap.AcceptedAt == nil {
			t.Fatalf("[%s] 接受后状态/时间异常 status=%s acceptedAt=%v", stage, ap.Status, ap.AcceptedAt)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusAccepted)
		// 涉及物品全部锁定为已售。
		for _, pid := range append(append([]uint{}, env.offerIDs...), env.targetIDs...) {
			env.assertProductStatus(stage, pid, constants.ProductStatusSold)
		}
		if env.countActions(p.ID, constants.ExchangeActionAccepted) != 1 {
			t.Fatalf("[%s] 应有 1 条 accepted 历史", stage)
		}
		// 成交后不能再接受/拒绝/取消/还价。
		if _, err := env.svc.Accept(env.bobID, p.ID); err == nil {
			t.Fatalf("[%s] 重复接受不应成功", stage)
		}
	})

	t.Run("还价方案被发起人接受即成交锁定", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("接受还价-前置1", env.aliceID, env.createReq())
		if _, err := env.svc.Counter(env.bobID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 30}); err != nil {
			t.Fatalf("[接受还价-前置2] %v", err)
		}
		const stage = "接受-countered"
		if _, err := env.svc.Accept(env.aliceID, p.ID); err != nil {
			t.Fatalf("[%s] 发起人接受还价应成功: %v", stage, err)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusAccepted)
		for _, pid := range append(append([]uint{}, env.offerIDs...), env.targetIDs...) {
			env.assertProductStatus(stage, pid, constants.ProductStatusSold)
		}
	})

	t.Run("接受自动结束冲突的其他生效提案", func(t *testing.T) {
		env := newRegEnv(t, false)
		// 提案 A：Alice 相机/键盘 换 Bob 平板/耳机（公开接口，占用全部涉及物品）。
		a := env.mustCreate("自动结束-提案A", env.aliceID, env.createReq())

		// 提案 B 与 A 共享 Bob 的平板。由于公开发起接口会拦截“同一物品只能有一个生效提案”，
		// 这里直接经仓储/DB 构造一个并发遗留的重叠提案，用于确定性验证接受时的防御性自动结束。
		b := &model.ExchangeProposal{
			ProposalNo:   "REG-CONFLICT-B",
			OfferorID:    env.carolID,
			OffereeID:    env.bobID,
			Status:       constants.ExchangeStatusPending,
			Turn:         constants.ExchangePartyOfferee,
			Round:        0,
			Note:         "carol watch for tablet",
			TopUpAmount:  0,
			TopUpPayer:   constants.ExchangePartyOfferor,
			ExpiresAt:    time.Now().Add(48 * time.Hour),
			LastActionBy: env.carolID,
		}
		bItems := []model.ExchangeProposalItem{
			{ProductID: env.carolOffer[0], OwnerID: env.carolID, Side: constants.ExchangeSideOffer, Title: "reg_carol_watch", Price: 1500},
			{ProductID: env.targetIDs[0], OwnerID: env.bobID, Side: constants.ExchangeSideTarget, Title: "reg_bob_tablet", Price: 1800},
		}
		if err := env.db.Transaction(func(tx *gorm.DB) error {
			return env.exRepo.CreateWithTx(tx, b, bItems)
		}); err != nil {
			t.Fatalf("[自动结束-提案B] 构造重叠提案失败: %v", err)
		}

		// Bob 接受 A：A 成交，平板售出，B 被原子结束为 cancelled 并补系统历史。
		const stage = "自动结束-接受A"
		if _, err := env.svc.Accept(env.bobID, a.ID); err != nil {
			t.Fatalf("[%s] 接受 A 应成功: %v", stage, err)
		}
		env.assertStatus(stage, a.ID, constants.ExchangeStatusAccepted)
		env.assertStatus(stage, b.ID, constants.ExchangeStatusCancelled)
		var sysCancel int64
		env.db.Model(&model.ExchangeProposalHistory{}).
			Where("proposal_id = ? AND actor_role = ? AND action = ?",
				b.ID, constants.ExchangePartySystem, constants.ExchangeActionCancelled).Count(&sysCancel)
		if sysCancel != 1 {
			t.Fatalf("[%s] 被结束提案 B 应有 1 条系统取消历史，实际 %d", stage, sysCancel)
		}
		// Carol 的手表未参与成交，应释放回在售；B 的原始 created 历史仍保留。
		env.assertProductStatus(stage, env.carolOffer[0], constants.ProductStatusOnSale)
		if c := env.countActions(b.ID, constants.ExchangeActionCreated); c != 1 {
			t.Fatalf("[%s] 被结束提案 B 的 created 历史应保留 1 条，实际 %d", stage, c)
		}
	})

	t.Run("非回合方与非参与方不能接受", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("接受权限-前置", env.aliceID, env.createReq())
		if _, err := env.svc.Accept(env.aliceID, p.ID); err == nil { // pending 回合在 Bob
			t.Fatal("[接受权限-发起人抢先] 不应成功")
		} else {
			env.assertCode("接受权限-发起人抢先", err, constants.CodeExchangeTurnInvalid)
		}
		_, err := env.svc.Accept(env.carolID, p.ID)
		env.assertCode("接受权限-第三方", err, constants.CodeNotExchangeParty)
	})
}

// TestRegConcurrentAccept 并发接受：恰好一个成功、另一个明确失败、物品只售出一次。
func TestRegConcurrentAccept(t *testing.T) {
	env := newRegEnv(t, true)
	p := env.mustCreate("并发接受-前置", env.aliceID, env.createReq())

	const stage = "并发接受"
	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 同时释放，制造对同一提案的并发接受
			_, results[idx] = env.svc.Accept(env.bobID, p.ID)
		}(i)
	}
	close(start)
	wg.Wait()

	var success, businessFail int
	for _, err := range results {
		switch {
		case err == nil:
			success++
		default:
			if _, ok := codeOf(err); ok {
				businessFail++
			}
		}
	}
	if success != 1 || businessFail != 1 {
		t.Fatalf("[%s] 应恰好 1 个成功、1 个业务失败，实际 success=%d businessFail=%d（results=%v）",
			stage, success, businessFail, results)
	}
	env.assertStatus(stage, p.ID, constants.ExchangeStatusAccepted)
	if env.countActions(p.ID, constants.ExchangeActionAccepted) != 1 {
		t.Fatalf("[%s] accepted 历史必须恰好 1 条", stage)
	}
	for _, pid := range append(append([]uint{}, env.offerIDs...), env.targetIDs...) {
		env.assertProductStatus(stage, pid, constants.ProductStatusSold)
	}
}

// ---------------------------------------------------------------------------
// 4) 拒绝
// ---------------------------------------------------------------------------

func TestRegReject(t *testing.T) {
	t.Run("接收人拒绝后释放物品", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("拒绝-前置", env.aliceID, env.createReq())
		const stage = "拒绝-成功"
		rp, err := env.svc.Reject(env.bobID, p.ID, "不换了")
		if err != nil {
			t.Fatalf("[%s] 拒绝应成功: %v", stage, err)
		}
		if rp.Status != constants.ExchangeStatusRejected || rp.RejectedAt == nil {
			t.Fatalf("[%s] 拒绝后状态/时间异常", stage)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusRejected)
		if env.countActions(p.ID, constants.ExchangeActionRejected) != 1 {
			t.Fatalf("[%s] 应有 1 条 rejected 历史", stage)
		}
		env.allProductStatusesOnSale(stage, append(append([]uint{}, env.offerIDs...), env.targetIDs...))

		// 释放后 Carol 可就同一 target 发起提案。
		env.mustCreate("拒绝-释放后重新发起", env.carolID, dto.ExchangeCreateRequest{
			OfferProductIDs:  append([]uint{}, env.carolOffer...),
			TargetProductIDs: []uint{env.targetIDs[0]},
		})
	})

	t.Run("发起人不能拒绝、第三方不能拒绝、终态不能再拒绝", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("拒绝权限-前置", env.aliceID, env.createReq())
		_, err := env.svc.Reject(env.aliceID, p.ID, "")
		env.assertCode("拒绝-发起人", err, constants.CodeExchangeTurnInvalid)
		_, err = env.svc.Reject(env.carolID, p.ID, "")
		env.assertCode("拒绝-第三方", err, constants.CodeNotExchangeParty)

		if _, err := env.svc.Reject(env.bobID, p.ID, ""); err != nil {
			t.Fatalf("[拒绝-首次] 应成功: %v", err)
		}
		_, err = env.svc.Reject(env.bobID, p.ID, "")
		env.assertCode("拒绝-终态重复", err, constants.CodeExchangeStateInvalid)
	})
}

// ---------------------------------------------------------------------------
// 5) 取消
// ---------------------------------------------------------------------------

func TestRegCancel(t *testing.T) {
	t.Run("发起人取消后释放物品", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("取消-前置", env.aliceID, env.createReq())
		const stage = "取消-成功"
		cp, err := env.svc.Cancel(env.aliceID, p.ID, "先不换")
		if err != nil {
			t.Fatalf("[%s] 取消应成功: %v", stage, err)
		}
		if cp.Status != constants.ExchangeStatusCancelled || cp.CancelledAt == nil {
			t.Fatalf("[%s] 取消后状态/时间异常", stage)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusCancelled)
		if env.countActions(p.ID, constants.ExchangeActionCancelled) != 1 {
			t.Fatalf("[%s] 应有 1 条 cancelled 历史", stage)
		}
		env.allProductStatusesOnSale(stage, append(append([]uint{}, env.offerIDs...), env.targetIDs...))

		// 释放后 Alice 可用同一批物品重新发起。
		env.mustCreate("取消-释放后重新发起", env.aliceID, env.createReq())
	})

	t.Run("接收人不能取消、第三方不能取消、还价态发起人仍可取消", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("取消权限-前置", env.aliceID, env.createReq())
		_, err := env.svc.Cancel(env.bobID, p.ID, "")
		env.assertCode("取消-接收人", err, constants.CodeNotExchangeParty)
		_, err = env.svc.Cancel(env.carolID, p.ID, "")
		env.assertCode("取消-第三方", err, constants.CodeNotExchangeParty)

		if _, err := env.svc.Counter(env.bobID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 40}); err != nil {
			t.Fatalf("[取消权限-还价] %v", err)
		}
		// countered 回合回到发起人，发起人仍可主动取消并释放。
		if _, err := env.svc.Cancel(env.aliceID, p.ID, ""); err != nil {
			t.Fatalf("[取消-还价态发起人] 应成功: %v", err)
		}
		env.assertStatus("取消-还价态", p.ID, constants.ExchangeStatusCancelled)
		env.assertProductStatus("取消-还价态释放", env.targetIDs[0], constants.ProductStatusOnSale)
	})
}

// ---------------------------------------------------------------------------
// 6) 到期后的任何动作（不依赖后台清理）
// ---------------------------------------------------------------------------

func TestRegExpiredActions(t *testing.T) {
	cases := []struct {
		name string
		do   func(env *regEnv, id uint) error
	}{
		{"到期-接受", func(env *regEnv, id uint) error { _, e := env.svc.Accept(env.bobID, id); return e }},
		{"到期-拒绝", func(env *regEnv, id uint) error { _, e := env.svc.Reject(env.bobID, id, ""); return e }},
		{"到期-取消", func(env *regEnv, id uint) error { _, e := env.svc.Cancel(env.aliceID, id, ""); return e }},
		{"到期-还价", func(env *regEnv, id uint) error {
			_, e := env.svc.Counter(env.bobID, id, dto.ExchangeCounterRequest{TopUpAmount: 1})
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newRegEnv(t, false)
			p := env.mustCreate(tc.name+"-前置", env.aliceID, env.createReq())
			env.backdateExpiry(p.ID)

			err := tc.do(env, p.ID)
			// 阶段 A：动作必须明确失败（10025），不能是 500/空错误。
			env.assertCode(tc.name+"-动作明确失败", err, constants.CodeExchangeExpired)

			// 阶段 B：提案在数据库中转 expired。
			env.assertStatus(tc.name+"-状态收口", p.ID, constants.ExchangeStatusExpired)

			// 阶段 C：恰好 1 条系统超时历史，created 历史仍在。
			if n := env.countActions(p.ID, constants.ExchangeActionExpired); n != 1 {
				t.Fatalf("[%s] 超时历史应为 1 条，实际 %d", tc.name, n)
			}
			got := env.reload(p.ID)
			if len(got.History) != 2 {
				t.Fatalf("[%s] 历史应为 created+expired 共 2 条，实际 %d", tc.name, len(got.History))
			}

			// 阶段 D：关联物品立即释放回在售，并可重新发起。
			env.allProductStatusesOnSale(tc.name+"-物品释放", append(append([]uint{}, env.offerIDs...), env.targetIDs...))
			env.mustCreate(tc.name+"-释放后重新发起", env.carolID, dto.ExchangeCreateRequest{
				OfferProductIDs:  append([]uint{}, env.carolOffer...),
				TargetProductIDs: []uint{env.targetIDs[0]},
			})
		})
	}
}

// ---------------------------------------------------------------------------
// 7) 后台到期清理
// ---------------------------------------------------------------------------

func TestRegBackgroundSweep(t *testing.T) {
	t.Run("后台清理到期提案并释放", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("后台清理-前置", env.aliceID, env.createReq())
		env.backdateExpiry(p.ID)

		const stage = "后台清理"
		n, err := env.svc.ExpireDue()
		if err != nil {
			t.Fatalf("[%s] 清理应成功: %v", stage, err)
		}
		if n != 1 {
			t.Fatalf("[%s] 应清理 1 个，实际 %d", stage, n)
		}
		env.assertStatus(stage, p.ID, constants.ExchangeStatusExpired)
		if env.countActions(p.ID, constants.ExchangeActionExpired) != 1 {
			t.Fatalf("[%s] 应有 1 条超时历史", stage)
		}
		env.allProductStatusesOnSale(stage, append(append([]uint{}, env.offerIDs...), env.targetIDs...))

		// 清理后动作仍明确失败。
		_, err = env.svc.Accept(env.bobID, p.ID)
		env.assertCode(stage+"-清理后接受失败", err, constants.CodeExchangeExpired)
	})

	t.Run("未到期提案不被清理", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("未到期-前置", env.aliceID, env.createReq()) // 有效期 72h
		n, err := env.svc.ExpireDue()
		if err != nil {
			t.Fatalf("[未到期] 清理失败: %v", err)
		}
		if n != 0 {
			t.Fatalf("[未到期] 不应清理，实际清理 %d", n)
		}
		env.assertStatus("未到期-仍待回应", p.ID, constants.ExchangeStatusPending)
	})
}

// ---------------------------------------------------------------------------
// 8) 重复清理幂等 + 动作与后台清理共用收口
// ---------------------------------------------------------------------------

func TestRegSweepIdempotent(t *testing.T) {
	env := newRegEnv(t, false)
	p := env.mustCreate("重复清理-前置", env.aliceID, env.createReq())
	env.backdateExpiry(p.ID)

	for i := 0; i < 3; i++ {
		n, err := env.svc.ExpireDue()
		if err != nil {
			t.Fatalf("[重复清理-第%d轮] 失败: %v", i+1, err)
		}
		if i == 0 && n != 1 {
			t.Fatalf("[重复清理-第1轮] 应收口 1 个，实际 %d", n)
		}
		if i > 0 && n != 0 {
			t.Fatalf("[重复清理-第%d轮] 不应重复收口，实际 %d", i+1, n)
		}
	}
	env.assertStatus("重复清理-状态", p.ID, constants.ExchangeStatusExpired)
	if n := env.countActions(p.ID, constants.ExchangeActionExpired); n != 1 {
		t.Fatalf("[重复清理-历史] 超时历史必须恰好 1 条，实际 %d", n)
	}
}

func TestRegActionAndSweepShareSettlement(t *testing.T) {
	t.Run("动作先收口后后台清理不重复写历史", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("动作先收口-前置", env.aliceID, env.createReq())
		env.backdateExpiry(p.ID)

		_, err := env.svc.Accept(env.bobID, p.ID)
		env.assertCode("动作先收口-接受失败", err, constants.CodeExchangeExpired)
		env.assertStatus("动作先收口-状态", p.ID, constants.ExchangeStatusExpired)

		n, err := env.svc.ExpireDue()
		if err != nil {
			t.Fatalf("[动作先收口-清理] %v", err)
		}
		if n != 0 {
			t.Fatalf("[动作先收口-清理] 不应重复收口，实际 %d", n)
		}
		if c := env.countActions(p.ID, constants.ExchangeActionExpired); c != 1 {
			t.Fatalf("[动作先收口-历史] 超时历史必须 1 条，实际 %d", c)
		}
	})

	t.Run("后台先清理后动作明确失败", func(t *testing.T) {
		env := newRegEnv(t, false)
		p := env.mustCreate("后台先收口-前置", env.aliceID, env.createReq())
		env.backdateExpiry(p.ID)

		if _, err := env.svc.ExpireDue(); err != nil {
			t.Fatalf("[后台先收口-清理] %v", err)
		}
		_, err := env.svc.Accept(env.bobID, p.ID)
		env.assertCode("后台先收口-接受失败", err, constants.CodeExchangeExpired)
		env.assertStatus("后台先收口-状态", p.ID, constants.ExchangeStatusExpired)
	})
}
