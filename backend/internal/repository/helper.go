package repository

import "gorm.io/gorm/clause"

// clauseLocking 返回 SELECT ... FOR UPDATE 子句（并发下单/换物提案防超卖与物品占用竞争）。
func clauseLocking() clause.Locking {
	return clause.Locking{Strength: "UPDATE"}
}

// clauseLockingSkipLocked 返回 SELECT ... FOR UPDATE SKIP LOCKED 子句（超时扫描多实例并发互不阻塞）。
// SQLite 方言会忽略该子句，PostgreSQL 下生效。
func clauseLockingSkipLocked() clause.Locking {
	return clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}
}
