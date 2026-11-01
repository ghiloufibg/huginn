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
	// From and To, when From is set, make an absolute range: each
	// partition starts at the first record at or after From and ends at
	// the first at or after To (the end offset when To is zero: the range
	// is then open and follows). Since and Tail do not apply. Timestamps
	// are set by producers and can be out of order, so a source locates the
	// bounds approximately and the consumer filters records by time.
	From, To time.Time
	// After, with a range, starts each partition it names at that offset
	// (the offset after the last record of the page before) instead of at
	// From; a partition it does not name starts at From.
	After map[int32]int64
	// Limit caps the history read per partition: the newest records of the
	// window, or, with a range, the first Limit records from its start (a
	// page; RecordBatch.Next says where the next one starts). It keeps a
	// long window over a busy topic bounded.
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
	// Next, set with HistoryDone for a range, is where each partition
	// resumes: the offset after the last record the history delivered. More
	// says some partition has records left in the range beyond its page; a
	// read with More does not follow, even when the range is open.
	Next map[int32]int64
	More bool
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
	// Window is a tail (Tail records per partition), a duration, or a range
	// of absolute times. A range larger than the view holds is read in
	// pages: the first page holds the first records of each partition, and
	// KafkaBatch.Next says where the next one starts.
	Window domain.TimeWindow
	// After starts a later page of a range: the offsets KafkaBatch.Next
	// gave, by partition.
	After         map[int32]int64
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
	// Next, set with HistoryDone when a range has records after the page
	// delivered, is the After of the query that reads the next page. A page
	// with a successor is not live: the read does not follow.
	Next map[int32]int64
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
