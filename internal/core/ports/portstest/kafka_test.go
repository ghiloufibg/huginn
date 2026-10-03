package portstest

import (
	"fmt"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func TestFakeKafkaContract(t *testing.T) {
	RunTopicSourceContract(t, func(t *testing.T) TopicFixture {
		clock := NewFakeClock(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
		k := NewFakeKafka(clock)
		k.AddTopic("orders", 3)
		for i := range 30 {
			k.Produce(domain.KafkaRecord{Topic: "orders", Partition: int32(i % 3), Time: clock.Now().Add(-time.Duration(30-i) * time.Second), Value: fmt.Appendf(nil, `{"n":%d}`, i)})
		}
		return TopicFixture{
			Factory: k, Topic: "orders", Tail: 4,
			Produce: func(*testing.T) {
				k.Produce(domain.KafkaRecord{Topic: "orders", Partition: 1, Time: clock.Now(), Value: []byte("new")})
			},
		}
	})
}
