//go:build dbtest

package events

import (
	"context"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// ReadCommitted returns every committed (read_committed) record on topic,
// reading from the beginning until the topic is quiet for `quiet`. Test-only
// so tests never import a Kafka client themselves.
func ReadCommitted(ctx context.Context, brokers []string, topic string, quiet time.Duration) ([]Event, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.FetchIsolationLevel(kgo.ReadCommitted()),
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	var out []Event
	for {
		pctx, cancel := context.WithTimeout(ctx, quiet)
		fetches := cl.PollFetches(pctx)
		cancel()
		n := 0
		fetches.EachRecord(func(r *kgo.Record) { out = append(out, fromRecord(r)); n++ })
		if n == 0 && pctx.Err() != nil {
			return out, ctx.Err()
		}
	}
}
