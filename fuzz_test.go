// Copyright (c) the go-richdoc authors.
// SPDX-License-Identifier: BSD-3-Clause

package epub

import (
	"fmt"
	"testing"

	"github.com/go-richdoc/richdoc"
)

// FuzzParse points this reader at bytes nobody wrote, which is the only kind
// it will ever meet: an EPUB arrives from a download, an upload or a mail
// attachment, and it is a ZIP holding XML holding XHTML — three parsers deep,
// each with its own history of being the way in.
//
// Nothing in this org had a fuzz target before v0.13.0 of its sibling, and
// this is the second. It asserts four things:
//
//   - Parse does not panic, whatever the bytes are.
//   - its two results are exclusive: a document OR an error, never neither.
//     A caller reads one of them, and "nil, nil" is the shape that gets
//     dereferenced.
//   - Parse is DETERMINISTIC. A ZIP is a map from names to entries and this
//     reader keeps one; a single range over such a map reaching the output
//     would make a book's text depend on nothing the book said.
//   - THE LIMITS HOLD. This is the one worth the target: Limits declares what
//     the reader will inflate, and a declaration is not a measurement. The
//     assertion is on the DOCUMENT's own size, which is where an accounting
//     mistake would land -- a.spent counting an entry once instead of on
//     every read, say, which would let a spine of a thousand chapters each
//     naming the same image multiply a small archive without limit.
//
// The bound allows for what the reader legitimately adds: an image's bytes
// become a base64 data URL, which is 4/3 of them plus a "data:<type>;base64,"
// prefix, and there can be no more images than the archive has entries.
//
// ⚠ What this target does NOT reach, stated because the numbers look better
// than they are: a ZIP is a checksummed container, so most mutations are
// refused by archive/zip before any of this package runs. 1.8 million
// executions produced 32 interesting inputs, where the same budget against a
// plain-text parser produces hundreds. The deep paths -- the OPF parse, the
// XHTML parse -- are reached far more cheaply by fuzzing those readers
// DIRECTLY, which is their own packages' work (go-richdoc/html is the one this
// package hands every chapter to). This target covers the container and the
// accounting; it is not a substitute for theirs.
func FuzzParse(f *testing.F) {
	// Real publications first: the fuzzer needs a valid container to reach
	// past zip.NewReader, and these are the shapes the suite already builds.
	f.Add(book(f))
	f.Add(book(f, part{name: "OEBPS/text/chapter3.xhtml", data: "<html><body><p>three</p></body></html>"}))
	f.Add(book(f, part{name: "OEBPS/images/extra.png", data: "\x89PNG\r\n\x1a\nnot really a png"}))
	// A chapter whose image escapes its own directory, which must resolve to
	// nothing rather than to a file outside the publication.
	f.Add(book(f, part{name: "OEBPS/text/chapter4.xhtml",
		data: `<html><body><p><img src="../../../../etc/passwd"/></p></body></html>`}))
	// Deeply nested markup, which is what MaxDepth is for.
	deep := ""
	for i := 0; i < 200; i++ {
		deep += "<div>"
	}
	f.Add(book(f, part{name: "OEBPS/text/deep.xhtml", data: "<html><body>" + deep + "x</body></html>"}))
	// And the degenerate containers.
	f.Add(build(f))
	f.Add(build(f, part{name: "mimetype", data: "application/epub+zip", store: true}))
	f.Add([]byte("PK\x03\x04 truncated"))
	f.Add([]byte("not a zip at all"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, src []byte) {
		// Small limits on purpose: a bound is only tested near its edge, and a
		// fuzzer that has to build a 64 MiB archive to reach one never will.
		lim := Limits{
			MaxEntries:    64,
			MaxEntryBytes: 1 << 16,
			MaxTotalBytes: 1 << 16,
			MaxDepth:      64,
			MaxNodes:      4096,
		}
		doc, err := Parse(src, lim)
		if err != nil {
			if doc != nil {
				t.Fatalf("both a document and an error: %v", err)
			}
			return
		}
		if doc == nil {
			t.Fatal("neither a document nor an error")
		}

		// Deterministic.
		again, err2 := Parse(src, lim)
		if err2 != nil || again == nil {
			t.Fatalf("the second Parse disagreed with the first: doc=%v err=%v", again != nil, err2)
		}
		if first, second := dump(doc), dump(again); first != second {
			t.Fatalf("two parses of the same bytes differ\nfirst:\n%.400s\nsecond:\n%.400s", first, second)
		}

		// The limits hold, measured on the document rather than trusted.
		if n, ceiling := contentBytes(doc), 4*lim.MaxTotalBytes+256*int64(lim.MaxEntries); n > ceiling {
			t.Fatalf("the document carries %d bytes, over the %d the limits allow "+
				"(MaxTotalBytes=%d): an archive of %d bytes produced it",
				n, ceiling, lim.MaxTotalBytes, len(src))
		}
	})
}

// dump is a deep, order-stable rendering of a document: fmt sorts map keys, so
// Meta cannot make two equal documents print differently.
func dump(d *richdoc.Document) string { return fmt.Sprintf("%#v", d) }

// contentBytes is every byte the document carries that came out of the
// archive: its text, and the image URLs, which hold whole files base64-encoded
// inline.
func contentBytes(d *richdoc.Document) int64 {
	n := int64(len(richdoc.PlainText(d)))
	for _, v := range d.Meta {
		n += int64(len(v))
	}
	var blocks func([]richdoc.Block)
	var inlines func([]richdoc.Inline)
	inlines = func(xs []richdoc.Inline) {
		for _, x := range xs {
			switch v := x.(type) {
			case richdoc.Image:
				n += int64(len(v.URL)) + int64(len(v.Alt))
			case richdoc.Link:
				n += int64(len(v.URL))
				inlines(v.Inlines)
			case richdoc.Emph:
				inlines(v.Inlines)
			case richdoc.Strong:
				inlines(v.Inlines)
			case richdoc.Anchor:
				inlines(v.Inlines)
			case richdoc.Footnote:
				blocks(v.Blocks)
			}
		}
	}
	blocks = func(bs []richdoc.Block) {
		for _, b := range bs {
			switch v := b.(type) {
			case richdoc.Paragraph:
				inlines(v.Inlines)
			case richdoc.Heading:
				inlines(v.Inlines)
			case richdoc.BlockQuote:
				blocks(v.Blocks)
			case richdoc.List:
				for _, it := range v.Items {
					blocks(it.Blocks)
				}
			case richdoc.Table:
				for _, row := range append([][]richdoc.Cell{v.Header}, v.Rows...) {
					for _, c := range row {
						inlines(c.Inlines)
						blocks(c.Blocks)
					}
				}
			case richdoc.CodeBlock:
				n += int64(len(v.Text))
			case richdoc.RawBlock:
				n += int64(len(v.Text))
			}
		}
	}
	blocks(d.Blocks)
	return n
}
