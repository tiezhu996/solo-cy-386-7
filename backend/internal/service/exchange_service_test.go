package service

import (
	"errors"
	"io"
	"log/slog"
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

// newExchangeTestDB 构建独立内存库并迁移全部模型（SQLite 会忽略 FOR UPDATE，仅用于业务逻辑验证）。
func newExchangeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "_svc?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	models := []interface{}{
		&model.User{}, &model.Product{}, &model.Favorite{}, &model.Address{},
		&model.CartItem{}, &model.Order{}, &model.Message{}, &model.Review{}, &model.AuditLog{},
		&model.ExchangeProposal{}, &model.ExchangeProposalItem{}, &model.ExchangeProposalHistory{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type exchangeFixture struct {
	db                *gorm.DB
	svc               *ExchangeService
	productRepo       repository.ProductRepository
	alice, bob, carol model.User
	// Alice 的在售物品 / Bob 的在售物品 / Carol 的在售物品
	aliceP1, aliceP2 model.Product
	bobP3, bobP4     model.Product
	carolP5          model.Product
}

func setupExchangeFixture(t *testing.T) *exchangeFixture {
	t.Helper()
	db := newExchangeTestDB(t)
	fx := &exchangeFixture{db: db}
	fx.productRepo = repository.NewProductRepository(db)
	exchangeRepo := repository.NewExchangeRepository(db)
	fx.svc = NewExchangeService(db, exchangeRepo, fx.productRepo, testLogger())

	users := []*model.User{
		{Username: "alice", PasswordHash: "h", Nickname: "Alice", Role: "user", Status: "active"},
		{Username: "bob", PasswordHash: "h", Nickname: "Bob", Role: "user", Status: "active"},
		{Username: "carol", PasswordHash: "h", Nickname: "Carol", Role: "user", Status: "active"},
	}
	for _, u := range users {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	fx.alice, fx.bob, fx.carol = *users[0], *users[1], *users[2]

	products := []*model.Product{
		{SellerID: fx.alice.ID, Title: "Alice相机", Description: "p1", OriginalPrice: 3000, Price: 2000, Condition: "almost_new", Category: "digital", Status: "on_sale"},
		{SellerID: fx.alice.ID, Title: "Alice键盘", Description: "p2", OriginalPrice: 400, Price: 200, Condition: "brand_new", Category: "digital", Status: "on_sale"},
		{SellerID: fx.bob.ID, Title: "Bob平板", Description: "p3", OriginalPrice: 2500, Price: 1800, Condition: "lightly_used", Category: "digital", Status: "on_sale"},
		{SellerID: fx.bob.ID, Title: "Bob耳机", Description: "p4", OriginalPrice: 800, Price: 500, Condition: "almost_new", Category: "digital", Status: "on_sale"},
		{SellerID: fx.carol.ID, Title: "Carol手表", Description: "p5", OriginalPrice: 2000, Price: 1500, Condition: "brand_new", Category: "other", Status: "on_sale"},
	}
	for _, p := range products {
		if err := db.Create(p).Error; err != nil {
			t.Fatalf("create product: %v", err)
		}
	}
	fx.aliceP1, fx.aliceP2 = *products[0], *products[1]
	fx.bobP3, fx.bobP4 = *products[2], *products[3]
	fx.carolP5 = *products[4]
	return fx
}

func assertAppErrorCode(t *testing.T, err error, wantCode int) {
	t.Helper()
	var appErr *util.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError code %d, got %v", wantCode, err)
	}
	if appErr.Code != wantCode {
		t.Fatalf("expected error code %d, got %d (message=%s)", wantCode, appErr.Code, appErr.Message)
	}
}

// TestExchangeCreateValidations 发起提案的归属/在售/占用校验。
func TestExchangeCreateValidations(t *testing.T) {
	fx := setupExchangeFixture(t)

	t.Run("只能用自己的物品发起", func(t *testing.T) {
		_, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.bobP3.ID}, TargetProductIDs: []uint{fx.bobP4.ID},
		})
		assertAppErrorCode(t, err, constants.CodeForbidden)
	})

	t.Run("不能换自己的物品", func(t *testing.T) {
		_, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.aliceP2.ID},
		})
		assertAppErrorCode(t, err, constants.CodeExchangeInvalidItems)
	})

	t.Run("换入物品必须同一对方", func(t *testing.T) {
		_, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID, fx.carolP5.ID},
		})
		assertAppErrorCode(t, err, constants.CodeExchangeInvalidItems)
	})

	t.Run("正常发起", func(t *testing.T) {
		p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID, fx.bobP4.ID},
			Note: "相机换平板耳机", TopUpAmount: 100,
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if p.Status != constants.ExchangeStatusPending || p.Turn != constants.ExchangePartyOfferee {
			t.Fatalf("unexpected initial status=%s turn=%s", p.Status, p.Turn)
		}
		if len(p.Items) != 3 || len(p.History) != 1 {
			t.Fatalf("expected 3 items and 1 history, got %d/%d", len(p.Items), len(p.History))
		}
	})

	t.Run("同一物品只能有一个生效提案", func(t *testing.T) {
		// Carol 也想换 Bob 的平板（已被 Alice 的提案占用）。
		_, err := fx.svc.Create(fx.carol.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.carolP5.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
		})
		assertAppErrorCode(t, err, constants.CodeExchangeItemConflict)
		// Alice 不能重复使用自己已被占用的相机发起第二个提案。
		_, err = fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
			OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP4.ID},
		})
		assertAppErrorCode(t, err, constants.CodeExchangeItemConflict)
	})
}

