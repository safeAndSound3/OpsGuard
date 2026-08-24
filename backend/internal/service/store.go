package service

import (
	"sync"

	"monitor-platform/internal/model"
)

var (
	mu sync.RWMutex
	sources []model.DataSource
	rules   []model.CollectionRule
)

func init() {
	// initialize in-memory with mock data from mockdata.go by calling those functions
	sources = GetDataSources()
	rules = GetCollectionRules()
}

func ListDataSources() []model.DataSource {
	mu.RLock()
	defer mu.RUnlock()
	res := make([]model.DataSource, len(sources))
	copy(res, sources)
	return res
}

func AddDataSource(ds model.DataSource) model.DataSource {
	mu.Lock()
	defer mu.Unlock()
	ds.ID = ds.ID
	sources = append([]model.DataSource{ds}, sources...)
	return ds
}

func TestDataSourceConnection(ds model.DataSource) (bool, string) {
	// Mock testing logic. In production this would attempt real connection.
	return true, "连接测试成功，端口开放且认证通过"
}

func ListCollectionRules() []model.CollectionRule {
	mu.RLock()
	defer mu.RUnlock()
	res := make([]model.CollectionRule, len(rules))
	copy(res, rules)
	return res
}

func AddCollectionRule(rule model.CollectionRule) model.CollectionRule {
	mu.Lock()
	defer mu.Unlock()
	rules = append([]model.CollectionRule{rule}, rules...)
	return rule
}

func GetSchemaForDataSource(id string) map[string][]string {
	// Return mock schema: map[table] = []fields
	// In real system this would connect to the data source and fetch actual schema
	return map[string][]string{
		"users":    {"id", "name", "email", "created_at"},
		"orders":   {"id", "user_id", "amount", "status", "created_at"},
		"products": {"id", "sku", "title", "stock", "price"},
	}
}
