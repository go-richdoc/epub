package epub

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
)

// nopCloser lets the fixture above register a "compressor" that compresses
// nothing, which is all it needs to write an entry nothing can read back.
type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// corrupt flips a byte inside one entry's compressed data, leaving every header
// and the central directory intact — so the archive opens and the entry does
// not.
//
// The local header is: signature(4) version(2) flags(2) method(2) time(4)
// crc(4) sizes(8) nameLen(2) extraLen(2), then the name, then the extra, then
// the data. Reading those two lengths is what makes this land in the data
// rather than somewhere that changes nothing.
func corrupt(t *testing.T, src []byte, name string) []byte {
	t.Helper()
	i := bytes.Index(src, append([]byte("PK\x03\x04"), make([]byte, 0)...))
	for i >= 0 && i+30 <= len(src) {
		nameLen := int(binary.LittleEndian.Uint16(src[i+26 : i+28]))
		extraLen := int(binary.LittleEndian.Uint16(src[i+28 : i+30]))
		got := string(src[i+30 : i+30+nameLen])
		if got == name {
			data := i + 30 + nameLen + extraLen
			if data+8 >= len(src) {
				t.Fatalf("%s has no data to corrupt", name)
			}
			out := bytes.Clone(src)
			out[data+4] ^= 0xff
			return out
		}
		next := bytes.Index(src[i+4:], []byte("PK\x03\x04"))
		if next < 0 {
			break
		}
		i += 4 + next
	}
	t.Fatalf("no local header for %s", name)
	return nil
}

// ⛔ Every one of these is a number the FILE chose, and each has to be refused
// with a message that names which one. A limit that refuses without saying
// which sends a reader to read the wrong code.
func TestEveryLimitRefusesAndSaysWhich(t *testing.T) {
	big := strings.Repeat("x", 4096)

	t.Run("too many entries", func(t *testing.T) {
		var parts []part
		for i := range 20 {
			parts = append(parts, part{name: fmt.Sprintf("f%d.txt", i), data: "x"})
		}
		_, err := Parse(build(t, parts...), Limits{MaxEntries: 10})
		if err == nil || !strings.Contains(err.Error(), "20 entries, over the 10") {
			t.Fatalf("error = %v, want the entry count", err)
		}
	})

	t.Run("one entry larger than its own ceiling", func(t *testing.T) {
		// ⛔ 1000 and not 100: the container and the package document are read
		// FIRST, and a ceiling below their size refuses the book before the
		// oversized chapter is reached — which is a correct refusal of the
		// wrong thing, and would have made this test pass for a reason it is
		// not about.
		_, err := Parse(book(t, part{name: "OEBPS/text/chapter2.xhtml", data: big}), Limits{MaxEntryBytes: 1000})
		if err == nil || !strings.Contains(err.Error(), "says it is 4096 bytes, over the 1000 limit") {
			t.Fatalf("error = %v, want the declared size and the per-entry limit", err)
		}
	})

	t.Run("the whole budget spent", func(t *testing.T) {
		// Two chapters of 4 KiB each, with room for about one.
		src := book(t,
			part{name: "OEBPS/text/chapter1.xhtml", data: "<html><body><p>" + big + "</p></body></html>"},
			part{name: "OEBPS/text/chapter2.xhtml", data: "<html><body><p>" + big + "</p></body></html>"})
		_, err := Parse(src, Limits{MaxTotalBytes: 5000})
		if err == nil || !strings.Contains(err.Error(), "5000 limit") {
			t.Fatalf("error = %v, want the total budget", err)
		}
	})

	t.Run("an entry that overruns what is left of the budget", func(t *testing.T) {
		// ⛔ The OTHER half of the same guard: the per-entry ceiling is high
		// enough, and what is left of the TOTAL is not. Reading stops mid-way
		// and says so, rather than returning a truncated chapter.
		src := book(t, part{name: "OEBPS/text/chapter1.xhtml", data: "<html><body><p>" + big + "</p></body></html>"})
		_, err := Parse(src, Limits{MaxEntryBytes: 1 << 20, MaxTotalBytes: 900})
		if err == nil || !strings.Contains(err.Error(), "900") {
			t.Fatalf("error = %v, want the total budget", err)
		}
	})

	t.Run("the budget exactly spent, with the next entry still to read", func(t *testing.T) {
		// ⛔ The third shape of the same guard, and the only one that reaches
		// "nothing left at all": the container fits EXACTLY, so the package
		// document finds a budget of zero rather than too little.
		_, err := Parse(book(t), Limits{MaxTotalBytes: int64(len(containerXML))})
		if err == nil || !strings.Contains(err.Error(), "already inflated") {
			t.Fatalf("error = %v, want the spent budget", err)
		}
	})

	t.Run("a chapter past the HTML reader's own limits", func(t *testing.T) {
		deep := strings.Repeat("<div>", 300) + "x" + strings.Repeat("</div>", 300)
		_, err := Parse(book(t, part{name: "OEBPS/text/chapter2.xhtml", data: deep}), Limits{MaxDepth: 10})
		if err == nil || !strings.Contains(err.Error(), "chapter2.xhtml") {
			t.Fatalf("error = %v, want the chapter named", err)
		}
		if !strings.Contains(err.Error(), "nested deeper than 10") {
			t.Errorf("error = %v, want the HTML reader's depth limit", err)
		}
	})
}

