package jobqueue

import (
	queuecontract "GopherAI/internal/jobqueue"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/streadway/amqp"
)

const (
	JobsExchange       = "gopher.jobs.v1"
	DeadLetterExchange = "gopher.jobs.dlx.v1"
	DocumentIndexQueue = "gopher.document.index.v1"
	DocumentRetryQueue = "gopher.document.index.v1.retry"
	DocumentDLQ        = "gopher.document.index.v1.dlq"
	IncidentIndexQueue = "gopher.incident.index.v1"
	IncidentRetryQueue = "gopher.incident.index.v1.retry"
	IncidentDLQ        = "gopher.incident.index.v1.dlq"
	DefaultRetryDelay  = 5 * time.Second
)

type QueueConfig struct {
	Queue      string
	RetryQueue string
	DeadQueue  string
}

var (
	DocumentQueueConfig = QueueConfig{Queue: DocumentIndexQueue, RetryQueue: DocumentRetryQueue, DeadQueue: DocumentDLQ}
	IncidentQueueConfig = QueueConfig{Queue: IncidentIndexQueue, RetryQueue: IncidentRetryQueue, DeadQueue: IncidentDLQ}
)

type RabbitMQ struct {
	connection     *amqp.Connection
	publishChannel *amqp.Channel
	consumeChannel *amqp.Channel
	confirmations  <-chan amqp.Confirmation
	publishMutex   sync.Mutex
}

func Dial(url string) (*RabbitMQ, error) {
	connection, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	broker, err := newRabbitMQ(connection)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return broker, nil
}

func newRabbitMQ(connection *amqp.Connection) (*RabbitMQ, error) {
	if connection == nil {
		return nil, errors.New("rabbitmq connection is required")
	}
	publishChannel, err := connection.Channel()
	if err != nil {
		return nil, fmt.Errorf("open publish channel: %w", err)
	}
	consumeChannel, err := connection.Channel()
	if err != nil {
		_ = publishChannel.Close()
		return nil, fmt.Errorf("open consume channel: %w", err)
	}
	broker := &RabbitMQ{connection: connection, publishChannel: publishChannel, consumeChannel: consumeChannel}
	if err := broker.declareTopology(); err != nil {
		broker.Close()
		return nil, err
	}
	if err := publishChannel.Confirm(false); err != nil {
		broker.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}
	broker.confirmations = publishChannel.NotifyPublish(make(chan amqp.Confirmation, 1))
	if err := consumeChannel.Qos(1, 0, false); err != nil {
		broker.Close()
		return nil, fmt.Errorf("set consumer prefetch: %w", err)
	}
	return broker, nil
}

func (broker *RabbitMQ) Close() {
	if broker == nil {
		return
	}
	if broker.consumeChannel != nil {
		_ = broker.consumeChannel.Close()
	}
	if broker.publishChannel != nil {
		_ = broker.publishChannel.Close()
	}
	if broker.connection != nil {
		_ = broker.connection.Close()
	}
}

func (broker *RabbitMQ) Publish(ctx context.Context, routingKey string, body []byte) error {
	return broker.publishPersistent(ctx, JobsExchange, routingKey, body)
}

func (broker *RabbitMQ) Consume(ctx context.Context, handler func(context.Context, []byte) queuecontract.Result) error {
	return broker.ConsumeQueue(ctx, DocumentQueueConfig, handler)
}

func (broker *RabbitMQ) ConsumeQueue(ctx context.Context, queue QueueConfig, handler func(context.Context, []byte) queuecontract.Result) error {
	if queue.Queue == "" || queue.RetryQueue == "" || queue.DeadQueue == "" || handler == nil {
		return errors.New("complete queue configuration and handler are required")
	}
	deliveries, err := broker.consumeChannel.Consume(queue.Queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("start %s consumer: %w", queue.Queue, err)
	}
	closed := broker.connection.NotifyClose(make(chan *amqp.Error, 1))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case closeError := <-closed:
			if closeError == nil {
				return errors.New("rabbitmq connection closed")
			}
			return fmt.Errorf("rabbitmq connection closed: %w", closeError)
		case delivery, open := <-deliveries:
			if !open {
				return fmt.Errorf("%s delivery channel closed", queue.Queue)
			}
			result := handler(ctx, delivery.Body)
			if err := broker.finishDelivery(ctx, delivery, result, queue); err != nil {
				return err
			}
		}
	}
}

