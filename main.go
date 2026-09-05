package main

import (
	"GopherAI/common/mysql"
	"GopherAI/common/rabbitmq"
	"GopherAI/common/redis"
	"GopherAI/config"
	"GopherAI/internal/controlwebhook"
	"GopherAI/internal/observability"
	"GopherAI/router"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
)

func StartServer(addr string, port int) error {
	r := router.InitRouter()
	//服务器静态资源路径映射关系，这里目前不需要
	// r.Static(config.GetConfig().HttpFilePath, config.GetConfig().MusicFilePath)
	return r.Run(fmt.Sprintf("%s:%d", addr, port))
}

func main() {
	gin.SetMode(gin.ReleaseMode)
	conf := config.GetConfig()
	host := conf.MainConfig.Host
	port := conf.MainConfig.Port
	if err := StartPprofServer(conf); err != nil {
		log.Println("StartPprofServer error , " + err.Error())
		return
	}
	//初始化mysql
	if err := mysql.InitMysql(); err != nil {
		log.Println("InitMysql error , " + err.Error())
		return
	}
	//初始化redis
	redis.Init()
	log.Println("redis init success  ")
	rabbitmq.InitRabbitMQ()
	log.Println("rabbitmq init success  ")
	webhookConfig, err := controlwebhook.DefaultConfig()
	if err != nil {
		log.Println("control webhook configuration rejected")
		return
	}
	metricWindowService := observability.NewDefaultMetricWindowService()
	go observability.RunMetricWindowSampler(context.Background(), metricWindowService, 20*time.Second, time.Minute, log.Default())
	webhookRepository := controlwebhook.NewGormRepository(mysql.DB)
	go controlwebhook.RunReconciler(context.Background(), metricWindowService, controlwebhook.NewReconciler(webhookRepository, observability.DefaultMetrics()), 35*time.Second, time.Minute, log.Default())
	if webhookConfig.Enabled {
		dispatcher, dispatcherErr := controlwebhook.NewDispatcher(webhookConfig, webhookRepository, controlwebhook.NewHTTPClient(), observability.DefaultMetrics(), log.Default())
		if dispatcherErr != nil {
			log.Println("control webhook dispatcher configuration rejected")
			return
		}
		go func() {
			if runErr := dispatcher.Run(context.Background()); runErr != nil {
				log.Println("control webhook dispatcher stopped")
			}
		}()
	}

	err = StartServer(host, port) // 启动 HTTP 服务
	if err != nil {
		panic(err)
	}
}
