// Copyright (c) the go-richdoc authors.
// SPDX-License-Identifier: BSD-3-Clause

package epub

import (
	"math"
	"testing"

	"github.com/go-richdoc/html"
)

// TestTheByteCeilingReachesTheHtmlReader.
//
// ⛔ TESTED AS A MAPPING, not through a parse. Observing it through behaviour
// would need a chapter larger than 64 MiB — the point of the threading is what
// happens ABOVE the HTML reader's own default — and a 64 MiB fixture buys a
// slow test and several gigabytes of allocation to assert one assignment. The
// claim here is "these ceilings mean those ceilings", which is a claim about a
// function, so it is asked of the function.
func TestTheByteCeilingReachesTheHtmlReader(t *testing.T) {
	got := htmlLimits(Limits{MaxDepth: 7, MaxNodes: 11, MaxEntryBytes: 128 << 20})
	want := html.Limits{MaxDepth: 7, MaxNodes: 11, MaxInputBytes: 128 << 20}
	if got != want {
		t.Errorf("htmlLimits gave %+v, want %+v", got, want)
	}

	// ⛔ A caller who raised the entry ceiling must not be refused at 64 MiB by
	// a ceiling they never set. This is the case the threading exists for, so
	// it is asserted on its own rather than left implicit in the line above.
	if got.MaxInputBytes <= 64<<20 {
		t.Errorf("an entry ceiling of 128 MiB reached the HTML reader as %d",
			got.MaxInputBytes)
	}
}

// TestTheClampIsNotAConversion.
//
// ⛔ The planted positive is the 64-bit value that becomes a SMALL POSITIVE int
// on a 32-bit target. A plain conversion of 1<<32 is zero — a ceiling of zero
// means "use the default" to every orDefaults in this family, so an overflow
// would not even look like an error: it would look like a caller's enormous
// ceiling being quietly ignored.
func TestTheClampIsNotAConversion(t *testing.T) {
	cases := []struct {
		in   int64
		want int
	}{
		{0, 0},
		{1, 1},
		{64 << 20, 64 << 20},
		{int64(maxInt), maxInt},
		{math.MaxInt64, maxInt},
	}
	for _, c := range cases {
		if got := clampToInt(c.in); got != c.want {
			t.Errorf("clampToInt(%d) = %d, want %d", c.in, got, c.want)
		}
	}

	// And on a 64-bit build the clamp must not be a no-op that happens to pass:
	// MaxInt64 is above maxInt only where int is 32 bits, so say which case
	// this run actually exercised.
	if int64(maxInt) == math.MaxInt64 {
		t.Logf("int is 64 bits here, so the clamp returned maxInt for MaxInt64 by equality; "+
			"the 32-bit path is covered by the cross-build matrix (maxInt = %d)", maxInt)
	}
}
