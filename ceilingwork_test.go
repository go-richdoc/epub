// Copyright (c) the go-richdoc authors.
// SPDX-License-Identifier: BSD-3-Clause

// Does a ceiling stop the WORK, or only the ANSWER?
//
// ⛔ THE CONTROL IS THE WHOLE TEST, ported from go-richdoc/data where it found a
// JSON ceiling checked after encoding/json had already built everything, and
// then from go-richdoc/html where it found the same thing again — a refusal
// that cost 96 MB where it now costs 7 096 bytes. Both times the code READ as
// if the guard were in the right place; both times running the control settled
// it in one measurement. So it is ported here rather than argued about, and it
// stays committed so that a future change to how entries are inflated has to
// face it.
//
// ⛔ Every figure is a count of BYTES from runtime.MemStats: exact, and
// independent of what else the machine is doing (these were taken at a load
// average above 50). There is no timing claim here.
package epub

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
)

// allocatedBy is how many bytes one call asks the allocator for in total.
//
// ⛔ retained is SIGNED: a refusal frees what it inflated, so the heap can end
// LOWER than it started, and as a uint64 subtraction that reads as eighteen
// quintillion rather than as minus four hundred kilobytes.
func allocatedBy(t *testing.T, f func() (*richdoc.Document, error)) (total uint64, retained int64, err error) {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	doc, err := f()

	runtime.ReadMemStats(&after)
	total = after.TotalAlloc - before.TotalAlloc
	runtime.GC()
	var held runtime.MemStats
	runtime.ReadMemStats(&held)
	retained = int64(held.HeapAlloc) - int64(before.HeapAlloc)
	runtime.KeepAlive(doc)
	return total, retained, err
}

