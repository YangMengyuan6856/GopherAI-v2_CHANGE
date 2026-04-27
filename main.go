package main

import (
	"GopherAI/common/mysql"
	"GopherAI/common/rabbitmq"
	"GopherAI/common/redis"
	"GopherAI/common/skill"
	"GopherAI/config"
	daoskill "GopherAI/dao/skill"
	"GopherAI/router"
	skillsvc "GopherAI/service/skill"
	"fmt"
	"log"
)

func StartServer(addr string, port int) error {
	r := router.InitRouter()
	//服务器静态资源路径映射关系，这里目前不需要
	// r.Static(config.GetConfig().HttpFilePath, config.GetConfig().MusicFilePath)
	return r.Run(fmt.Sprintf("%s:%d", addr, port))
}

// initSkills 注册所有内置技能并注入调用日志器
func initSkills(conf *config.Config) {
	registry := skill.GetRegistry()

	// 注册内置天气技能（复用 MCP 服务）
	mcpBaseURL := "http://localhost:8081/mcp"
	registry.Register(skill.NewWeatherSkill(mcpBaseURL))
	log.Printf("skill [weather] registered, mcp=%s", mcpBaseURL)

	// 注册日期时间技能
	registry.Register(skill.NewDateTimeSkill())
	log.Println("skill [datetime] registered")

	// 注册计算器技能
	registry.Register(skill.NewCalculatorSkill())
	log.Println("skill [calculator] registered")

	// 注册翻译助手技能
	registry.Register(skill.NewTranslateSkill())
	log.Println("skill [translate] registered")

	// 注册文本摘要技能
	registry.Register(skill.NewSummarizeSkill())
	log.Println("skill [summarize] registered")

	// 注册 RAG 知识库检索技能
	registry.Register(skill.NewRAGQuerySkill())
	log.Println("skill [rag_query] registered")

	// 注册智能 Agent 技能（自主调用 MCP 工具）
	registry.Register(skill.NewAgentSkill(mcpBaseURL))
	log.Printf("skill [agent] registered, mcp=%s", mcpBaseURL)

	invoker := skill.GetInvoker()

	// 注入 DB 日志器（异步写，不阻塞执行链路）
	invoker.SetLogger(&daoskill.DBLogger{})
	log.Println("skill invocation logger initialized")

	// 注入用户技能启用状态检查器
	invoker.SetChecker(skillsvc.IsSkillEnabledForUser)
	log.Println("skill user checker initialized")

	// 同步技能元数据到数据库
	skillsvc.SyncSkillsToDB()
}

func main() {
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
