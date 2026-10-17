package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
)

// a part is one entry of a publication a test builds.
type part struct {
	name  string
	data  string
	store bool // written uncompressed, as the mimetype entry must be
}

// build writes a ZIP, which is all an EPUB's container is.
func build(t *testing.T, parts ...part) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		method := zip.Deflate
		if p.store {
			method = zip.Store
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const containerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`

// book is a small, complete, ordinary EPUB 3: the shape nearly every book has.
func book(t *testing.T, extra ...part) []byte {
	t.Helper()
	parts := []part{
		{name: "mimetype", data: "application/epub+zip", store: true},
		{name: containerPath, data: containerXML},
		{name: "OEBPS/content.opf", data: `<?xml version="1.0"?>
		<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
		  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
		    <dc:title>A Book</dc:title>
		    <dc:creator>Ada Lovelace</dc:creator>
		    <dc:language>en</dc:language>
		    <dc:date>1843</dc:date>
		    <dc:publisher>Taylor</dc:publisher>
		    <dc:identifier id="pub-id">urn:uuid:1</dc:identifier>
		  </metadata>
		  <manifest>
		    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
		    <item id="c1" href="text/chapter1.xhtml" media-type="application/xhtml+xml"/>
		    <item id="c2" href="text/chapter2.xhtml" media-type="application/xhtml+xml"/>
		    <item id="cover" href="images/cover.png" media-type="image/png"/>
		  </manifest>
		  <spine><itemref idref="c1"/><itemref idref="c2"/></spine>
		</package>`},
		{name: "OEBPS/nav.xhtml", data: `<html><body><nav><ol><li><a href="text/chapter1.xhtml">One</a></li></ol></nav></body></html>`},
		{name: "OEBPS/text/chapter1.xhtml", data: `<?xml version="1.0" encoding="utf-8"?>
		<html xmlns="http://www.w3.org/1999/xhtml"><head><title>One</title></head>
		<body><h1>Chapter One</h1><p>The first words.</p>
		<p><img src="../images/cover.png" alt="The cover"/></p></body></html>`},
		{name: "OEBPS/text/chapter2.xhtml", data: `<html><body><h1>Chapter Two</h1><p>The second words.</p></body></html>`},
		{name: "OEBPS/images/cover.png", data: "\x89PNG\r\n\x1a\nnot really a png"},
	}
	return build(t, append(parts, extra...)...)
}

func parse(t *testing.T, src []byte) *richdoc.Document {
	t.Helper()
	doc, err := Parse(src, Limits{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func TestAnOrdinaryBook(t *testing.T) {
	doc := parse(t, book(t))

	// The reading order is the spine's, and the nav document -- which is in
	// the manifest and NOT in the spine -- is not content.
	var headings []string
	for _, b := range doc.Blocks {
		if h, ok := b.(richdoc.Heading); ok {
			headings = append(headings, text(h.Inlines))
		}
	}
	want := []string{"Chapter One", "Chapter Two"}
	if fmt.Sprint(headings) != fmt.Sprint(want) {
		t.Errorf("headings = %v, want %v", headings, want)
	}
	all := richdoc.PlainText(doc)
	if strings.Contains(all, "One</a>") || strings.Contains(all, "nav") {
		t.Errorf("the navigation document was read as content:\n%s", all)
	}

	// The metadata every reader shows.
	for k, v := range map[string]string{
		"title": "A Book", "author": "Ada Lovelace", "language": "en",
		"date": "1843", "publisher": "Taylor", "identifier": "urn:uuid:1",
	} {
		if doc.Meta[k] != v {
			t.Errorf("Meta[%q] = %q, want %q", k, doc.Meta[k], v)
		}
	}
}

// ⛔ The decision that makes the output self-contained: once the ZIP is closed,
// "../images/cover.png" is a name nothing can resolve.
func TestAnImageBecomesItsBytes(t *testing.T) {
	doc := parse(t, book(t))
	var urls []string
	richdoc.Walk(doc, visitor(func(n any) {
		if img, ok := n.(richdoc.Image); ok {
			urls = append(urls, img.URL)
		}
	}))
	if len(urls) != 1 {
		t.Fatalf("images = %d, want the one the chapter refers to", len(urls))
	}
	if !strings.HasPrefix(urls[0], "data:image/png;base64,") {
		t.Errorf("image URL = %.40q, want a png data URL", urls[0])
	}
	if !strings.Contains(urls[0], "iVBORw0K") {
		t.Errorf("image URL does not carry the entry's bytes: %.60q", urls[0])
	}
}

// ⛔ Every container a rewrite can forget. An image inside a table cell inside
// a list item is the shape that catches a mapBlocks missing a case, and a
// missed case keeps an href nothing can resolve.
func TestAnImageIsInlinedWhereverItSits(t *testing.T) {
	deep := part{name: "OEBPS/text/chapter2.xhtml", data: `<html><body>
	  <ul><li><table><caption><img src="../images/cover.png" alt="in a caption"/></caption>
	    <tr><td><blockquote><p><a href="#x"><em><img src="../images/cover.png" alt="deep"/></em></a></p></blockquote></td></tr>
	  </table></li></ul>
	  <h2><img src="../images/cover.png" alt="in a heading"/></h2>
	</body></html>`}
	doc := parse(t, book(t, deep))
	var inlined, raw int
	richdoc.Walk(doc, visitor(func(n any) {
		if img, ok := n.(richdoc.Image); ok {
			if strings.HasPrefix(img.URL, "data:") {
				inlined++
			} else {
				raw++
			}
		}
	}))
	if raw != 0 {
		t.Errorf("%d image(s) kept an unresolvable href: a container is missing from the rewrite", raw)
	}
	if inlined != 4 {
		t.Errorf("inlined = %d, want 4 (chapter one's, a caption, a cell, a heading)", inlined)
	}
}

func TestAnImageThatCannotBeReadKeepsItsHref(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a picture the book does not hold", `<p><img src="../images/missing.png" alt="x"/></p>`},
		{"a reference that climbs out", `<p><img src="../../../etc/passwd" alt="x"/></p>`},
		{"an absolute URL", `<p><img src="https://example.com/x.png" alt="x"/></p>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := parse(t, book(t, part{
				name: "OEBPS/text/chapter2.xhtml",
				data: "<html><body>" + tc.src + "</body></html>",
			}))
			var kept int
			richdoc.Walk(doc, visitor(func(n any) {
				if img, ok := n.(richdoc.Image); ok && !strings.HasPrefix(img.URL, "data:") {
					kept++
				}
			}))
			if kept != 1 {
				t.Errorf("kept hrefs = %d, want the one that could not be read", kept)
			}
			// And the book is still a book: losing a picture must not lose it.
			if len(doc.Blocks) == 0 {
				t.Error("the publication came back empty")
			}
		})
	}
}

