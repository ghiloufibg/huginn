// Package avrojson turns Avro binary data into JSON, given the schema it
// was written with (the writer schema read from the Schema Registry).
//
// The output is Avro's JSON encoding, as kafka-avro-console-consumer prints
// it (fields in schema order, a union value wrapped in its branch name,
// null plain), except for logical types, which are made readable: a decimal
// is an exact decimal string, timestamps and dates are ISO 8601, a
// duration an object, and other bytes and fixed values 0x… hex
// (docs/plan/M13-schema-registry.md §2).
//
// Data is untrusted: every length is checked against the bytes left,
// nesting is bounded, and the output is capped, so a malformed record
// fails instead of allocating without bound or recursing without end.
package avrojson

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/hamba/avro/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Limits on what one record may produce.
const (
	maxDepth = 64       // nested records, arrays, maps and unions
	maxItems = 1 << 20  // array items and map entries, in total
	maxOut   = 64 << 20 // bytes of JSON
)

// Codec decodes data written with one schema. It is immutable and safe for
// concurrent use.
type Codec struct {
	schema avro.Schema
	name   string
}

// NewCodec parses a writer schema. refs are the schemas it references
// (Schema Registry references), each parsed before the next, in the
// order given: a referenced schema comes before the schemas using it.
func NewCodec(schema string, refs ...string) (*Codec, error) {
	cache := &avro.SchemaCache{} // one per codec: names never leak between registries
	for i, r := range refs {
		if _, err := avro.ParseWithCache(r, "", cache); err != nil {
			return nil, fmt.Errorf("referenced schema %d: %w", i+1, err)
		}
	}
	s, err := avro.ParseWithCache(schema, "", cache)
	if err != nil {
		return nil, err
	}
	c := &Codec{schema: s, name: string(s.Type())}
	if n, ok := s.(avro.NamedSchema); ok {
		c.name = n.FullName()
	}
	return c, nil
}

// Name is the schema's full name (a record's), or its type.
func (c *Codec) Name() string { return c.name }

// Decode appends to dst the JSON of data, a value written with the codec's
// schema. Errors wrap domain.ErrInvalidPayload and say where the data
// stopped making sense. Bytes left after the value are ignored, as the
// Java deserializer does.
func (c *Codec) Decode(dst, data []byte) ([]byte, error) { return c.DecodeLimit(dst, data, maxOut) }

// DecodeLimit is Decode with at most maxJSON bytes of JSON (at most the
// package's own bound): a larger value fails instead of growing on.
func (c *Codec) DecodeLimit(dst, data []byte, maxJSON int) ([]byte, error) {
	if maxJSON <= 0 || maxJSON > maxOut {
		maxJSON = maxOut
	}
	d := decoder{data: data, out: dst, maxOut: len(dst) + maxJSON}
	if err := d.value(c.schema, 0); err != nil {
		return nil, fmt.Errorf("invalid avro at byte %d: %w: %w", d.pos, err, domain.ErrInvalidPayload)
	}
	return d.out, nil
}

var (
	errShort   = errors.New("data ends early")
	errDeep    = errors.New("nested too deep")
	errTooMany = errors.New("too many items")
	errTooBig  = errors.New("value too large")
)

type decoder struct {
	data   []byte
	pos    int
	out    []byte
	items  int
	maxOut int // len(out) beyond which the value is too large
}

func (d *decoder) left() int { return len(d.data) - d.pos }

// long reads a zigzag varint (int and long are encoded alike).
func (d *decoder) long() (int64, error) {
	u, n := binary.Uvarint(d.data[d.pos:])
	if n <= 0 {
		return 0, errShort
	}
	d.pos += n
	return int64(u>>1) ^ -int64(u&1), nil
}

// int reads an Avro int, which must fit 32 bits.
func (d *decoder) int() (int32, error) {
	n, err := d.long()
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("int %d out of range", n)
	}
	return int32(n), nil
}

// take returns the next n bytes.
func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || n > d.left() {
		return nil, errShort
	}
	b := d.data[d.pos : d.pos+n]
	d.pos += n
	return b, nil
}

// bytes reads length-prefixed bytes.
func (d *decoder) bytes() ([]byte, error) {
	n, err := d.long()
	if err != nil {
		return nil, err
	}
	if n < 0 || n > int64(d.left()) {
		return nil, errShort
	}
	return d.take(int(n))
}

func (d *decoder) raw(s string) { d.out = append(d.out, s...) }

