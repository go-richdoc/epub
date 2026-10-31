package epub

import (
	"encoding/base64"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/go-richdoc/html"
	"github.com/go-richdoc/richdoc"
)

// Parse reads an EPUB publication into a document.
//
// The whole of the format is three reads and a loop: the container names the
// package document, the package document's manifest says what is in the book,
// its spine says in what order, and each content document goes through the
// HTML reader.
func Parse(src []byte, lim Limits) (*richdoc.Document, error) {
	lim = lim.orDefaults()
	a, err := openArchive(src, lim)
	if err != nil {
		return nil, err
	}
	opfName, err := rootfile(a)
	if err != nil {
		return nil, err
	}
	p, err := readOPF(a, opfName)
	if err != nil {
		return nil, err
	}
	order, err := p.readingOrder(opfName)
	if err != nil {
		return nil, err
	}

	out := &richdoc.Document{Meta: p.meta()}
	// ⛔ The book's own node budget, which MaxNodes is not: that one is handed
	// fresh to every chapter. See Limits.MaxBookNodes.
	spent := 0
	for _, name := range order {
		b, err := a.read(name)
		if err != nil {
			// ⛔ A chapter the spine names and the ZIP does not hold is the
			// book's defect and not ours, and it is REPORTED: a silently
			// shorter book is the one failure a reader cannot notice. The
			// error names the chapter.
			return nil, err
		}
		chapter, err := html.Parse(b, html.Limits{MaxDepth: lim.MaxDepth, MaxNodes: lim.MaxNodes})
		if err != nil {
			return nil, fmt.Errorf("epub: %s: %w", name, err)
		}
		blocks := inlineImages(a, name, chapter.Blocks)
		spent += countNodes(blocks)
		if spent > lim.MaxBookNodes {
			return nil, fmt.Errorf("epub: %s: the book holds more than %d nodes",
				name, lim.MaxBookNodes)
		}
		out.Blocks = append(out.Blocks, blocks...)
	}
	return out, nil
}

// countNodes is how many blocks and inlines a slice of blocks holds, counting
// the ones nested inside lists, quotes, table cells and footnotes.
//
// ⛔ EVERY CONTAINER, or the count is a ceiling somebody routes around. A
// thousand nested lists holding a thousand paragraphs each is two nodes to a
// counter that does not recurse, and that is exactly the document a node
// ceiling exists for.
func countNodes(bs []richdoc.Block) int {
	n := 0
	var blocks func([]richdoc.Block)
	var inlines func([]richdoc.Inline)

	inlines = func(xs []richdoc.Inline) {
		for _, x := range xs {
			n++
			switch v := x.(type) {
			case richdoc.Link:
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
			n++
			switch v := b.(type) {
			case richdoc.Paragraph:
				inlines(v.Inlines)
			case richdoc.Heading:
				inlines(v.Inlines)
			case richdoc.BlockQuote:
				blocks(v.Blocks)
			// ⛔ AN ITEM AND A CELL ARE COUNTED THEMSELVES, not only their
			// contents. An EMPTY one holds nothing to recurse into, so a table
			// of a million empty <td> was ONE node to the first draft of this
			// counter — a ceiling routed around by the cheapest markup there
			// is. Caught by this function's own test disagreeing with the
			// number I had worked out by hand for it.
			case richdoc.List:
				for _, it := range v.Items {
					n++
					blocks(it.Blocks)
				}
			case richdoc.Table:
				for _, row := range append([][]richdoc.Cell{v.Header}, v.Rows...) {
					for _, c := range row {
						n++
						inlines(c.Inlines)
						blocks(c.Blocks)
					}
				}
			}
		}
	}
	blocks(bs)
	return n
}

// inlineImages resolves every image in a chapter against the chapter's own
// location and replaces its href with the bytes it names.
//
// ⛔ An image that cannot be read keeps its href, and the chapter is kept:
// losing a picture must not lose a book, which is the opposite of the rule for
// a missing CHAPTER above. The two are different because a href a reader
// cannot resolve is still a name a person can look up, while a chapter that is
// not there is content nothing can recover.
func inlineImages(a *archive, from string, blocks []richdoc.Block) []richdoc.Block {
	cache := map[string]string{}
	return mapBlocks(blocks, func(img richdoc.Image) richdoc.Image {
		if url, ok := cache[img.URL]; ok {
			img.URL = url
			return img
		}
		// ⛔ ONE path out, and no case of its own for a data URL or an empty
		// src: resolve refuses both — a "data:" is a scheme and an empty
		// reference is nothing — and an image that cannot be resolved keeps
		// its URL, which for those two is already the right answer. The
		// mutation sweep is what collapsed this: the guards it had were three
		// where one does the work, and deleting any of them changed nothing
		// any test could see.
		if name, err := resolve(from, img.URL); err == nil {
			if b, err := a.read(name); err == nil {
				url := "data:" + mediaTypeOf(name) + ";base64," + base64.StdEncoding.EncodeToString(b)
				cache[img.URL] = url
				img.URL = url
			}
		}
		return img
	})
}

// mediaTypeOf is an entry's type, from its extension.
//
// ⛔ Read from the NAME and not from the manifest, deliberately: an image
// reached through a chapter's src may not be in the manifest at all (producers
// forget), and mime.TypeByExtension knows every type a book's pictures come
// in. A type nothing recognises becomes application/octet-stream, which a
// renderer will sniff rather than refuse.
func mediaTypeOf(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		if i := strings.Index(t, ";"); i > 0 {
			t = t[:i]
		}
		return t
	}
	return "application/octet-stream"
}

