package kubernetes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// MaxLineBytes caps one log line. The API reassembles the runtime's 16 KiB
// chunks, so a single line can be large (a JSON payload, a heap dump);
// longer lines are cut and marked, never a reason to stop the stream.
const MaxLineBytes = 1 << 20

const truncatedMark = " … [truncated]"

// kubeletNoLogs starts the text the kubelet returns, as log content, for a
// container whose logs it cannot read any more.
const kubeletNoLogs = "unable to retrieve container logs for "

// Stream reads the logs of one container with the kubelet's timestamps.
func (c *Client) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	cs, err := c.clientset(req.Scope.Context)
	if err != nil {
		return nil, err
	}
	rc, err := cs.CoreV1().Pods(req.Namespace).GetLogs(req.Pod, logOptions(req)).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("logs of %s/%s: %w", req.Pod, req.Container, mapErr(err))
	}
	st := &stream{ch: make(chan domain.RawLine, 256)}
	go st.read(ctx, rc, req)
	return st, nil
}

// logOptions maps a request to the API's options. The API has no head: a
// head reads from the start of the log file (no tail, since or follow) and
// the stream stops after its lines. limitBytes is not used for it: it
// counts bytes and cuts the last line.
func logOptions(req ports.LogRequest) *corev1.PodLogOptions {
	o := &corev1.PodLogOptions{Container: req.Container, Follow: req.Follow, Previous: req.Previous, Timestamps: true}
	if req.Window.IsHead() {
		o.Follow = false
		return o
	}
	switch {
	case !req.SinceTime.IsZero():
		t := metav1.NewTime(req.SinceTime)
		o.SinceTime = &t
	case req.Window.IsTail():
		n := int64(req.Window.Tail)
		o.TailLines = &n
	case req.Window.Since > 0:
		s := int64((req.Window.Since + time.Second - 1) / time.Second)
		o.SinceSeconds = &s
	}
	if req.Limit > 0 && (o.TailLines == nil || *o.TailLines > int64(req.Limit)) {
		n := int64(req.Limit)
		o.TailLines = &n
	}
	return o
}

type stream struct {
	ch  chan domain.RawLine
	err error // written before ch is closed
}

func (s *stream) Lines() <-chan domain.RawLine { return s.ch }
func (s *stream) Err() error                   { return s.err }

func (s *stream) read(ctx context.Context, rc io.ReadCloser, req ports.LogRequest) {
	defer close(s.ch)
	stop := context.AfterFunc(ctx, func() { rc.Close() }) // unblocks the read
	defer stop()
	defer rc.Close()
	r := bufio.NewReaderSize(rc, 64<<10)
	// A head ends after its lines: returning closes the body, which ends
	// the kubelet's read of the file.
	for n := 0; !req.Window.IsHead() || n < req.Window.Head; n++ {
		text, err := readLine(r)
		if len(text) > 0 || err == nil {
			l := splitTimestamp(text)
			if l.Time.IsZero() && strings.HasPrefix(l.Text, kubeletNoLogs) {
				// The kubelet answers 200 with this text when the
				// container's log file is gone (container removed).
				s.err = fmt.Errorf("%s/%s: logs no longer on the node (%s): %w", req.Pod, req.Container, l.Text, domain.ErrNotFound)
				return
			}
			l.Pod, l.Container = req.Pod, req.Container
			select {
			case s.ch <- l:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.err = mapErr(err)
			}
			return
		}
	}
}

// readLine reads one line without its newline, keeping at most
// MaxLineBytes of it.
func readLine(r *bufio.Reader) (string, error) {
	var buf []byte
	truncated := false
	for {
		frag, err := r.ReadSlice('\n')
		if len(buf)+len(frag) <= MaxLineBytes {
			buf = append(buf, frag...)
		} else if !truncated {
			buf = append(buf, frag[:MaxLineBytes-len(buf)]...)
			truncated = true
		}
		switch {
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case err != nil:
			return finish(buf, truncated), err
		}
		return finish(buf, truncated), nil
	}
}

func finish(b []byte, truncated bool) string {
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	if n := len(b); n > 0 && b[n-1] == '\r' {
		b = b[:n-1]
	}
	if truncated {
		return string(b) + truncatedMark
	}
	return string(b)
}

// splitTimestamp separates the RFC 3339 timestamp the kubelet puts before
// each line (timestamps=true) from the text.
func splitTimestamp(line string) domain.RawLine {
	for i := 0; i < len(line) && i < 40; i++ {
		if line[i] == ' ' {
			if t, err := time.Parse(time.RFC3339Nano, line[:i]); err == nil {
				return domain.RawLine{Time: t, Text: line[i+1:]}
			}
			break
		}
	}
	return domain.RawLine{Text: line}
}
