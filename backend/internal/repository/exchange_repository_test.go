package repository

import (
	"testing"
	"time"

	"github.com/marketpal/marketpal/internal/constants"
	"github.com/marketpal/marketpal/internal/model"
	"gorm.io/gorm"
)

// seedExchangeData 准备两个用户与四件在售物品（用户 1 两件、用户 2 两件）。
func seedExchangeData(t *testing.T, db *gorm.DB) (offeror, offeree *model.User, products []model.Product) {
	t.Helper()
	offeror = &model.User{Username: "ex_alice", PasswordHash: "h", Nickname: "Alice", Role: "user", Status: "active"}
	offeree = &model.User{Username: "ex_bob", PasswordHash: "h", Nickname: "Bob", Role: "user", Status: "active"}
	if err := db.Create(offeror).Error; err != nil {
		t.Fatalf("create offeror: %v", err)
	}
	if err := db.Create(offeree).Error; err != nil {
		t.Fatalf("create offeree: %v", err)
	}
	products = []model.Product{
		{SellerID: offeror.ID, Title: "相机", Description: "换出相机", OriginalPrice: 3000, Price: 2000, Condition: "almost_new", Category: "digital", Status: "on_sale"},
		{SellerID: offeror.ID, Title: "键盘", Description: "换出键盘", OriginalPrice: 400, Price: 200, Condition: "brand_new", Category: "digital", Status: "on_sale"},
		{SellerID: offeree.ID, Title: "平板", Description: "换入平板", OriginalPrice: 2500, Price: 1800, Condition: "lightly_used", Category: "digital", Status: "on_sale"},
		{SellerID: offeree.ID, Title: "耳机", Description: "换入耳机", OriginalPrice: 800, Price: 500, Condition: "almost_new", Category: "digital", Status: "on_sale"},
	}
	for i := range products {
		if err := db.Create(&products[i]).Error; err != nil {
			t.Fatalf("create product: %v", err)
		}
	}
	return offeror, offeree, products
}

func newExchangeProposal(offerorID, offereeID uint, status string, items []model.ExchangeProposalItem) *model.ExchangeProposal {
	return &model.ExchangeProposal{
		ProposalNo:  "EX-TEST-" + time.Now().Format("150405.000000") + "-" + status,
		OfferorID:   offerorID,
		OffereeID:   offereeID,
		Status:      status,
		Turn:        constants.ExchangePartyOfferee,
		Round:       0,
		Note:        "测试提案",
		TopUpAmount: 100,
		TopUpPayer:  constants.ExchangePartyOfferor,
		ExpiresAt:   time.Now().Add(48 * time.Hour),
	}
}

