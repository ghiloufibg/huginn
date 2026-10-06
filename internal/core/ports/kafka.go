package ports

import (
	"context"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// --- driven: reading topics -------------------------------------------

// TopicInfo describes a topic as the brokers report it.
type TopicInfo struct {
	Name       string
	Partitions int
	// Err says why the topic cannot be read (domain.ErrNotFound for an
	// unknown topic, domain.ErrForbidden when not authorized).
	Err error
}

// TopicRead asks for the records of one topic.
type TopicRead struct {
	Topic string
	// Since, when positive, starts each partition at the first record at
	// or after now - Since; otherwise each partition starts Tail records
	// before its end.
	Since time.Duration
	Tail  int
	// Limit caps the history read per partition (the newest records of
	// the window), so a long window over a busy topic stays bounded.
	Limit int
	// Follow keeps reading new records once the history is read.
	Follow bool
	// Partitions restricts the read; empty means every partition.
	Partitions []int32
	// ReadCommitted hides the records of aborted transactions.
	ReadCommitted bool
}

// RecordBatch is what a topic read delivers. Records of one partition
// arrive in offset order; partitions interleave freely.
type RecordBatch struct {
	Records []domain.KafkaRecord
	// Notices are messages about the read (a reconnection, records
	// deleted by retention).
	Notices []string
	// HistoryDone is set once every partition reached the end offset it
	// had when the read started; later records are live.
	HistoryDone bool
	// Err ends the read: the channel closes after this batch.
	Err error
}

// TopicSource reads topics of one cluster with one set of credentials,
// strictly read only: it never joins a consumer group, commits an
// offset, produces or creates a topic (docs/DECISIONS.md D-057).
type TopicSource interface {
	// Describe returns the partitions of each topic, in the order given.
	Describe(ctx context.Context, topics []string) ([]TopicInfo, error)
	// Read streams the records of a topic; the channel closes when the
	// read ends (history without Follow, ctx cancelled, or a failure
	// reported in the last batch's Err).
	Read(ctx context.Context, q TopicRead) (<-chan RecordBatch, error)
	// Close releases the connections.
	Close()
}

// TopicSourceFactory connects to a cluster.
type TopicSourceFactory interface {
	Open(ctx context.Context, conn domain.KafkaConnection) (TopicSource, error)
}

// --- driving: the Kafka screen ----------------------------------------

// KafkaTopicState is a topic of a Kafka session.
type KafkaTopicState struct {
	Name      string
	Direction domain.TopicDirection
	// Partitions is the partition count, 0 when unknown.
	Partitions int
	// Err says why the topic cannot be read: a key missing from the
	// sources, credentials rejected, not authorized…
	Err error
}

// KafkaQuery asks for the records of a topic of an open session.
type KafkaQuery struct {
	Topic string
	// Window is a tail (Tail records per partition) or a duration.
	Window        domain.TimeWindow
	Follow        bool
	ReadCommitted bool
	// Raw leaves records written by Schema Registry serializers
	// undecoded, even when the profile names a registry.
	Raw bool
}

// KafkaBatch is what a topic read delivers to the screen. Records are in
// timestamp order within the batch; the history comes in batches that
// follow each other in timestamp order.
type KafkaBatch struct {
	Records     []domain.KafkaRecord
	Notices     []string
	HistoryDone bool
	// Err ends the read.
	Err error
}

// KafkaSession is an open Kafka screen: its topics, resolved and
// described, and the connections behind them.
type KafkaSession interface {
	// Profile names the profile that applied (its file name).
	Profile() string
	// Decodes reports whether records written by Schema Registry
	// serializers are decoded (the profile names a registry).
	Decodes() bool
	// CheckRegistry tells whether the profile names a Schema Registry
	// and, if so, whether it can be used (a request to it).
	CheckRegistry(ctx context.Context) (named bool, err error)
	Topics() []KafkaTopicState
	// Read streams the records of one topic; the channel closes when ctx
	// is cancelled or the read ends.
	Read(ctx context.Context, q KafkaQuery) (<-chan KafkaBatch, error)
	// Close ends every read and closes the connections.
	Close()
}

// Kafka is the driving port behind the Kafka screen.
type Kafka interface {
	// Repos returns the repositories among repos that have a Kafka
	// screen in env. It only checks files for existence, and remembers
	// the answers.
	Repos(ctx context.Context, env domain.Env, repos []string) map[string]bool
	// Open resolves the repository's profile (sources, credentials,
	// topics) and connects. A problem with one topic is reported on the
	// topic; an error means nothing can be shown.
	Open(ctx context.Context, env domain.Env, repo string) (KafkaSession, error)
}
