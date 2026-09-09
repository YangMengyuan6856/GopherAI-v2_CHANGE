package controlrecommendation

import (
	"time"

	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
	"GopherAI/internal/observability"
	"GopherAI/internal/policy"
)

func NewDefaultController() (*Controller, error) {
	registry := policy.DefaultStrategyRegistry()
	policyRepository := policy.NewCachedPolicyRepository(
		policy.NewGormPolicyAuthority(mysql.DB),
		policy.NewRedisPolicyCache(redisstore.Rdb, 30*time.Second),
	)
	policyService, err := policy.NewStrategyControlService(policyRepository, registry, policy.DefaultPolicyEnvironment, policy.DefaultRoutingPolicy(), observability.DefaultMetrics())
	if err != nil {
		return nil, err
	}
	return NewController(policyService, NewFileEvaluationReader(DefaultUnifiedReportPath), NewGormRepository(mysql.DB), registry, observability.DefaultMetrics())
}
