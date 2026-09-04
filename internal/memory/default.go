package memory

import (
	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
)

func NewDefaultService() *Service {
	service, err := NewService(
		NewGormAuthority(mysql.DB),
		NewRedisWorkingCache(redisstore.Rdb, DefaultWindowLimit, DefaultWindowTTL),
		NewAssembler(),
	)
	if err != nil {
		panic(err)
	}
	return service
}
