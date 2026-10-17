# epub

**EPUB 2 and EPUB 3 into [richdoc](https://github.com/go-richdoc/richdoc).**
Pure Go, CGO-free, including `GOOS=js`.

```go
doc, err := epub.Parse(src, epub.Limits{})
```

An EPUB is three things in a trench coat: a **ZIP**, a **manifest** and an
**order**. None of them is the content — the content is XHTML, which
[`go-richdoc/html`](https://github.com/go-richdoc/html) reads — so this package
is the three layers above it.

| | |
|---|---|
| `META-INF/container.xml` | the one path in an EPUB that is fixed. It names the package document |
| the package document | the **manifest** (what is in the book) and the **spine** (what order to read it in) |
| the spine | the reading order, and the only thing that says it |

## ⛔ Nothing here is found by its path

The same rule OPC taught [go-ooxml](https://github.com/go-ooxml/opc), for the
same reason: a reader that guessed `OEBPS/content.opf` would work on most books
and on nothing else. The container names the package document, the package
document names its content, and the spine says in which order — each step read,
never assumed.

## ⛔ The reading order is the spine, not the ZIP

A ZIP has no order worth trusting, and an alphabetical walk of its entries puts
chapter 10 before chapter 2 — the defect every naive reader of a container
format has. Only the spine says what comes after what.

An item marked `linear="no"` is *auxiliary* in EPUB's terms: an answer key, a
pop-up footnote, an advertisement. It is **kept**, in spine position, because
losing a chapter is worse than carrying an appendix, and the specification
leaves the choice to the reading system.

The navigation document and the NCX are **not** read as content. A richdoc
document has headings, which is what a table of contents is made from; a second
list of the same links would be a page of duplicates. A nav document the author
put *in* the spine is content like any other and is read there.

## An image becomes its bytes

Images are resolved against the chapter that referred to them and inlined as
data URLs — the same decision [`fb2`](https://github.com/go-richdoc/fb2) makes,
for the same reason: once the ZIP is closed, `images/cover.jpg` is a name
nothing can resolve. The same picture referred to fifty times is inflated
**once**.

⛔ An image that cannot be read **keeps its href**, and the chapter is kept; a
chapter the spine names and the ZIP does not hold is an **error**. The two
differ because a href a reader cannot resolve is still a name a person can look
up, while a missing chapter is content nothing can recover — and a silently
shorter book is the one failure nobody notices.

## ⛔ The limits are the security surface

```go
epub.Limits{MaxEntries: 8192, MaxEntryBytes: 64 << 20, MaxTotalBytes: 384 << 20}
```

Every one of these bounds an allocation decided by a number **in the file**. A
ZIP's central directory costs about forty-six bytes an entry and declares how
large each entry will be once inflated: it costs the author nothing to claim
four gigabytes, and a reader that believes it allocates them.

Entries are read **on demand**, not all at once: a book is mostly pictures, and
a conversion may want none of them. An entry is refused from its **declared**
size before a byte is inflated, then bounded again while reading, in case the
declaration was a lie the other way.

A reference that climbs out of the container is refused rather than cleaned
into something harmless; a reference with a scheme is refused as a URL; a
reference to nothing but a fragment is refused rather than resolved to the
chapter that made it.

## Measured on real books, which are not in this repository

⛔ **There is no book in `testdata`, deliberately.** The population this was
measured against is the machine owner's own library — copyrighted, personal,
and nobody's business in a public repository. The fixtures here are built by
hand; the real corpus is read from wherever the person running the test keeps
theirs:

```sh
EPUB_CORPUS=~/Books go test -run TestARealCorpus -v ./...
```

Without the variable the test **skips and says so**, because a test that passes
by doing nothing is worse than one that is not there.

What twelve real books said, on 2026-10-17:

```text
12 book(s): 11 read, 1 refused
7 480 to 377 683 words each; 0 to 127 headings; 2 to 52 images
REFUSED  Mastering Ext JS     epub: not a ZIP: zip: not a valid zip file
```

The refusal is **the file's**, not the reader's: that EPUB has no
end-of-central-directory record — it is truncated — and `unzip` refuses it in
the same breath. A reader that accepted it would hand back a partial book and
call it a book.

Two of the eleven have almost no headings (0 and 2): those publishers style
their chapter openers as paragraphs rather than as `h1`. That is the book's
markup, faithfully carried.

## How it is checked

- 100% of statements, **no uncovered block**, race detector on, `gofmt` and
  `go vet` clean, ten build targets including `js/wasm`.
- Every limit has a test that refuses **and names which limit**, including the
  three shapes of the budget guard: one entry too large, an entry that overruns
  what is left, and a budget exactly spent.
- A corrupted archive — one byte flipped inside a chapter's compressed data,
  found by reading the local header rather than by guessing an offset, because
  the first version of that test corrupted a header field and the archive read
  perfectly.
- An entry compressed with a method nothing can open, which is what a DRM'd
  book looks like; a manifest href that climbs out; a spine of stylesheets.
- An image inside a table caption inside a list item: the shape that catches a
  tree rewrite missing a container, which would otherwise keep an href nothing
  can resolve.
- **A mutation sweep**, which collapsed three guards into one: an empty `src`,
  a `data:` URL and a failed resolve all end the same way, and deleting any of
  the three changed nothing a test could see.

## ⛔ The third bounded ZIP reader in this fleet

`go-ooxml/opc` has one for OPC packages and `go-pdfkit/convert` has one for
comic archives. The three share a shape — a cap on entries, a cap per entry, a
cap in total, and a refusal of names that climb out — and that shape is what
wants extracting into a module of its own. `ocf.go` is written with no richdoc
and no EPUB in it, so the extraction is a move rather than a rewrite.

## The rest of the family

[**richdoc**](https://github.com/go-richdoc/richdoc) is the model ·
[**html**](https://github.com/go-richdoc/html) reads the chapters ·
[**fb2**](https://github.com/go-richdoc/fb2) ·
[**markdown**](https://github.com/go-richdoc/markdown) ·
[**rst**](https://github.com/go-richdoc/rst) ·
[**latex**](https://github.com/go-richdoc/latex) ·
[**data**](https://github.com/go-richdoc/data)

[**go-pdfkit**](https://github.com/go-pdfkit) composes a richdoc document into
a PDF, which is what makes `.epub` → PDF a two-step with no format-specific
code in between.

## Licence

BSD-3-Clause.