// TestExchangeRepositoryLifecycle 覆盖创建、占用统计、CAS 终态化与自动结束其他提案。
func TestExchangeRepositoryLifecycle(t *testing.T) {
	db := newTestDB(t)
	repo := NewExchangeRepository(db)
	offeror, offeree, products := seedExchangeData(t, db)

	// 提案 A：相机/键盘 换 平板/耳机。
	itemsA := []model.ExchangeProposalItem{
		{ProductID: products[0].ID, OwnerID: offeror.ID, Side: "offer", Title: products[0].Title, Price: products[0].Price},
		{ProductID: products[1].ID, OwnerID: offeror.ID, Side: "offer", Title: products[1].Title, Price: products[1].Price},
		{ProductID: products[2].ID, OwnerID: offeree.ID, Side: "target", Title: products[2].Title, Price: products[2].Price},
		{ProductID: products[3].ID, OwnerID: offeree.ID, Side: "target", Title: products[3].Title, Price: products[3].Price},
	}
	pA := newExchangeProposal(offeror.ID, offeree.ID, constants.ExchangeStatusPending, itemsA)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return repo.CreateWithTx(tx, pA, itemsA)
	}); err != nil {
		t.Fatalf("CreateWithTx A: %v", err)
	}

	got, err := repo.GetByID(pA.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Items) != 4 || len(got.History) != 1 {
		t.Fatalf("expected 4 items and 1 history, got items=%d history=%d", len(got.Items), len(got.History))
	}
	if got.History[0].Action != constants.ExchangeActionCreated {
		t.Fatalf("expected first history action created, got %s", got.History[0].Action)
	}

	t.Run("active_count_by_product", func(t *testing.T) {
		var counts map[uint]int64
		if err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			counts, e = repo.CountActiveByProductIDsTx(tx,
				[]uint{products[0].ID, products[2].ID, 999999}, 0)
			return e
		}); err != nil {
			t.Fatalf("CountActive: %v", err)
		}
		if counts[products[0].ID] != 1 || counts[products[2].ID] != 1 {
			t.Fatalf("expected each involved product busy once, got %v", counts)
		}
		if _, ok := counts[999999]; ok {
			t.Fatal("nonexistent product should not appear in counts")
		}
	})

	// 提案 B：第三方用户也想要平板，与 A 在平板上冲突。
	other := &model.User{Username: "ex_carol", PasswordHash: "h", Nickname: "Carol", Role: "user", Status: "active"}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other: %v", err)
	}
	otherProd := model.Product{SellerID: other.ID, Title: "手表", Description: "第三方物品", OriginalPrice: 2000, Price: 1500, Condition: "brand_new", Category: "other", Status: "on_sale"}
	if err := db.Create(&otherProd).Error; err != nil {
		t.Fatalf("create other product: %v", err)
	}
	itemsB := []model.ExchangeProposalItem{
		{ProductID: otherProd.ID, OwnerID: other.ID, Side: "offer", Title: otherProd.Title, Price: otherProd.Price},
		{ProductID: products[2].ID, OwnerID: offeree.ID, Side: "target", Title: products[2].Title, Price: products[2].Price},
	}
	pB := newExchangeProposal(other.ID, offeree.ID, constants.ExchangeStatusPending, itemsB)
	pB.ProposalNo += "-B"
	if err := db.Transaction(func(tx *gorm.DB) error {
		return repo.CreateWithTx(tx, pB, itemsB)
	}); err != nil {
		t.Fatalf("CreateWithTx B: %v", err)
	}

	t.Run("cas_accept_only_once", func(t *testing.T) {
		var first, second bool
		err := db.Transaction(func(tx *gorm.DB) error {
			now := time.Now()
			var e error
			first, e = repo.CompareAndUpdateStatusTx(tx, pA.ID,
				constants.ExchangeStatusTransitions[constants.ExchangeStatusAccepted],
				map[string]interface{}{"status": constants.ExchangeStatusAccepted, "accepted_at": &now})
			return e
		})
		if err != nil {
			t.Fatalf("first CAS: %v", err)
		}
		if !first {
			t.Fatal("first accept CAS should succeed")
		}
		err = db.Transaction(func(tx *gorm.DB) error {
			var e error
			second, e = repo.CompareAndUpdateStatusTx(tx, pA.ID,
				constants.ExchangeStatusTransitions[constants.ExchangeStatusAccepted],
				map[string]interface{}{"status": constants.ExchangeStatusAccepted})
			return e
		})
		if err != nil {
			t.Fatalf("second CAS: %v", err)
		}
		if second {
			t.Fatal("second accept CAS must fail: only one concurrent accept succeeds")
		}
	})

	t.Run("accept_closes_overlapping_proposals", func(t *testing.T) {
		var closed int64
		err := db.Transaction(func(tx *gorm.DB) error {
			var e error
			closed, e = repo.AutoCloseOthersByProductIDsTx(tx,
				[]uint{products[0].ID, products[1].ID, products[2].ID, products[3].ID}, pA.ID,
				constants.ExchangeStatusCancelled)
			return e
		})
		if err != nil {
			t.Fatalf("AutoCloseOthers: %v", err)
		}
		if closed != 1 {
			t.Fatalf("expected exactly 1 overlapping proposal closed, got %d", closed)
		}
		pBAfter, _ := repo.GetByID(pB.ID)
		if pBAfter.Status != constants.ExchangeStatusCancelled {
			t.Fatalf("expected proposal B cancelled, got %s", pBAfter.Status)
		}
		// 被自动结束的提案应补一条系统历史。
		var sysHist int64
		db.Model(&model.ExchangeProposalHistory{}).
			Where("proposal_id = ? AND actor_role = ?", pB.ID, constants.ExchangePartySystem).Count(&sysHist)
		if sysHist != 1 {
			t.Fatalf("expected 1 system history on closed proposal, got %d", sysHist)
		}
	})
}

