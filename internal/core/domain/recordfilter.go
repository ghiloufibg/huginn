package domain

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// RecordFilter selects Kafka records. It is a list of terms separated by
// spaces, all of which must match:
//
//	key=<text>          the key contains text
//	partition=<n>       the record is in partition n
//	header.<name>=<text> header name exists and contains text
//	<text>              the key, the value or a header value contains text
//
// Text is matched ignoring ASCII case unless it holds an upper-case letter
// (smart case).
type RecordFilter struct{ terms []recordTerm }

type recordTerm struct {
	field     string // "", "key", "partition", "header"
	header    string
	text      []byte
	fold      bool
	partition int32
}

// ParseRecordFilter parses s; an empty s matches every record.
func ParseRecordFilter(s string) (RecordFilter, error) {
	var f RecordFilter
	for _, w := range strings.Fields(s) {
		t := recordTerm{}
		name, val, ok := strings.Cut(w, "=")
		switch {
		case ok && name == "key":
			t.field = "key"
		case ok && name == "partition":
			n, err := strconv.ParseInt(val, 10, 32)
			if err != nil || n < 0 {
				return RecordFilter{}, fmt.Errorf("partition=%s: not a partition number", val)
			}
			t.field, t.partition = "partition", int32(n)
		case ok && strings.HasPrefix(name, "header.") && len(name) > len("header."):
			t.field, t.header = "header", strings.TrimPrefix(name, "header.")
		default:
			val = w
		}
		t.fold = strings.ToLower(val) == val
		t.text = []byte(val)
		f.terms = append(f.terms, t)
	}
	return f, nil
}

// Empty reports whether the filter matches every record.
func (f RecordFilter) Empty() bool { return len(f.terms) == 0 }

// Match reports whether r matches every term.
func (f RecordFilter) Match(r *KafkaRecord) bool {
	for _, t := range f.terms {
		if !t.match(r) {
			return false
		}
	}
	return true
}

func (t recordTerm) match(r *KafkaRecord) bool {
	switch t.field {
	case "partition":
		return r.Partition == t.partition
	case "key":
		return t.contains(r.Key)
	case "header":
		for _, h := range r.Headers {
			if h.Key == t.header && t.contains(h.Value) {
				return true
			}
		}
		return false
	}
	if t.contains(r.Key) || t.contains(r.Value) {
		return true
	}
	for _, h := range r.Headers {
		if t.contains(h.Value) {
			return true
		}
	}
	return false
}

func (t recordTerm) contains(b []byte) bool {
	if !t.fold {
		return bytes.Contains(b, t.text)
	}
	return indexFold(b, t.text) >= 0
}

// indexFold is bytes.Index ignoring ASCII case; needle is lower case. It
// allocates nothing, so a filter over a full buffer stays cheap.
func indexFold(b, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
	first := needle[0]
	for i := 0; i+len(needle) <= len(b); i++ {
		if lower(b[i]) != first {
			continue
		}
		j := 1
		for j < len(needle) && lower(b[i+j]) == needle[j] {
			j++
		}
		if j == len(needle) {
			return i
		}
	}
	return -1
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
