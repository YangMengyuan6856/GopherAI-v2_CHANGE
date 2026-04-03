package main

import (
	"GopherAI/common/aihelper"
	"GopherAI/common/mysql"
	"GopherAI/common/rabbitmq"
	"GopherAI/common/redis"
	"GopherAI/common/skill"
	"GopherAI/config"
	daoskill "GopherAI/dao/skill"
	"GopherAI/dao/message"
	"GopherAI/router"
	"fmt"
	"log"
)

func StartServer(addr string, port int) error {
	r := router.InitRouter()
	//服务器静态资源路径映射关系，这里目前不需要
	// r.Static(config.GetConfig().HttpFilePath, config.GetConfig().MusicFilePath)
	return r.Run(fmt.Sprintf("%s:%d", addr, port))
}

// 从数据库加载消息并初始化 AIHelperManager
func readDataFromDB() error {
	manager := aihelper.GetGlobalManager()
	// 从数据库读取所有消息
	msgs, err := message.GetAllMessages()
	if err != nil {
		return err
	}
	// 遍历数据库消息
	for i := range msgs {
		m := &msgs[i]
		//默认openai模型
		modelType := "1"
		config := make(map[string]interface{})

		// 创建对应的 AIHelper
		helper, err := manager.GetOrCreateAIHelper(m.UserName, m.SessionID, modelType, config)
		if err != nil {
			log.Printf("[readDataFromDB] failed to create helper for user=%s session=%s: %v", m.UserName, m.SessionID, err)
			continue
		}
		log.Println("readDataFromDB init:  ", helper.SessionID)
		// 添加消息到内存中(不开启存储功能)
		helper.AddMessage(m.Content, m.UserName, m.IsUser, false)
	}

	log.Println("AIHelperManager init success ")
	return nil
}

// initSkills 注册所有内置技能并注入调用日志器
func initSkills(conf *config.Config) {
	registry := skill.GetRegistry()

	// 注册内置天气技能（复用 MCP 服务）
	mcpBaseURL := "http://localhost:8081/mcp"
	registry.Register(skill.NewWeatherSkill(mcpBaseURL))
	log.Printf("skill [weather] registered, mcp=%s", mcpBaseURL)

	// 注入 DB 日志器（异步写，不阻塞执行链路）
	skill.GetInvoker().SetLogger(&daoskill.DBLogger{})
	log.Println("skill invocation logger initialized")
}

func main() {
	conf := config.GetConfig()
	host := conf.MainConfig.Host
	port := conf.MainConfig.Port
	//初始化mysql
	if err := mysql.InitMysql(); err != nil {
		log.Println("InitMysql error , " + err.Error())
		return
	}
	//初始化AIHelperManager
	readDataFromDB()

	//初始化 Skill 注册中心
	initSkills(conf)
	log.Println("skill registry init success")

	//初始化redis
	redis.Init()
	log.Println("redis init success  ")
	rabbitmq.InitRabbitMQ()
	log.Println("rabbitmq init success  ")

	err := StartServer(host, port) // 启动 HTTP 服务
	if err != nil {
		panic(err)
	}
}