// ⛔ A data URL already in the chapter is left alone rather than resolved as a
// path -- and an empty src never reaches here, but the guard is what says so.
func TestADataURLIsLeftAlone(t *testing.T) {
	doc := parse(t, book(t, part{
		name: "OEBPS/text/chapter2.xhtml",
		data: `<html><body><p><img src="data:image/gif;base64,R0lGOD" alt="x"/></p></body></html>`,
	}))
	var seen string
	richdoc.Walk(doc, visitor(func(n any) {
		if img, ok := n.(richdoc.Image); ok && img.Alt == "x" {
			seen = img.URL
		}
	}))
	if seen != "data:image/gif;base64,R0lGOD" {
		t.Errorf("URL = %q, want it untouched", seen)
	}
}

// ⛔ The same picture referred to twice is inflated ONCE. A book's chapter
// footer carries the same logo on every page, and a reader that inflated it
// per reference would spend its whole budget on one image.
func TestTheSamePictureIsReadOnce(t *testing.T) {
	src := book(t, part{
		name: "OEBPS/text/chapter2.xhtml",
		data: `<html><body>` + strings.Repeat(`<p><img src="../images/cover.png" alt="logo"/></p>`, 50) + `</body></html>`,
	})
	// 50 references to a 23-byte entry: under a budget of 200 bytes they fit
	// only if the entry is read once.
	if _, err := Parse(src, Limits{MaxTotalBytes: 4000}); err != nil {
		t.Errorf("Parse: %v -- the same entry was inflated per reference", err)
	}
}

func TestWhatItRefuses(t *testing.T) {
	for _, tc := range []struct {
		name, says string
		src        []byte
	}{
		{
			name: "not a ZIP at all", says: "not a ZIP",
			src: []byte("I am a plain text file pretending"),
		},
		{
			name: "no container", says: "META-INF/container.xml is not in",
			src: build(t, part{name: "mimetype", data: "application/epub+zip", store: true}),
		},
		{
			name: "a container that does not parse", says: "does not parse",
			src: build(t, part{name: containerPath, data: "<container"}),
		},
		{
			name: "a container that names nothing", says: "names no package document",
			src: build(t, part{name: containerPath, data: `<container><rootfiles></rootfiles></container>`}),
		},
		{
			name: "a package document that is not there", says: "OEBPS/content.opf is not in",
			src: build(t, part{name: containerPath, data: containerXML}),
		},
		{
			name: "a package document that does not parse", says: "does not parse",
			src: build(t,
				part{name: containerPath, data: containerXML},
				part{name: "OEBPS/content.opf", data: "<package"}),
		},
		{
			name: "a spine with nothing readable in it", says: "names no content document",
			src: build(t,
				part{name: containerPath, data: containerXML},
				part{name: "OEBPS/content.opf", data: `<package><manifest>
				  <item id="i" href="x.png" media-type="image/png"/>
				</manifest><spine><itemref idref="i"/><itemref idref="absent"/></spine></package>`}),
		},
		{
			name: "a chapter the spine names and the book does not hold", says: "chapter9.xhtml is not in",
			src: build(t,
				part{name: containerPath, data: containerXML},
				part{name: "OEBPS/content.opf", data: `<package><manifest>
				  <item id="c" href="chapter9.xhtml" media-type="application/xhtml+xml"/>
				</manifest><spine><itemref idref="c"/></spine></package>`}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src, Limits{})
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want it to say %q", err, tc.says)
			}
			if !strings.HasPrefix(err.Error(), "epub:") {
				t.Errorf("error = %v, want it to name this package", err)
			}
		})
	}
}

// visitor adapts a function to richdoc.Visitor.
type visitor func(any)

func (v visitor) Enter(n any) bool { v(n); return true }
func (v visitor) Leave(any)        {}

// text renders inline content as plain words.
func text(in []richdoc.Inline) string {
	return strings.TrimSpace(richdoc.PlainText(&richdoc.Document{
		Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: in}},
	}))
}