func (broker *RabbitMQ) finishDelivery(ctx context.Context, delivery amqp.Delivery, result queuecontract.Result, queue QueueConfig) error {
	body := result.Body
	if len(body) == 0 {
		body = delivery.Body
	}
	switch result.Action {
	case queuecontract.ActionAck:
		return delivery.Ack(false)
	case queuecontract.ActionRetry:
		if err := broker.publishPersistent(ctx, "", queue.RetryQueue, body); err != nil {
			_ = delivery.Nack(false, true)
			return fmt.Errorf("publish retry message: %w", err)
		}
		return delivery.Ack(false)
	case queuecontract.ActionDead:
		if err := broker.publishPersistent(ctx, DeadLetterExchange, queue.Queue, body); err != nil {
			_ = delivery.Nack(false, true)
			return fmt.Errorf("publish dead letter: %w", err)
		}
		return delivery.Ack(false)
	default:
		_ = delivery.Nack(false, true)
		return fmt.Errorf("unsupported delivery action %q", result.Action)
	}
}

func (broker *RabbitMQ) publishPersistent(ctx context.Context, exchange string, routingKey string, body []byte) error {
	broker.publishMutex.Lock()
	defer broker.publishMutex.Unlock()
	if broker.connection.IsClosed() {
		return errors.New("rabbitmq connection is closed")
	}
	err := broker.publishChannel.Publish(exchange, routingKey, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "application/json",
		Timestamp:    time.Now().UTC(),
		Body:         body,
	})
	if err != nil {
		return err
	}
	select {
	case confirmation, open := <-broker.confirmations:
		if !open {
			return errors.New("publisher confirmation channel closed")
		}
		if !confirmation.Ack {
			return errors.New("rabbitmq negatively acknowledged publish")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New("publisher confirmation timeout")
	}
}

func (broker *RabbitMQ) declareTopology() error {
	for _, channel := range []*amqp.Channel{broker.publishChannel, broker.consumeChannel} {
		if err := channel.ExchangeDeclare(JobsExchange, "topic", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare jobs exchange: %w", err)
		}
		if err := channel.ExchangeDeclare(DeadLetterExchange, "topic", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare dead-letter exchange: %w", err)
		}
	}
	for _, queue := range []QueueConfig{DocumentQueueConfig, IncidentQueueConfig} {
		if err := broker.declareQueue(queue); err != nil {
			return err
		}
	}
	return nil
}

func (broker *RabbitMQ) declareQueue(queue QueueConfig) error {
	mainQueue, err := broker.consumeChannel.QueueDeclare(queue.Queue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": DeadLetterExchange, "x-dead-letter-routing-key": queue.Queue,
	})
	if err != nil {
		return fmt.Errorf("declare %s: %w", queue.Queue, err)
	}
	if err := broker.consumeChannel.QueueBind(mainQueue.Name, queue.Queue, JobsExchange, false, nil); err != nil {
		return fmt.Errorf("bind %s: %w", queue.Queue, err)
	}
	retryQueue, err := broker.consumeChannel.QueueDeclare(queue.RetryQueue, true, false, false, false, amqp.Table{
		"x-message-ttl": int32(DefaultRetryDelay / time.Millisecond), "x-dead-letter-exchange": JobsExchange, "x-dead-letter-routing-key": queue.Queue,
	})
	if err != nil || retryQueue.Name == "" {
		if err == nil {
			err = errors.New("retry queue has no name")
		}
		return fmt.Errorf("declare %s: %w", queue.RetryQueue, err)
	}
	deadQueue, err := broker.consumeChannel.QueueDeclare(queue.DeadQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("declare %s: %w", queue.DeadQueue, err)
	}
	if err := broker.consumeChannel.QueueBind(deadQueue.Name, queue.Queue, DeadLetterExchange, false, nil); err != nil {
		return fmt.Errorf("bind %s: %w", queue.DeadQueue, err)
	}
	return nil
}

var _ queuecontract.Publisher = (*RabbitMQ)(nil)
var _ queuecontract.Consumer = (*RabbitMQ)(nil)
