package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/ramisoul84/kfc-loyalty/pkg/logger"
)

// MessageHandler processes a single Kafka message.
//
// Returning an error makes the consumer retry (see RetryHandler), then
// log it and commit past the message: a stuck partition is worse than one
// skipped event. Handlers must be idempotent, since retries and
// redeliveries happen.
type MessageHandler func(ctx context.Context, key string, value []byte) error

// Consumer wraps a kafka-go Reader with a consumer group and a handler.
type Consumer struct {
	reader  *kafka.Reader
	handler MessageHandler
	logger  *logger.Logger

	cancel   context.CancelFunc
	done     chan struct{}
	attempts int
}

// Option tweaks the consumer.
type Option func(*Consumer, *kafka.ReaderConfig)

// StartAtLatest makes a brand-new consumer group start from the newest
// message instead of the oldest. Use it for live fan-out (SSE), where
// replaying history to screens would be wrong.
func StartAtLatest() Option {
	return func(_ *Consumer, c *kafka.ReaderConfig) { c.StartOffset = kafka.LastOffset }
}

// RetryHandler retries a failing handler up to attempts times with
// backoff before committing past the message. Use it when a message
// carries money-like state (points) and a transient error must not drop it.
func RetryHandler(attempts int) Option {
	return func(c *Consumer, _ *kafka.ReaderConfig) { c.attempts = attempts }
}

func NewConsumer(
	brokers []string,
	topic string,
	groupID string,
	handler MessageHandler,
	log *logger.Logger,
	opts ...Option,
) *Consumer {
	cfg := kafka.ReaderConfig{
		Brokers:        brokers,
		Topic:          topic,
		GroupID:        groupID,
		MinBytes:       1,    // don't wait for a batch
		MaxBytes:       10e6, // 10 MB
		MaxWait:        500 * time.Millisecond,
		StartOffset:    kafka.FirstOffset, // new groups read from beginning
		CommitInterval: 0,                 // commit synchronously
		ReadBackoffMin: 100 * time.Millisecond,
		ReadBackoffMax: 1 * time.Second,
	}
	c := &Consumer{
		handler:  handler,
		logger:   log,
		done:     make(chan struct{}),
		attempts: 1,
	}
	for _, opt := range opts {
		opt(c, &cfg)
	}
	c.reader = kafka.NewReader(cfg)
	return c
}

// Start begins consuming in a background goroutine and returns immediately.
func (c *Consumer) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	go func() {
		defer close(c.done)

		c.logger.Info("kafka consumer started",
			"topic", c.reader.Config().Topic,
			"group", c.reader.Config().GroupID,
			"brokers", c.reader.Config().Brokers,
		)

		for {
			msg, err := c.reader.FetchMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					c.logger.Info("kafka consumer stopping")
					return
				}
				c.logger.Error("kafka fetch failed", "error", err)
				time.Sleep(time.Second)
				continue
			}

			c.logger.Debug("kafka message received",
				"topic", msg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"key", string(msg.Key),
			)

			if err := c.handle(ctx, msg); err != nil {
				c.logger.Error("kafka handler failed",
					"topic", msg.Topic,
					"partition", msg.Partition,
					"offset", msg.Offset,
					"error", err,
				)
				// Fall through and commit anyway — see type doc.
			}

			if err := c.reader.CommitMessages(ctx, msg); err != nil {
				c.logger.Error("kafka commit failed",
					"partition", msg.Partition,
					"offset", msg.Offset,
					"error", err,
				)
			}
		}
	}()
}

// Stop cancels the consuming loop and closes the reader.
func (c *Consumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	<-c.done

	if c.reader != nil {
		if err := c.reader.Close(); err != nil {
			return fmt.Errorf("close kafka reader: %w", err)
		}
	}
	return nil
}

// handle runs the handler, retrying with exponential backoff.
func (c *Consumer) handle(ctx context.Context, msg kafka.Message) error {
	var err error
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= c.attempts; attempt++ {
		if err = c.handler(ctx, string(msg.Key), msg.Value); err == nil {
			return nil
		}
		if attempt == c.attempts || ctx.Err() != nil {
			break
		}
		c.logger.Warn("kafka handler failed, retrying", "attempt", attempt, "error", err)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return err
		}
		backoff *= 2
	}
	return err
}