// TestExchangeRepositoryExpireDue 超时扫描原子结束到期提案并写历史。
func TestExchangeRepositoryExpireDue(t *testing.T) {
	db := newTestDB(t)
	repo := NewExchangeRepository(db)
	offeror, offeree, products := seedExchangeData(t, db)

	items := []model.ExchangeProposalItem{
		{ProductID: products[0].ID, OwnerID: offeror.ID, Side: "offer", Title: products[0].Title, Price: products[0].Price},
		{ProductID: products[2].ID, OwnerID: offeree.ID, Side: "target", Title: products[2].Title, Price: products[2].Price},
	}
	p := newExchangeProposal(offeror.ID, offeree.ID, constants.ExchangeStatusPending, items)
	p.ProposalNo += "-expire"
	p.ExpiresAt = time.Now().Add(-1 * time.Hour) // 已过期
	if err := db.Transaction(func(tx *gorm.DB) error {
		return repo.CreateWithTx(tx, p, items)
	}); err != nil {
		t.Fatalf("CreateWithTx: %v", err)
	}

	var n int64
	if err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		n, e = repo.ExpireDueTx(tx, 100)
		return e
	}); err != nil {
		t.Fatalf("ExpireDueTx: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired proposal, got %d", n)
	}
	after, err := repo.GetByID(p.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != constants.ExchangeStatusExpired {
		t.Fatalf("expected expired, got %s", after.Status)
	}
	foundExpireHist := false
	for _, h := range after.History {
		if h.Action == constants.ExchangeActionExpired && h.ActorRole == constants.ExchangePartySystem {
			foundExpireHist = true
		}
	}
	if !foundExpireHist {
		t.Fatal("expected an expired system history entry")
	}

	// 再次扫描不应重复处理。
	if err := db.Transaction(func(tx *gorm.DB) error {
		var e error
		n, e = repo.ExpireDueTx(tx, 100)
		return e
	}); err != nil {
		t.Fatalf("second ExpireDueTx: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 expired on second sweep, got %d", n)
	}
}

// TestExchangeRepositoryListByParty 列表按参与方与状态过滤。
func TestExchangeRepositoryListByParty(t *testing.T) {
	db := newTestDB(t)
	repo := NewExchangeRepository(db)
	offeror, offeree, products := seedExchangeData(t, db)
	items := []model.ExchangeProposalItem{
		{ProductID: products[0].ID, OwnerID: offeror.ID, Side: "offer", Title: products[0].Title, Price: products[0].Price},
		{ProductID: products[2].ID, OwnerID: offeree.ID, Side: "target", Title: products[2].Title, Price: products[2].Price},
	}
	p := newExchangeProposal(offeror.ID, offeree.ID, constants.ExchangeStatusPending, items)
	p.ProposalNo += "-list"
	if err := db.Transaction(func(tx *gorm.DB) error {
		return repo.CreateWithTx(tx, p, items)
	}); err != nil {
		t.Fatalf("CreateWithTx: %v", err)
	}

	if _, total, err := repo.ListByParty(offeror.ID, "initiator", "", 1, 10); err != nil || total < 1 {
		t.Fatalf("initiator list total=%d err=%v", total, err)
	}
	if _, total, _ := repo.ListByParty(offeree.ID, "recipient", constants.ExchangeStatusPending, 1, 10); total < 1 {
		t.Fatalf("recipient pending list total=%d", total)
	}
	if _, total, _ := repo.ListByParty(offeree.ID, "recipient", constants.ExchangeStatusAccepted, 1, 10); total != 0 {
		t.Fatalf("recipient accepted list should be empty, total=%d", total)
	}
	// 无关用户看不到该提案。
	random := &model.User{Username: "ex_dave", PasswordHash: "h", Nickname: "Dave", Role: "user", Status: "active"}
	if err := db.Create(random).Error; err != nil {
		t.Fatalf("create random user: %v", err)
	}
	if _, total, err := repo.ListByParty(random.ID, "", "", 1, 10); err != nil || total != 0 {
		t.Fatalf("unrelated user must not see the proposal, total=%d err=%v", total, err)
	}
}
