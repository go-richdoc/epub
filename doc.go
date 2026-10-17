// Package epub reads an EPUB 2 or EPUB 3 publication into the richdoc document
// model.
//
// An EPUB is three things in a trench coat: a ZIP, a manifest, and an order.
// None of them is the content — the content is XHTML, which
// [github.com/go-richdoc/html] reads — so this package is about the three
// layers above it:
//
//	META-INF/container.xml   names the package document, which is NOT at a
//	                         fixed path: content.opf, OEBPS/content.opf and
//	                         EPUB/package.opf are all real
//	the package document     the manifest (what is in the book) and the spine
//	                         (what order to read it in)
//	the spine                the reading order, and the only thing that says it
//
// # ⛔ Nothing here is found by its path
//
// The same rule OPC taught go-ooxml, and for the same reason: a reader that
// guessed "OEBPS/content.opf" would work on most books and on nothing else.
// The container names the package document; the package document names its
// content; the spine says in which order. Each step is read, never assumed.
//
// # ⛔ The reading order is the spine, not the ZIP
//
// A ZIP has no order worth trusting and an alphabetical walk of its entries
// gives chapter 10 before chapter 2 — the defect every naive reader of a
// container format has. Only the spine says what comes after what.
//
// An item marked linear="no" is auxiliary in EPUB's terms: an answer key, a
// pop-up footnote, an advertisement. It is KEPT, in spine position, because
// losing a chapter is worse than carrying an appendix a reader did not ask
// for, and the specification leaves the choice to the reading system.
//
// # An image becomes its bytes
//
// Images are resolved against the chapter that referred to them and inlined as
// data URLs, the same decision [github.com/go-richdoc/fb2] makes and for the
// same reason: once the ZIP is closed, "images/cover.jpg" is a name nothing
// can resolve. An image past Limits.MaxEntryBytes keeps its href rather than
// being truncated — half a JPEG is not a smaller picture.
//
// # What it is not
//
// It does not read the navigation document or the NCX as a table of contents.
// A richdoc document has headings, which is what a table of contents is made
// from; a second list of the same links would be a page of duplicates. A nav
// document the author put IN the spine is content like any other and is read
// there.
//
// Nothing here is CGO, and it builds for GOOS=js.
package epub
