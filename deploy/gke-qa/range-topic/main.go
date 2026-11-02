// One-off QA tool (docs/plan/M15-gke-qa.md §2.3): creates the topics that M15's
// Kafka range tests read, and fills them with records whose time is known.
//
//   - range-topic: producer-set timestamps (CreateTime), one record every
//     -step, about 2% stamped 90 s in the past (a producer with a slow
//     clock: out-of-order timestamps), and one partition left without any
//     record for a stretch (an empty partition inside a range).
//   - range-topic-lat: the same records on a topic whose timestamps are
//     assigned by the broker (LogAppendTime), the other path of ListOffsets.
//
// It writes the truth file: every record it produced, with partition,
// offset and the timestamp it asked for. Together with the broker's own
// answer (kafka-console-consumer), that is the oracle for FR-8.
//
// Run once, from outside the cluster through
// `kubectl port-forward svc/kafka-qa 9092:9092` (the PLAINTEXT listener
// advertises localhost, M12). Not a standing service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

func main() {
	broker := flag.String("broker", "localhost:9092", "bootstrap address")
	n := flag.Int("n", 6000, "records per topic")
	parts := flag.Int("partitions", 6, "partitions per topic")
	step := flag.Duration("step", 200*time.Millisecond, "time between two records")
	behind := flag.Int("late-every", 50, "every Nth record is stamped 90 s in the past (0: none)")
	gapFrom := flag.Int("gap-from", 2000, "first record index of the stretch where the last partition gets nothing")
	gapTo := flag.Int("gap-to", 2600, "end (exclusive) of that stretch")
	truth := flag.String("truth", "range-topic-truth.csv", "file the produced records are listed in")
	topic := flag.String("topic", "range-topic", "topic (CreateTime); the LogAppendTime one is <topic>-lat")
	flag.Parse()

	cl, err := kgo.NewClient(kgo.SeedBrokers(*broker), kgo.RecordPartitioner(kgo.ManualPartitioner()))
	if err != nil {
		log.Fatalf("kafka client: %v", err)
	}
	defer cl.Close()
	ctx := context.Background()

	create(ctx, cl, *topic, int32(*parts), nil)
	create(ctx, cl, *topic+"-lat", int32(*parts), map[string]string{"message.timestamp.type": "LogAppendTime"})

	out, err := os.Create(*truth)
	if err != nil {
		log.Fatalf("truth file: %v", err)
	}
	defer out.Close()
	fmt.Fprintln(out, "topic,i,partition,offset,timestamp_ms,late")

	// The last record is stamped a few minutes before now, so every range
	// under test ends in the past and the oracle's snapshot is final.
	base := time.Now().Add(-time.Duration(*n)*(*step) - 5*time.Minute)
	for i := range *n {
		p := int32(i % *parts)
		if p == int32(*parts-1) && i >= *gapFrom && i < *gapTo {
			p = 0 // nothing on the last partition during the gap
		}
		ts := base.Add(time.Duration(i) * (*step))
		late := *behind > 0 && i%*behind == *behind-1
		if late {
			ts = ts.Add(-90 * time.Second)
		}
		for _, t := range []string{*topic, *topic + "-lat"} {
			rec := &kgo.Record{
				Topic: t, Partition: p, Key: fmt.Appendf(nil, "k-%06d", i),
				Value: fmt.Appendf(nil, `{"i":%d,"partition":%d,"late":%t}`, i, p, late),
			}
			if t == *topic {
				rec.Timestamp = ts // CreateTime: ours
			}
			if i == 7 { // a record with headers
				rec.Headers = []kgo.RecordHeader{{Key: "traceId", Value: []byte("range-qa")}}
			}
			if i == 11 { // a tombstone
				rec.Value = nil
			}
			res := cl.ProduceSync(ctx, rec)
			if err := res.FirstErr(); err != nil {
				log.Fatalf("produce %s #%d: %v", t, i, err)
			}
			r := res[0].Record
			fmt.Fprintf(out, "%s,%d,%d,%d,%d,%t\n", t, i, r.Partition, r.Offset, ts.UnixMilli(), late)
		}
	}
	fmt.Printf("produced %d records on each of %s and %s-lat; truth in %s; first timestamp %s, last %s\n",
		*n, *topic, *topic, *truth, base.UTC().Format(time.RFC3339), base.Add(time.Duration(*n-1)*(*step)).UTC().Format(time.RFC3339))
}

// create makes a topic, tolerating one that exists already.
func create(ctx context.Context, cl *kgo.Client, name string, partitions int32, configs map[string]string) {
	req := kmsg.NewPtrCreateTopicsRequest()
	t := kmsg.NewCreateTopicsRequestTopic()
	t.Topic, t.NumPartitions, t.ReplicationFactor = name, partitions, 1
	for k, v := range configs {
		c := kmsg.NewCreateTopicsRequestTopicConfig()
		c.Name, c.Value = k, kmsg.StringPtr(v)
		t.Configs = append(t.Configs, c)
	}
	req.Topics = append(req.Topics, t)
	resp, err := req.RequestWith(ctx, cl)
	if err != nil {
		log.Fatalf("create %s: %v", name, err)
	}
	for _, r := range resp.Topics {
		switch kerr := kerrText(r.ErrorCode); {
		case r.ErrorCode == 0:
			fmt.Printf("created %s (%d partitions)\n", name, partitions)
		case r.ErrorCode == 36: // TOPIC_ALREADY_EXISTS
			fmt.Printf("%s exists already\n", name)
		default:
			log.Fatalf("create %s: error %d %s", name, r.ErrorCode, kerr)
		}
	}
}

func kerrText(code int16) string {
	if code == 0 {
		return ""
	}
	return fmt.Sprintf("kafka error code %d", code)
}