// ⛔ A ZIP can be broken in ways that are not about size, and each must come
// back as a refusal rather than as a panic or a silent half-book.
func TestABrokenArchive(t *testing.T) {
	t.Run("a compression method nothing here can open", func(t *testing.T) {
		// ⛔ Method 99 is the AE-x marker a DRM'd book carries, and
		// archive/zip refuses to WRITE one — so the fixture registers a
		// compressor for it and no decompressor. Writing then works and
		// reading fails exactly as it does on a real encrypted book.
		zip.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) {
			return nopCloser{w}, nil
		})
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: containerPath, Method: 99})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(containerXML)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = Parse(buf.Bytes(), Limits{})
		if err == nil || !strings.Contains(err.Error(), containerPath) {
			t.Fatalf("error = %v, want the entry named", err)
		}
	})

	t.Run("a deflate stream with a byte changed", func(t *testing.T) {
		// ⛔ The corruption goes into ONE entry's compressed data, found by
		// reading its local header rather than by guessing an offset: the
		// first version flipped a byte forty past the file NAME, which landed
		// in the header and left the archive perfectly readable — a test that
		// corrupted nothing and passed by accident would have been worse.
		src := book(t)
		broken := corrupt(t, src, "OEBPS/text/chapter1.xhtml")
		_, err := Parse(broken, Limits{})
		if err == nil {
			t.Fatal("a corrupted archive was read as a book")
		}
		if !strings.HasPrefix(err.Error(), "epub:") {
			t.Errorf("error = %v, want it to name this package", err)
		}
	})
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name, from, href, want, wantErr string
	}{
		{name: "beside the referrer", from: "OEBPS/text/c1.xhtml", href: "c2.xhtml", want: "OEBPS/text/c2.xhtml"},
		{name: "up and across", from: "OEBPS/text/c1.xhtml", href: "../images/x.png", want: "OEBPS/images/x.png"},
		{name: "an absolute path is from the container's root", from: "OEBPS/text/c1.xhtml", href: "/OEBPS/images/x.png", want: "OEBPS/images/x.png"},
		{name: "a fragment is dropped", from: "OEBPS/c.opf", href: "text/c1.xhtml#start", want: "OEBPS/text/c1.xhtml"},
		{name: "a query is dropped too", from: "OEBPS/c.opf", href: "text/c1.xhtml?v=2", want: "OEBPS/text/c1.xhtml"},
		{name: "nothing but a fragment", from: "OEBPS/c1.xhtml", href: "#part2", wantErr: "nothing but a fragment"},
		{name: "a scheme is not a path", from: "OEBPS/c1.xhtml", href: "https://example.com/x.png", wantErr: "not in the publication"},
		{name: "a mailto is not a path either", from: "OEBPS/c1.xhtml", href: "mailto:a@b.c", wantErr: "not in the publication"},
		{name: "climbing out is refused", from: "OEBPS/text/c1.xhtml", href: "../../../etc/passwd", wantErr: "climbs out"},
		{name: "climbing out exactly to the top", from: "c1.xhtml", href: "..", wantErr: "climbs out"},
		// ⛔ A single-letter "drive" is a SCHEME by the URL grammar, and it is
		// refused as one. That is the right answer whichever way the producer
		// meant it: there is no "C:" directory in a publication either.
		{name: "a windows-style drive is a scheme", from: "OEBPS/c.opf", href: "C:/x.png", wantErr: "not in the publication"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.from, tc.href)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolve(%q, %q) = %q, want %q", tc.from, tc.href, got, tc.want)
			}
		})
	}
}

