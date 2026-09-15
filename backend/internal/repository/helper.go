package repository

import "gorm.io/gorm/clause"

// clauseLocking 返回 SELECT ... FOR UPDATE 子句（并发下单/换物提案防超卖与物品占用竞争）。
func clauseLocking() clause.Locking {
	return clause.Locking{Strength: "UPDATE"}
}
