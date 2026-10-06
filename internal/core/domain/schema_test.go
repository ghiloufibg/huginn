package domain

import "testing"

func TestFramedSchemaID(t *testing.T) {
	for _, tc := range []struct {
		b  []byte
		id uint32
		ok bool
	}{
		{[]byte{0, 0, 0, 0, 7, 'x'}, 7, true},
		{[]byte{0, 0, 0, 1, 0}, 256, true},
		{[]byte{0, 0, 0, 0, 0, 0, 0, 42}, 0, false}, // a big-endian number: no schema 0
		{[]byte{0, 0, 0, 7}, 0, false},              // too short
		{[]byte{1, 0, 0, 0, 7}, 0, false},           // no magic byte
		{nil, 0, false},
	} {
		if id, ok := FramedSchemaID(tc.b); id != tc.id && ok || ok != tc.ok {
			t.Errorf("FramedSchemaID(%v) = %d, %v; want %d, %v", tc.b, id, ok, tc.id, tc.ok)
		}
	}
}
