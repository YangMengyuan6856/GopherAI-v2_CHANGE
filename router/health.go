package router

import (
	"GopherAI/common/mysql"
	"GopherAI/common/rabbitmq"
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	healthcontroller "GopherAI/controller/health"
	healthservice "GopherAI/internal/health"
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func registerHealthRoutes(engine *gin.Engine) {
	service := healthservice.NewService("GopherAI", 1500*time.Millisecond, []healthservice.Probe{
		{Name: "mysql", Required: true, Check: checkMySQL},
		{Name: "redis_cache", Required: true, Check: checkRedisCache},
		{Name: "redis_vector", Required: true, Check: checkRedisVector},
		{Name: "rabbitmq", Required: true, Check: rabbitmq.Ping},
		{Name: "model_config", Required: true, Check: checkModelConfig},
	})
	controller := healthcontroller.NewController(service)
	engine.GET("/health/live", controller.Live)
	engine.GET("/health/ready", controller.Ready)
}

func checkMySQL(ctx context.Context) error {
	if mysql.DB == nil {
		return errors.New("mysql is not initialized")
	}
	database, err := mysql.DB.DB()
	if err != nil {
		return err
	}
	return database.PingContext(ctx)
}

func checkRedisCache(ctx context.Context) error {
	if redisstore.Rdb == nil {
		return errors.New("redis is not initialized")
	}
	return redisstore.Rdb.Ping(ctx).Err()
}

func checkRedisVector(ctx context.Context) error {
	if redisstore.Rdb == nil {
		return errors.New("redis is not initialized")
	}
	return redisstore.Rdb.Do(ctx, "FT._LIST").Err()
}

func checkModelConfig(context.Context) error {
	conf := config.GetConfig()
	values := []string{
		os.Getenv("OPENAI_API_KEY"),
		conf.RagModelConfig.RagBaseUrl,
		conf.RagModelConfig.RagChatModelName,
		conf.RagModelConfig.RagEmbeddingModel,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return errors.New("required model configuration is missing")
		}
	}
	return nil
}