// mapBlocks rebuilds a tree with every image passed through f.
//
// ⛔ It exists because richdoc's Walk OBSERVES and cannot rewrite: nodes are
// passed as values, so a visitor that edits one edits a copy. A rewrite of the
// whole tree is therefore written out by hand, every container included — and
// a container this function forgot would silently keep the un-inlined href,
// which is why there is a test asserting an image inside a table cell inside a
// list item comes out inlined.
//
// This is the second piece of this shape in the fleet and a candidate for
// richdoc itself; it stays here until there is a second CALLER rather than a
// second author.
func mapBlocks(blocks []richdoc.Block, f func(richdoc.Image) richdoc.Image) []richdoc.Block {
	out := make([]richdoc.Block, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case richdoc.Paragraph:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.Heading:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.BlockQuote:
			v.Blocks = mapBlocks(v.Blocks, f)
			out = append(out, v)
		case richdoc.List:
			items := make([]richdoc.ListItem, 0, len(v.Items))
			for _, it := range v.Items {
				it.Blocks = mapBlocks(it.Blocks, f)
				items = append(items, it)
			}
			v.Items = items
			out = append(out, v)
		case richdoc.Table:
			v.Caption = mapInlines(v.Caption, f)
			v.Header = mapCells(v.Header, f)
			rows := make([][]richdoc.Cell, 0, len(v.Rows))
			for _, r := range v.Rows {
				rows = append(rows, mapCells(r, f))
			}
			v.Rows = rows
			out = append(out, v)
		default:
			// A code block, a thematic break, raw content, a math block: no
			// image can be inside one.
			out = append(out, b)
		}
	}
	return out
}

func mapCells(cells []richdoc.Cell, f func(richdoc.Image) richdoc.Image) []richdoc.Cell {
	out := make([]richdoc.Cell, 0, len(cells))
	for _, c := range cells {
		c.Inlines = mapInlines(c.Inlines, f)
		c.Blocks = mapBlocks(c.Blocks, f)
		out = append(out, c)
	}
	return out
}

func mapInlines(in []richdoc.Inline, f func(richdoc.Image) richdoc.Image) []richdoc.Inline {
	out := make([]richdoc.Inline, 0, len(in))
	for _, i := range in {
		switch v := i.(type) {
		case richdoc.Image:
			out = append(out, f(v))
		case richdoc.Link:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.Emph:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.Strong:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.Strikethrough:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		case richdoc.Anchor:
			v.Inlines = mapInlines(v.Inlines, f)
			out = append(out, v)
		default:
			// Text, code, a line break, raw content, maths: nothing nested.
			out = append(out, i)
		}
	}
	return out
}
