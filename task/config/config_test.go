package config

import (
	"testing"

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