// TestExchangeFullCounterAcceptFlow 还价一次 → 接受 → 物品锁定成交。
func TestExchangeFullCounterAcceptFlow(t *testing.T) {
	fx := setupExchangeFixture(t)
	p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID}, TopUpAmount: 100,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 非参与方不能操作。
	if _, err := fx.svc.Accept(fx.carol.ID, p.ID); err == nil {
		t.Fatal("carol must not accept a proposal she is not part of")
	}

	// pending 阶段回合方是接收人 Bob；Alice 不能抢先接受/还价。
	_, err = fx.svc.Accept(fx.alice.ID, p.ID)
	assertAppErrorCode(t, err, constants.CodeExchangeTurnInvalid)

	// Bob 还价一次。
	countered, err := fx.svc.Counter(fx.bob.ID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 50, Note: "差价少点"})
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	if countered.Status != constants.ExchangeStatusCountered || countered.Round != 1 || countered.Turn != constants.ExchangePartyOfferor {
		t.Fatalf("unexpected countered state: %+v", countered)
	}

	// 还价次数仅一次：轮到 Alice 时再次还价应被拒。
	_, err = fx.svc.Counter(fx.alice.ID, p.ID, dto.ExchangeCounterRequest{TopUpAmount: 10})
	assertAppErrorCode(t, err, constants.CodeExchangeRoundExhausted)
	// Bob 在非自己回合也不能动作。
	_, err = fx.svc.Accept(fx.bob.ID, p.ID)
	assertAppErrorCode(t, err, constants.CodeExchangeTurnInvalid)

	// Alice 接受还价方案 → 成交并锁定物品。
	accepted, err := fx.svc.Accept(fx.alice.ID, p.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if accepted.Status != constants.ExchangeStatusAccepted {
		t.Fatalf("expected accepted, got %s", accepted.Status)
	}
	if len(accepted.History) != 3 {
		t.Fatalf("expected 3 history entries (created/countered/accepted), got %d", len(accepted.History))
	}
	for _, pid := range []uint{fx.aliceP1.ID, fx.bobP3.ID} {
		prod, _ := fx.productRepo.GetByID(pid)
		if prod.Status != constants.ProductStatusSold {
			t.Fatalf("product %d should be sold after accept, got %s", pid, prod.Status)
		}
	}
	// 并发/重复接受只能成功一次：再次接受必失败。
	_, err = fx.svc.Accept(fx.alice.ID, p.ID)
	assertAppErrorCode(t, err, constants.CodeExchangeStateInvalid)
}