func (d *decoder) value(s avro.Schema, depth int) error {
	if depth > maxDepth {
		return errDeep
	}
	if len(d.out) > d.maxOut {
		return errTooBig
	}
	switch s := s.(type) {
	case *avro.RefSchema:
		return d.value(s.Schema(), depth)
	case *avro.NullSchema:
		d.raw("null")
		return nil
	case *avro.PrimitiveSchema:
		return d.primitive(s)
	case *avro.RecordSchema:
		d.raw("{")
		for i, f := range s.Fields() {
			if i > 0 {
				d.raw(",")
			}
			d.out = appendString(d.out, f.Name())
			d.raw(":")
			if err := d.value(f.Type(), depth+1); err != nil {
				return err
			}
		}
		d.raw("}")
		return nil
	case *avro.EnumSchema:
		i, err := d.int()
		if err != nil {
			return err
		}
		syms := s.Symbols()
		if i < 0 || int(i) >= len(syms) {
			return fmt.Errorf("enum index %d out of %d symbols", i, len(syms))
		}
		d.out = appendString(d.out, syms[i])
		return nil
	case *avro.ArraySchema:
		d.raw("[")
		err := d.blocks(func(first bool) error {
			if !first {
				d.raw(",")
			}
			return d.value(s.Items(), depth+1)
		})
		d.raw("]")
		return err
	case *avro.MapSchema:
		d.raw("{")
		err := d.blocks(func(first bool) error {
			if !first {
				d.raw(",")
			}
			k, err := d.bytes()
			if err != nil {
				return err
			}
			d.out = appendBytes(d.out, k)
			d.raw(":")
			return d.value(s.Values(), depth+1)
		})
		d.raw("}")
		return err
	case *avro.UnionSchema:
		i, err := d.long()
		if err != nil {
			return err
		}
		types := s.Types()
		if i < 0 || i >= int64(len(types)) {
			return fmt.Errorf("union branch %d out of %d", i, len(types))
		}
		branch := types[i]
		if branch.Type() == avro.Null {
			d.raw("null")
			return nil
		}
		d.raw("{")
		d.out = appendString(d.out, branchName(branch))
		d.raw(":")
		if err := d.value(branch, depth+1); err != nil {
			return err
		}
		d.raw("}")
		return nil
	case *avro.FixedSchema:
		b, err := d.take(s.Size())
		if err != nil {
			return err
		}
		return d.fixed(b, s.Logical())
	}
	return fmt.Errorf("unsupported schema %s", s.Type())
}

// blocks reads the blocks of an array or a map, calling item for each
// entry. A negative count is followed by the block's size in bytes.
func (d *decoder) blocks(item func(first bool) error) error {
	first := true
	for {
		n, err := d.long()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if n < 0 {
			if n == math.MinInt64 {
				return errTooMany
			}
			n = -n
			if _, err := d.long(); err != nil { // block size, not needed
				return err
			}
		}
		if d.items += int(min(n, maxItems+1)); d.items > maxItems {
			return errTooMany
		}
		for range n {
			if err := item(first); err != nil {
				return err
			}
			first = false
		}
	}
}

func (d *decoder) primitive(s *avro.PrimitiveSchema) error {
	var logical avro.LogicalType
	if l := s.Logical(); l != nil {
		logical = l.Type()
	}
	switch s.Type() {
	case avro.Boolean:
		b, err := d.take(1)
		if err != nil {
			return err
		}
		switch b[0] {
		case 0:
			d.raw("false")
		case 1:
			d.raw("true")
		default:
			return fmt.Errorf("boolean byte %d", b[0])
		}
	case avro.Int:
		n, err := d.int()
		if err != nil {
			return err
		}
		switch logical {
		case avro.Date:
			d.time(time.Unix(int64(n)*86400, 0).UTC(), time.DateOnly)
		case avro.TimeMillis:
			d.clock(int64(n)*int64(time.Millisecond), "15:04:05.000")
		default:
			d.out = strconv.AppendInt(d.out, int64(n), 10)
		}
	case avro.Long:
		n, err := d.long()
		if err != nil {
			return err
		}
		d.long64(n, logical)
	case avro.Float:
		b, err := d.take(4)
		if err != nil {
			return err
		}
		d.float(float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), 32)
	case avro.Double:
		b, err := d.take(8)
		if err != nil {
			return err
		}
		d.float(math.Float64frombits(binary.LittleEndian.Uint64(b)), 64)
	case avro.Bytes:
		b, err := d.bytes()
		if err != nil {
			return err
		}
		return d.fixed(b, s.Logical())
	case avro.String:
		b, err := d.bytes()
		if err != nil {
			return err
		}
		d.out = appendBytes(d.out, b)
		if len(d.out) > d.maxOut { // one string can be the whole value
			return errTooBig
		}
	default:
		return fmt.Errorf("unsupported type %s", s.Type())
	}
	return nil
}

