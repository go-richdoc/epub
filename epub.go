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
		out.Blocks = append(out.Blocks, blocks...)
	}
	return out, nil
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