// bookWithChapterOf is an ordinary publication whose first chapter holds n
// paragraphs — so the archive stays small and what it inflates to does not.
//
// ⛔ A DEFLATED chapter, which is the point: the entry on disk is a few
// kilobytes and the bytes it becomes are what the ceiling is about. A stored
// entry would make the archive as big as its contents and the experiment would
// be measuring the file size instead.
func bookWithChapterOf(t testing.TB, n int) []byte {
	t.Helper()
	return build(t,
		part{name: "mimetype", data: "application/epub+zip", store: true},
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<?xml version="1.0"?>
		<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
		  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
		    <dc:title>A Book</dc:title><dc:identifier id="pub-id">urn:uuid:1</dc:identifier>
		  </metadata>
		  <manifest>
		    <item id="c1" href="text/chapter1.xhtml" media-type="application/xhtml+xml"/>
		  </manifest>
		  <spine><itemref idref="c1"/></spine>
		</package>`},
		part{name: "OEBPS/text/chapter1.xhtml",
			data: "<html><body>" + strings.Repeat("<p>one</p>", n) + "</body></html>"},
	)
}

// TestTheTotalByteCeilingStopsTheWork.
//
// ⛔ Two archives refused by the SAME ceiling, the second inflating to eight
// times what the first does. The reader was supposed to stop at the same
// inflated byte in both, so the costs must agree. If allocation tracks the
// archive instead, the ceiling is checked after the inflation it exists to
// prevent — which is what a ZIP bomb is.
func TestTheTotalByteCeilingStopsTheWork(t *testing.T) {
	const (
		ceiling = 4 << 10
		small   = 20_000  // ~200 KB inflated
		large   = 160_000 // ~1.6 MB inflated, eight times as much
	)

	cost := func(n int) uint64 {
		src := bookWithChapterOf(t, n)
		total, _, err := allocatedBy(t, func() (*richdoc.Document, error) {
			return Parse(src, Limits{MaxTotalBytes: ceiling})
		})
		if err == nil {
			t.Fatalf("a chapter of %d paragraphs against a %d byte ceiling was accepted", n, ceiling)
		}
		t.Logf("%d paragraphs, archive %d bytes, refused: %d allocated", n, len(src), total)
		return total
	}

	s, l := cost(small), cost(large)
	ratio := float64(l) / float64(s)
	t.Logf("same ceiling, 8x the inflated size: %d -> %d bytes allocated, ratio %.1f", s, l, ratio)
	if ratio > 2 {
		t.Errorf("the total-byte ceiling does not bound the work: 8x the inflated size costs "+
			"%.1fx the allocation", ratio)
	}
}

// TestWhatOneByteOfInflatedContentBuys.
//
// ⛔ PER BYTE INFLATED, AND NOT PER BYTE OF ARCHIVE. The first draft of this
// test used the archive's size and reported 8 058.8 bytes of heap per byte —
// a number with no ceiling worth setting, because the AUTHOR OF THE FILE picks
// the compression ratio. Deflate squeezes a repeated paragraph about a
// hundredfold, so the same reader "amplifies" by whatever factor the attacker
// chooses to compress by. The quantity the reader controls is what it does per
// byte it has inflated, and that is what MaxTotalBytes bounds, so that is the
// denominator.
func TestWhatOneByteOfInflatedContentBuys(t *testing.T) {
	const (
		paragraphs = 20_000
		ceiling    = 400 // bytes allocated per byte INFLATED
	)
	src := bookWithChapterOf(t, paragraphs)

	// What the reader inflates: the chapter, plus the container and the package
	// document, which are small and counted so the figure is not flattered.
	inflated := len("<html><body></body></html>") + paragraphs*len("<p>one</p>")

	total, retained, err := allocatedBy(t, func() (*richdoc.Document, error) {
		return Parse(src, Limits{})
	})
	if err != nil {
		t.Fatalf("%d bytes of archive, %d inflated: %v", len(src), inflated, err)
	}
	perByte := float64(total) / float64(inflated)
	t.Logf("%d bytes of archive -> %d bytes inflated -> %d allocated (%.1f per inflated byte), %d retained",
		len(src), inflated, total, perByte, retained)
	if perByte > ceiling {
		t.Errorf("allocates %.1f bytes per byte inflated, past the ceiling of %d",
			perByte, ceiling)
	}
}

// TestTheBookHasANodeCeilingAndNotJustEachChapter.
//
// ⛔ THE WITNESS IS THE SPINE, not one big chapter. Every chapter here is well
// inside MaxNodes and passes its own ceiling honestly; it is their SUM that
// nothing bounded, which is the shape of defect this family keeps producing —
// two ceilings that multiply, with each factor correct.
func TestTheBookHasANodeCeilingAndNotJustEachChapter(t *testing.T) {
	const (
		chapters   = 20
		paragraphs = 400 // 800 nodes a chapter: a paragraph and its text
	)

	var parts []part
	var manifest, spine strings.Builder
	for i := range chapters {
		name := fmt.Sprintf("text/c%d.xhtml", i)
		manifest.WriteString(fmt.Sprintf(
			`<item id="c%d" href="%s" media-type="application/xhtml+xml"/>`, i, name))
		spine.WriteString(fmt.Sprintf(`<itemref idref="c%d"/>`, i))
		parts = append(parts, part{name: "OEBPS/" + name,
			data: "<html><body>" + strings.Repeat("<p>one</p>", paragraphs) + "</body></html>"})
	}
	all := append([]part{
		{name: "mimetype", data: "application/epub+zip", store: true},
		{name: containerPath, data: containerXML},
		{name: "OEBPS/content.opf", data: `<?xml version="1.0"?>
		<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
		  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
		    <dc:title>A Book</dc:title><dc:identifier id="pub-id">urn:uuid:1</dc:identifier>
		  </metadata>
		  <manifest>` + manifest.String() + `</manifest>
		  <spine>` + spine.String() + `</spine>
		</package>`},
	}, parts...)
	src := build(t, all...)

	// ⛔ Each chapter is inside the per-chapter ceiling — asserted, not assumed,
	// because if one chapter alone exceeded it the refusal below would prove
	// nothing about the book.
	one := build(t,
		part{name: "mimetype", data: "application/epub+zip", store: true},
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<?xml version="1.0"?>
		<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
		  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
		    <dc:title>A Book</dc:title><dc:identifier id="pub-id">urn:uuid:1</dc:identifier>
		  </metadata>
		  <manifest><item id="c0" href="text/c0.xhtml" media-type="application/xhtml+xml"/></manifest>
		  <spine><itemref idref="c0"/></spine>
		</package>`},
		part{name: "OEBPS/text/c0.xhtml",
			data: "<html><body>" + strings.Repeat("<p>one</p>", paragraphs) + "</body></html>"},
	)
	chapter, err := Parse(one, Limits{MaxBookNodes: 1 << 20})
	if err != nil {
		t.Fatalf("one chapter of %d paragraphs was refused on its own: %v", paragraphs, err)
	}
	perChapter := countNodes(chapter.Blocks)
	t.Logf("one chapter holds %d nodes; %d of them is %d", perChapter, chapters, chapters*perChapter)

	// The book, with a ceiling above one chapter and below the spine.
	ceiling := perChapter * chapters / 2
	_, err = Parse(src, Limits{MaxBookNodes: ceiling})
	if err == nil {
		t.Fatalf("a book of %d nodes passed a ceiling of %d", chapters*perChapter, ceiling)
	}
	if !strings.Contains(err.Error(), "the book holds more than") {
		t.Errorf("refused, but not by the book's ceiling: %v", err)
	}

	// And the control: the same book under a ceiling that fits it.
	if _, err := Parse(src, Limits{MaxBookNodes: chapters*perChapter + 1}); err != nil {
		t.Errorf("a book of %d nodes was refused by a ceiling of %d: %v",
			chapters*perChapter, chapters*perChapter+1, err)
	}
}