// long64 writes a long, readable for the time logical types.
func (d *decoder) long64(n int64, logical avro.LogicalType) {
	const local = "2006-01-02T15:04:05"
	switch logical {
	case avro.TimeMicros:
		d.clock(n*int64(time.Microsecond), "15:04:05.000000")
	case avro.TimestampMillis:
		d.time(time.UnixMilli(n).UTC(), local+".000Z07:00")
	case avro.TimestampMicros:
		d.time(time.UnixMicro(n).UTC(), local+".000000Z07:00")
	case avro.LocalTimestampMillis:
		d.time(time.UnixMilli(n).UTC(), local+".000")
	case avro.LocalTimestampMicros:
		d.time(time.UnixMicro(n).UTC(), local+".000000")
	default:
		d.out = strconv.AppendInt(d.out, n, 10)
	}
}

// time writes a time as a JSON string (layouts need no escaping).
func (d *decoder) time(t time.Time, layout string) {
	d.raw(`"`)
	d.out = t.AppendFormat(d.out, layout)
	d.raw(`"`)
}

// clock writes a time of day; a value outside a day is written in µs.
func (d *decoder) clock(ns int64, layout string) {
	if ns < 0 || ns >= int64(24*time.Hour) {
		d.raw(`"`)
		d.out = strconv.AppendInt(d.out, ns/int64(time.Microsecond), 10)
		d.raw(`µs"`)
		return
	}
	d.time(time.Unix(0, ns).UTC(), layout)
}

// float writes a number; JSON has no NaN or infinities, written as strings.
func (d *decoder) float(f float64, bits int) {
	switch {
	case math.IsNaN(f):
		d.raw(`"NaN"`)
	case math.IsInf(f, 1):
		d.raw(`"Infinity"`)
	case math.IsInf(f, -1):
		d.raw(`"-Infinity"`)
	default:
		d.out = strconv.AppendFloat(d.out, f, 'g', -1, bits)
	}
}

// fixed writes bytes or a fixed value: a decimal, a duration, or hex.
func (d *decoder) fixed(b []byte, l avro.LogicalSchema) error {
	switch dec := l.(type) {
	case *avro.DecimalLogicalSchema:
		d.out = appendString(d.out, decimal(b, dec.Scale()))
		return nil
	case nil:
	default:
		if l.Type() == avro.Duration && len(b) == 12 {
			d.raw(`{"months":`)
			d.out = strconv.AppendUint(d.out, uint64(binary.LittleEndian.Uint32(b)), 10)
			d.raw(`,"days":`)
			d.out = strconv.AppendUint(d.out, uint64(binary.LittleEndian.Uint32(b[4:])), 10)
			d.raw(`,"millis":`)
			d.out = strconv.AppendUint(d.out, uint64(binary.LittleEndian.Uint32(b[8:])), 10)
			d.raw("}")
			return nil
		}
	}
	d.out = append(d.out, `"0x`...)
	d.out = hex.AppendEncode(d.out, b)
	d.raw(`"`)
	return nil
}

// decimal is the exact decimal of a big-endian two's-complement unscaled
// value and a scale: 1250 with scale 2 is "12.50".
func decimal(b []byte, scale int) string {
	n := new(big.Int).SetBytes(b)
	if len(b) > 0 && b[0]&0x80 != 0 { // negative
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(len(b))*8))
	}
	s := n.Abs(n).String()
	neg := len(b) > 0 && b[0]&0x80 != 0 && s != "0"
	if scale > 0 {
		for len(s) <= scale {
			s = "0" + s
		}
		s = s[:len(s)-scale] + "." + s[len(s)-scale:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// branchName is the name a union branch is wrapped in: a named type's full
// name, else its type.
func branchName(s avro.Schema) string {
	if r, ok := s.(*avro.RefSchema); ok {
		return r.Schema().FullName()
	}
	if n, ok := s.(avro.NamedSchema); ok {
		return n.FullName()
	}
	return string(s.Type())
}

// appendString appends s as a JSON string. Invalid UTF-8 becomes U+FFFD.
func appendString(dst []byte, s string) []byte {
	return appendJSONString(dst, s, utf8.DecodeRuneInString)
}

// appendBytes is appendString for bytes, without converting them.
func appendBytes(dst, b []byte) []byte { return appendJSONString(dst, b, utf8.DecodeRune) }

func appendJSONString[T string | []byte](dst []byte, s T, decode func(T) (rune, int)) []byte {
	const hexDigits = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				dst = append(dst, '\\', c)
			case c == '\n':
				dst = append(dst, '\\', 'n')
			case c == '\r':
				dst = append(dst, '\\', 'r')
			case c == '\t':
				dst = append(dst, '\\', 't')
			case c < 0x20 || c == 0x7f:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			default:
				dst = append(dst, c)
			}
			i++
			continue
		}
		r, size := decode(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, "\uFFFD"...) // the replacement character, as UTF-8
		} else {
			dst = append(dst, s[i:i+size]...)
		}
		i += size
	}
	return append(dst, '"')
}