// TestExchangeRejectReleasesItems 拒绝后物品释放，可重新发起。
func TestExchangeRejectReleasesItems(t *testing.T) {
	fx := setupExchangeFixture(t)
	p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 只有接收人能拒绝。
	_, err = fx.svc.Reject(fx.alice.ID, p.ID, "不想换")
	assertAppErrorCode(t, err, constants.CodeExchangeTurnInvalid)

	rejected, err := fx.svc.Reject(fx.bob.ID, p.ID, "不想换")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != constants.ExchangeStatusRejected {
		t.Fatalf("expected rejected, got %s", rejected.Status)
	}
	// 释放后 Carol 可以就同一物品发起提案。
	p2, err := fx.svc.Create(fx.carol.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.carolP5.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
	})
	if err != nil {
		t.Fatalf("re-create after reject should succeed, got %v", err)
	}
	if p2.Status != constants.ExchangeStatusPending {
		t.Fatalf("expected pending, got %s", p2.Status)
	}
}

// TestExchangeCancelReleasesItems 发起人取消后物品释放。
func TestExchangeCancelReleasesItems(t *testing.T) {
	fx := setupExchangeFixture(t)
	p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP2.ID}, TargetProductIDs: []uint{fx.bobP4.ID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 接收人不能取消（取消只属于发起人）。
	_, err = fx.svc.Cancel(fx.bob.ID, p.ID, "")
	assertAppErrorCode(t, err, constants.CodeNotExchangeParty)

	cancelled, err := fx.svc.Cancel(fx.alice.ID, p.ID, "先不换了")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != constants.ExchangeStatusCancelled {
		t.Fatalf("expected cancelled, got %s", cancelled.Status)
	}
	// 物品释放后可重新发起。
	if _, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP2.ID}, TargetProductIDs: []uint{fx.bobP4.ID},
	}); err != nil {
		t.Fatalf("re-create after cancel should succeed, got %v", err)
	}
}

// TestExchangeExpireFlow 超时扫描失效提案并释放物品，动作被拒。
func TestExchangeExpireFlow(t *testing.T) {
	fx := setupExchangeFixture(t)
	p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 人为把到期时间调到过去。
	if err := fx.db.Model(&model.ExchangeProposal{}).Where("id = ?", p.ID).
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("backdate expires_at: %v", err)
	}
	n, err := fx.svc.ExpireDue()
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired, got %d", n)
	}
	// 到期后接受应失败。
	_, err = fx.svc.Accept(fx.bob.ID, p.ID)
	assertAppErrorCode(t, err, constants.CodeExchangeExpired)
	// 释放后可重新发起。
	if _, err := fx.svc.Create(fx.carol.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.carolP5.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
	}); err != nil {
		t.Fatalf("create after expire should succeed, got %v", err)
	}
}

// TestExchangePermissionAndDetail 非参与方不能查看详情。
func TestExchangePermissionAndDetail(t *testing.T) {
	fx := setupExchangeFixture(t)
	p, err := fx.svc.Create(fx.alice.ID, dto.ExchangeCreateRequest{
		OfferProductIDs: []uint{fx.aliceP1.ID}, TargetProductIDs: []uint{fx.bobP3.ID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := fx.svc.GetDetail(fx.carol.ID, p.ID); err == nil {
		t.Fatal("non-party user must not read the proposal")
	}
	if _, err := fx.svc.GetDetail(fx.bob.ID, p.ID); err != nil {
		t.Fatalf("party user should read the proposal: %v", err)
	}
}