func TestMediaTypeComesFromTheName(t *testing.T) {
	for name, want := range map[string]string{
		"a/b/cover.png":   "image/png",
		"a/b/cover.jpeg":  "image/jpeg",
		"a/b/cover.svg":   "image/svg+xml",
		"a/b/what.xyzzy":  "application/octet-stream",
		"a/b/noextension": "application/octet-stream",
	} {
		if got := mediaTypeOf(name); got != want {
			t.Errorf("mediaTypeOf(%q) = %q, want %q", name, got, want)
		}
	}
	// ⛔ A type with parameters loses them: "text/html; charset=utf-8" in a
	// data URL is a header a renderer has to re-parse, and the charset of a
	// picture means nothing.
	if got := mediaTypeOf("a/page.html"); strings.Contains(got, ";") {
		t.Errorf("mediaTypeOf = %q, want no parameters", got)
	}
}

// ⛔ The rewrite has a case per container, and a case it forgets is invisible:
// the href simply stays. Blocks that cannot hold an image go through the
// default, and this is what says the default does not drop them.
func TestBlocksThatCannotHoldAnImageSurvive(t *testing.T) {
	doc := parse(t, book(t, part{name: "OEBPS/text/chapter2.xhtml", data: `<html><body>
	  <pre>code survives</pre><hr/><svg><rect/></svg>
	  <p><strong><img src="../images/cover.png" alt="in strong"/></strong>
	     <del><img src="../images/cover.png" alt="in del"/></del>
	     <a id="x"><img src="../images/cover.png" alt="in an anchor"/></a></p>
	</body></html>`}))
	var code, breaks, raws, inlined int
	richdoc.Walk(doc, visitor(func(n any) {
		switch v := n.(type) {
		case richdoc.CodeBlock:
			code++
		case richdoc.ThematicBreak:
			breaks++
		case richdoc.RawBlock:
			raws++
		case richdoc.Image:
			if strings.HasPrefix(v.URL, "data:") {
				inlined++
			}
		}
	}))
	if code != 1 || breaks != 1 || raws != 1 {
		t.Errorf("code=%d breaks=%d raw=%d, want one of each kept", code, breaks, raws)
	}
	if inlined != 4 {
		t.Errorf("inlined = %d, want chapter one's and the three in strong, del and an anchor", inlined)
	}
}

