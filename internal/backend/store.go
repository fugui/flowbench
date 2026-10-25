// Package backend 实现下游依赖服务（Backend）的内存确定性数据底座与自定义二进制协议处理器。
package backend

import "sync/atomic"

// SKUItem 表示 Backend 底层预置的商品价格、库存与仓储元数据
type SKUItem struct {
	SkuID           string
	SkuName         string
	BasePriceCents  int64
	AvailableStock  int32
	UnitWeightGrams int32
	WarehouseCode   string
}

// UserCreditProfile 表示 Backend 底层预置的用户信用与风险画像
type UserCreditProfile struct {
	UserID                 string
	CreditScore            int32
	HistoricalDefaultCount int32
	AccountAgeMonths       int32
	RiskBlacklisted        bool
}

// Store 提供线程安全、100% 确定性的内存基础数据查询服务，并记录调用计数以便 E2E 校验
type Store struct {
	skus       map[string]SKUItem
	users      map[string]UserCreditProfile
	queryCount atomic.Int64
}

// NewDefaultStore 创建包含标准验证场景数据集（SC-01 ~ SC-06）的内存数据底座
func NewDefaultStore() *Store {
	return &Store{
		skus: map[string]SKUItem{
			"SKU-PRO-01": {
				SkuID:           "SKU-PRO-01",
				SkuName:         "企业级千兆交换机",
				BasePriceCents:  10000, // 100.00 元
				AvailableStock:  50,
				UnitWeightGrams: 1500, // 1.5 kg
				WarehouseCode:   "WH-SH-01",
			},
			"SKU-EDGE-02": {
				SkuID:           "SKU-EDGE-02",
				SkuName:         "边缘计算网关节点",
				BasePriceCents:  80000, // 800.00 元
				AvailableStock:  10,
				UnitWeightGrams: 800,
				WarehouseCode:   "WH-BJ-02",
			},
			"SKU-OUT-OF-STOCK": {
				SkuID:           "SKU-OUT-OF-STOCK",
				SkuName:         "紧缺算力模组",
				BasePriceCents:  50000, // 500.00 元
				AvailableStock:  0,     // 0 库存，触发拒单
				UnitWeightGrams: 2000,
				WarehouseCode:   "WH-SZ-01",
			},
			"SKU-SLOW-500MS": {
				SkuID:           "SKU-SLOW-500MS",
				SkuName:         "慢查询延迟测试件",
				BasePriceCents:  2000,
				AvailableStock:  100,
				UnitWeightGrams: 500,
				WarehouseCode:   "WH-GZ-01",
			},
		},
		users: map[string]UserCreditProfile{
			"USR-VIP-001": {
				UserID:                 "USR-VIP-001",
				CreditScore:            780,
				HistoricalDefaultCount: 0,
				AccountAgeMonths:       36,
				RiskBlacklisted:        false,
			},
			"USR-NORMAL-002": {
				UserID:                 "USR-NORMAL-002",
				CreditScore:            620,
				HistoricalDefaultCount: 1,
				AccountAgeMonths:       14,
				RiskBlacklisted:        false,
			},
			"USR-LOW-003": {
				UserID:                 "USR-LOW-003",
				CreditScore:            510,
				HistoricalDefaultCount: 2,
				AccountAgeMonths:       6,
				RiskBlacklisted:        false,
			},
			"USR-BLACKLIST": {
				UserID:                 "USR-BLACKLIST",
				CreditScore:            350,
				HistoricalDefaultCount: 5,
				AccountAgeMonths:       2,
				RiskBlacklisted:        true,
			},
		},
	}
}

// LookupSKU 查询商品信息
func (s *Store) LookupSKU(skuID string) (SKUItem, bool) {
	item, ok := s.skus[skuID]
	return item, ok
}

// LookupUser 查询用户信用画像（若用户未预置，返回默认中等信用画像以便通用测试）
func (s *Store) LookupUser(userID string) (UserCreditProfile, bool) {
	u, ok := s.users[userID]
	if !ok {
		return UserCreditProfile{
			UserID:                 userID,
			CreditScore:            680,
			HistoricalDefaultCount: 0,
			AccountAgeMonths:       12,
			RiskBlacklisted:        false,
		}, false
	}
	return u, true
}

// IncQueryCount 增加一次 Backend 业务查询计数
func (s *Store) IncQueryCount() int64 {
	return s.queryCount.Add(1)
}

// QueryCount 返回 Backend 累计收到的业务查询次数
func (s *Store) QueryCount() int64 {
	return s.queryCount.Load()
}
