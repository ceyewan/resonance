package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfig_RequiresIsolatedAgentStreamChannel(t *testing.T) {
	config := Config{
		Consumer: ConsumerConfig{
			Topic: "chat", QueueGroup: "chat-group", WorkerCount: 1, MaxRetry: 1, DLQTopic: "chat.dlq",
		},
		StreamConsumer: ConsumerConfig{
			Topic: "stream", QueueGroup: "stream-group", WorkerCount: 1, MaxRetry: 1, DLQTopic: "stream.dlq",
		},
		StreamMaxDeltaBytes: 1024,
	}
	require.NoError(t, config.Validate())

	config.StreamConsumer.Topic = config.Consumer.Topic
	require.ErrorContains(t, config.Validate(), "isolated")
}

func TestConfig_RequiresProgressBeforeAckWait(t *testing.T) {
	config := Config{
		Consumer: ConsumerConfig{
			Topic: "chat", QueueGroup: "chat-group", WorkerCount: 1, MaxRetry: 1,
			DLQTopic: "chat.dlq", ProgressInterval: 30 * time.Second,
		},
		StreamConsumer: ConsumerConfig{
			Topic: "stream", QueueGroup: "stream-group", WorkerCount: 1, MaxRetry: 1,
			DLQTopic: "stream.dlq", ProgressInterval: 5 * time.Second,
		},
		StreamMaxDeltaBytes: 1024,
	}
	config.JetStream.AckWait = 30 * time.Second

	require.ErrorContains(t, config.Validate(), "less than jetstream ack_wait")
	config.Consumer.ProgressInterval = 10 * time.Second
	require.NoError(t, config.Validate())

	config.JetStream.AckWait = 0
	config.Consumer.ProgressInterval = 30 * time.Second
	require.ErrorContains(t, config.Validate(), "less than jetstream ack_wait")

	config.Consumer.ProgressInterval = -time.Second
	require.ErrorContains(t, config.Validate(), "durable consumer configuration is invalid")
}