func TestTheMetadataAnEpubWritesTwoWays(t *testing.T) {
	src := build(t,
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<package><metadata>
		  <title></title><title>  The Real Title  </title>
		  <meta name="calibre:series" content="A Series"/>
		  <meta property="dcterms:modified">2026-10-17T00:00:00Z</meta>
		</metadata><manifest>
		  <item id="c" href="c.xhtml" media-type="application/xhtml+xml"/>
		</manifest><spine><itemref idref="c"/></spine></package>`},
		part{name: "OEBPS/c.xhtml", data: "<html><body><p>words</p></body></html>"})
	doc := parse(t, src)
	for k, v := range map[string]string{
		"title":  "The Real Title",
		"series": "A Series",
		"date":   "2026-10-17T00:00:00Z",
	} {
		if doc.Meta[k] != v {
			t.Errorf("Meta[%q] = %q, want %q", k, doc.Meta[k], v)
		}
	}
	if _, ok := doc.Meta["author"]; ok {
		t.Errorf("Meta has an author the book never named: %q", doc.Meta["author"])
	}
}

func TestAPublicationThatSaysNothingAboutItself(t *testing.T) {
	src := build(t,
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<package><manifest>
		  <item id="c" href="c.xhtml" media-type="application/xhtml+xml"/>
		</manifest><spine><itemref idref="c"/></spine></package>`},
		part{name: "OEBPS/c.xhtml", data: "<html><body><p>words</p></body></html>"})
	doc := parse(t, src)
	if doc.Meta != nil {
		t.Errorf("Meta = %v, want nil rather than an empty map", doc.Meta)
	}
}

func TestAManifestHrefThatClimbsOutIsRefused(t *testing.T) {
	src := build(t,
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<package><manifest>
		  <item id="c" href="../../../etc/passwd" media-type="application/xhtml+xml"/>
		</manifest><spine><itemref idref="c"/></spine></package>`})
	_, err := Parse(src, Limits{})
	if err == nil || !strings.Contains(err.Error(), "climbs out") {
		t.Fatalf("error = %v, want the refusal to name what it is", err)
	}
}

// ⛔ linear="no" is KEPT, in spine position. The specification leaves the
// choice to the reading system, and losing a chapter is worse than carrying an
// appendix.
func TestAnAuxiliaryItemIsKept(t *testing.T) {
	src := build(t,
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<package><manifest>
		  <item id="a" href="a.xhtml" media-type="application/xhtml+xml"/>
		  <item id="b" href="b.xhtml" media-type="application/xhtml+xml"/>
		</manifest><spine>
		  <itemref idref="a"/><itemref idref="b" linear="no"/>
		</spine></package>`},
		part{name: "OEBPS/a.xhtml", data: "<html><body><p>the chapter</p></body></html>"},
		part{name: "OEBPS/b.xhtml", data: "<html><body><p>the answer key</p></body></html>"})
	all := richdoc.PlainText(parse(t, src))
	for _, want := range []string{"the chapter", "the answer key"} {
		if !strings.Contains(all, want) {
			t.Errorf("the document does not say %q:\n%s", want, all)
		}
	}
}

// A spine item whose media type says it is not a content document is skipped,
// and a chapter with no media-type at all is read: the attribute is required
// and producers leave it out anyway.
func TestWhatCountsAsAContentDocument(t *testing.T) {
	src := build(t,
		part{name: containerPath, data: containerXML},
		part{name: "OEBPS/content.opf", data: `<package><manifest>
		  <item id="css" href="s.css" media-type="text/css"/>
		  <item id="plain" href="p.xhtml"/>
		  <item id="html" href="h.html" media-type="text/html"/>
		</manifest><spine>
		  <itemref idref="css"/><itemref idref="plain"/><itemref idref="html"/>
		</spine></package>`},
		part{name: "OEBPS/s.css", data: "body { color: red }"},
		part{name: "OEBPS/p.xhtml", data: "<html><body><p>no media type</p></body></html>"},
		part{name: "OEBPS/h.html", data: "<html><body><p>text/html</p></body></html>"})
	all := richdoc.PlainText(parse(t, src))
	if strings.Contains(all, "color") {
		t.Errorf("a stylesheet in the spine was read as a chapter:\n%s", all)
	}
	for _, want := range []string{"no media type", "text/html"} {
		if !strings.Contains(all, want) {
			t.Errorf("the document does not say %q", want)
		}
	}
}
