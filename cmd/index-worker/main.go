package main

import (
	"GopherAI/common/mysql"
	redisstore "GopherAI/common/redis"
	"GopherAI/config"
	"GopherAI/internal/incident"
	"GopherAI/internal/knowledge"
	jobqueueadapter "GopherAI/internal/platform/jobqueue"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	embeddingArk "github.com/cloudwego/eino-ext/components/embedding/ark"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultWorkerAddress = "127.0.0.1:9091"
	defaultEnvironment   = "prod"
)

type workerState struct {
	ready atomic.Bool
}

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf(`{"event":"index_worker_exit","status":"error","error_code":"WORKER_EXIT"}`)
		os.Exit(1)
	}
}

func run() error {
	gin.SetMode(gin.ReleaseMode)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := mysql.InitMysql(); err != nil {
		return fmt.Errorf("initialize mysql: %w", err)
	}
	redisstore.Init()
	if err := redisstore.Rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("initialize redis: %w", err)
	}
	configuration := config.GetConfig()
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return errors.New("OPENAI_API_KEY is required by the index worker")
	}
	embeddingTimeout := 45 * time.Second
	retryTimes := 1
	embedder, err := embeddingArk.NewEmbedder(ctx, &embeddingArk.EmbeddingConfig{
		BaseURL: configuration.RagBaseUrl, APIKey: apiKey, Model: configuration.RagEmbeddingModel,
		Timeout: &embeddingTimeout, RetryTimes: &retryTimes,
	})
	if err != nil {
		return fmt.Errorf("initialize embedder: %w", err)
	}
	environment := strings.TrimSpace(os.Getenv("GOPHERAI_ENV"))
	if environment == "" {
		environment = defaultEnvironment
	}
	indexer, err := knowledge.NewRedisChunkIndexer(redisstore.Rdb, embedder, environment, configuration.RagDimension, knowledge.DefaultEmbeddingBatchSize)
	if err != nil {
		return fmt.Errorf("initialize redis indexer: %w", err)
	}
	repository := knowledge.NewGormRepository(mysql.DB)
	processor, err := knowledge.NewProcessor(repository, knowledge.NewDefaultStructuredTextChunker(), indexer, configuration.RagEmbeddingModel, knowledge.SystemClock{})
	if err != nil {
		return err
	}
	registry := prometheus.NewRegistry()
	metrics := knowledge.NewWorkerMetrics(registry)
	consumer, err := knowledge.NewIndexConsumer(processor, metrics, knowledge.DefaultMaxDeliveryAttempts, knowledge.SystemClock{})
	if err != nil {
		return err
	}
	incidentRepository := incident.NewGormRepository(mysql.DB)
	incidentIndexer, err := incident.NewRedisCaseIndexer(redisstore.Rdb, environment)
	if err != nil {
		return fmt.Errorf("initialize incident indexer: %w", err)
	}
	incidentProcessor, err := incident.NewProcessor(incidentRepository, incidentIndexer, incident.SystemClock{})
	if err != nil {
		return err
	}
	incidentConsumer, err := incident.NewConsumer(incidentProcessor, metrics, incident.DefaultMaximumAttempts, incident.SystemClock{})
	if err != nil {
		return err
	}
	state := new(workerState)
	server, listener, err := startStatusServer(state, registry)
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownContext)
		_ = listener.Close()
	}()
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf(`{"event":"index_worker_http","status":"error","error_code":"WORKER_HTTP_FAILED"}`)
			cancel()
		}
	}()

	rabbitURL := buildRabbitURL(configuration)
	for ctx.Err() == nil {
		err = runBrokerSession(ctx, rabbitURL, repository, consumer, incidentConsumer, metrics, state)
		state.ready.Store(false)
		if ctx.Err() != nil {
			break
		}
		log.Printf(`{"event":"index_worker_broker","status":"reconnecting","error_code":"BROKER_SESSION_LOST"}`)
		select {
		case <-ctx.Done():
		case <-time.After(3 * time.Second):
		}
	}
	return ctx.Err()
}

func runBrokerSession(ctx context.Context, rabbitURL string, repository *knowledge.GormRepository, consumer *knowledge.IndexConsumer, incidentConsumer *incident.Consumer, metrics *knowledge.WorkerMetrics, state *workerState) error {
	broker, err := jobqueueadapter.Dial(rabbitURL)
	if err != nil {
		return err
	}
	defer broker.Close()
	publisher, err := knowledge.NewOutboxPublisher(repository, broker, metrics, knowledge.SystemClock{}, time.Second, 20)
	if err != nil {
		return err
	}
	sessionContext, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsChannel := make(chan error, 3)
	go func() { errorsChannel <- publisher.Run(sessionContext) }()
	go func() { errorsChannel <- broker.Consume(sessionContext, consumer.Handle) }()
	go func() {
		errorsChannel <- broker.ConsumeQueue(sessionContext, jobqueueadapter.IncidentQueueConfig, incidentConsumer.Handle)
	}()
	state.ready.Store(true)
	log.Print(`{"event":"index_worker","status":"ready"}`)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errorsChannel:
		return err
	}
}

func startStatusServer(state *workerState, gatherer prometheus.Gatherer) (*http.Server, net.Listener, error) {
	address := strings.TrimSpace(os.Getenv("GOPHERAI_WORKER_ADDR"))
	if address == "" {
		address = defaultWorkerAddress
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for worker health: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		writeHealth(writer, http.StatusOK, "alive")
	})
	mux.HandleFunc("/health/ready", func(writer http.ResponseWriter, _ *http.Request) {
		if !state.ready.Load() {
			writeHealth(writer, http.StatusServiceUnavailable, "not_ready")
			return
		}
		writeHealth(writer, http.StatusOK, "ready")
	})
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return server, listener, nil
}

func writeHealth(writer http.ResponseWriter, status int, state string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"service": "GopherAI Index Worker", "status": state})
}

func buildRabbitURL(configuration *config.Config) string {
	authority := &url.URL{
		Scheme: "amqp",
		User:   url.UserPassword(configuration.RabbitmqUsername, configuration.RabbitmqPassword),
		Host:   net.JoinHostPort(configuration.RabbitmqHost, strconv.Itoa(configuration.RabbitmqPort)),
	}
	vhost := strings.TrimPrefix(configuration.RabbitmqVhost, "/")
	if vhost != "" {
		authority.Path = "/" + vhost
	} else {
		authority.Path = "/"
	}
	return authority.String()
}
