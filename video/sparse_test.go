// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only

package video

import (
	"bytes"
	"testing"
)

func TestTailRing_RetainsLastBytes(t *testing.T) {
	cases := []struct {
		name  string
		cap   int
		write [][]byte
		want  []byte
	}{
		{"empty", 4, nil, []byte{}},
		{"under cap, single", 8, [][]byte{[]byte("abc")}, []byte("abc")},
		{"under cap, multi", 8, [][]byte{[]byte("ab"), []byte("cd")}, []byte("abcd")},
		{"exactly cap", 4, [][]byte{[]byte("abcd")}, []byte("abcd")},
		{"over cap, single", 4, [][]byte{[]byte("abcdef")}, []byte("cdef")},
		{"over cap, multi wrap", 4, [][]byte{[]byte("abc"), []byte("def")}, []byte("cdef")},
		{"over cap, many small", 3, [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}, []byte("cde")},
		{"big then small", 4, [][]byte{[]byte("abcdef"), []byte("gh")}, []byte("efgh")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTailRing(tc.cap)
			for _, w := range tc.write {
				r.Write(w)
			}
			if got := r.Bytes(); !bytes.Equal(got, tc.want) {
				t.Fatalf("ring=%q, want %q", got, tc.want)
			}
		})
	}
}
