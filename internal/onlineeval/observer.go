package onlineeval

import (
	"GopherAI/common/mysql"
	"GopherAI/internal/app"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

const observerBuffer = 128

const performanceProbeUser = "__perf_probe__"

type queuedObservation struct {
	output app.ChatOutput
	err    error
}

type Observer struct {
	repository Repository
	queue      chan queuedObservation
	clock      func() time.Time
}

var (
	defaultObserverOnce sync.Once
	defaultObserver     *Observer
)

func NewObserver(repository Repository, buffer int, clock func() time.Time) *Observer {
	if buffer <= 0 {
		buffer = observerBuffer
	}
	if clock == nil {
		clock = time.Now
	}
	observer := &Observer{repository: repository, queue: make(chan queuedObservation, buffer), clock: clock}
	go observer.run()
	return observer
}

func NewDefaultObserver() *Observer {
	defaultObserverOnce.Do(func() {
		defaultObserver = NewObserver(NewGormRepository(mysql.DB), observerBuffer, time.Now)
	})
	return defaultObserver
}

// Record never waits for MySQL or RabbitMQ. If the local bounded queue is full,
// the formal answer still succeeds and a structured drop signal is emitted.
func (observer *Observer) Record(output app.ChatOutput, requestErr error) {
	if observer == nil || observer.repository == nil {
		return
	}
	if output.Request.UserID == performanceProbeUser {
		return
	}
	decision := Decide(output, requestErr)
	if !decision.Selected {
		return
	}
	select {
	case observer.queue <- queuedObservation{output: output, err: requestErr}:
	default:
		writeObserverLog("dropped", output.Decision.StrategyName, decision.TrafficClass, "LOCAL_BUFFER_FULL")
	}
}

func (observer *Observer) run() {
	for observation := range observer.queue {
		decision := Decide(observation.output, observation.err)
		sample, event, err := BuildSample(observation.output, observation.err, decision, observer.clock(), false)
		if err != nil {
			writeObserverLog("dropped", observation.output.Decision.StrategyName, decision.TrafficClass, "SAMPLE_BUILD_FAILED")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = observer.repository.Create(ctx, &sample, &event)
		cancel()
		if err != nil {
			writeObserverLog("dropped", sample.Strategy, sample.TrafficClass, "SAMPLE_PERSIST_FAILED")
			continue
		}
		writeObserverLog("selected", sample.Strategy, sample.TrafficClass, "")
	}
}

func writeObserverLog(status, strategy, trafficClass, code string) {
	record := map[string]string{"event": "online_evaluation_sampling", "status": status, "strategy": boundedToken(strategy, 64, "unknown"), "traffic_class": trafficClass}
	if code != "" {
		record["error_code"] = code
	}
	if encoded, err := json.Marshal(record); err == nil {
		log.Print(string(encoded))
	}
}
