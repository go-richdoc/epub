package epub

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
)

// Limits bound what a publication may ask for. A zero field means the default,
// so Limits{} is the default everywhere.
//
// ⛔ Every one of these bounds an allocation decided by a number IN THE FILE.
// A ZIP's central directory costs about forty-six bytes an entry and says how
// large each entry will be once inflated: it costs the author of that file
// nothing to claim four gigabytes, and a reader that believes it allocates
// them.
type Limits struct {
	// MaxEntries is how many files the ZIP may hold. It bounds an allocation
	// made before a byte of content is read.
	MaxEntries int

	// MaxEntryBytes is how large any one entry may be once inflated, and
	// MaxTotalBytes how much this reader will inflate in all. One entry under
	// the ceiling says nothing about a thousand of them.
	MaxEntryBytes int64
	MaxTotalBytes int64

	// MaxDepth and MaxNodes are handed to the HTML reader for each content
	// document. They are named here so a caller sets one Limits rather than
	// two.
	MaxDepth int
	MaxNodes int
}

const (
	defaultMaxEntries    = 8192
	defaultMaxEntryBytes = 64 << 20
	defaultMaxTotalBytes = 384 << 20
)

func (l Limits) orDefaults() Limits {
	if l.MaxEntries <= 0 {
		l.MaxEntries = defaultMaxEntries
	}
	if l.MaxEntryBytes <= 0 {
		l.MaxEntryBytes = defaultMaxEntryBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = defaultMaxTotalBytes
	}
	return l
}

// an archive is a bounded reader over the publication's ZIP.
//
// ⛔ ENTRIES ARE READ ON DEMAND, not all at once. A book is mostly pictures and
// a conversion may want none of them, so inflating the archive up front would
// spend its whole budget on bytes nobody asked for — and would turn a 300 MiB
// book into a refusal where reading its chapters would have succeeded.
//
// ⛔ This is the third bounded ZIP reader in the fleet: go-ooxml/opc has one
// for OPC packages and go-pdfkit/convert has one for comic archives. The three
// share a shape — a cap on entries, a cap per entry, a cap in total, and a
// refusal of names that climb out of the container — and that shape is what
// wants extracting into a module of its own. It is deliberately written here
// with no richdoc and no EPUB in it, so that the extraction is a move rather
// than a rewrite.
type archive struct {
	zr    *zip.Reader
	files map[string]*zip.File
	lim   Limits
	spent int64
}

func openArchive(src []byte, lim Limits) (*archive, error) {
	zr, err := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	if err != nil {
		return nil, fmt.Errorf("epub: not a ZIP: %w", err)
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, fmt.Errorf("epub: %d entries, over the %d this reader will index",
			len(zr.File), lim.MaxEntries)
	}
	a := &archive{zr: zr, files: make(map[string]*zip.File, len(zr.File)), lim: lim}
	for _, f := range zr.File {
		// ⛔ The LAST entry of a given name wins, which is what a ZIP reader
		// that appends updates would do. A duplicate name is not refused: real
		// books carry them, and refusing the book over a repeated entry helps
		// nobody.
		a.files[path.Clean(f.Name)] = f
	}
	return a, nil
}

// read inflates one entry, within the budget.
func (a *archive) read(name string) ([]byte, error) {
	f, ok := a.files[path.Clean(name)]
	if !ok {
		return nil, fmt.Errorf("epub: %s is not in the publication", name)
	}
	// ⛔ Refused from the DECLARED size first, before a byte is inflated —
	// then bounded again while reading, in case the declaration was a lie the
	// other way. go-ooxml/opc learned both halves on a package that said 8 638
	// bytes and opened to 8 MiB.
	if size := int64(f.UncompressedSize64); size > a.lim.MaxEntryBytes {
		return nil, fmt.Errorf("epub: %s says it is %d bytes, over the %d limit",
			name, size, a.lim.MaxEntryBytes)
	}
	room := a.lim.MaxEntryBytes
	if left := a.lim.MaxTotalBytes - a.spent; left < room {
		room = left
	}
	if room <= 0 {
		return nil, fmt.Errorf("epub: %d bytes already inflated, which is the %d limit",
			a.spent, a.lim.MaxTotalBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("epub: %s: %w", name, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, room+1))
	if err != nil {
		return nil, fmt.Errorf("epub: %s: %w", name, err)
	}
	if int64(len(b)) > room {
		return nil, fmt.Errorf("epub: %s is larger than its declared size and past the %d limit",
			name, a.lim.MaxTotalBytes)
	}
	a.spent += int64(len(b))
	return b, nil
}

// resolve turns a reference inside one part into the name of another, the way
// every container format resolves one: relative to the DIRECTORY of the part
// that made the reference.
//
// ⛔ A reference that climbs out of the container is refused rather than
// cleaned into something harmless. This reader hands names to a map and not to
// a filesystem, so there is no zip-slip to suffer here — but a caller that
// joined one of these onto a directory would, and a name is easier to refuse
// than to apologise for.
//
// ⛔ A fragment is dropped and a reference with a SCHEME is refused: an
// external href is a URL and not an entry, and "http://x/y" cleaned into a
// path becomes "http:/x/y", a lookup that fails for a reason with nothing to
// do with what went wrong.
func resolve(from, href string) (string, error) {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if href == "" {
		// ⛔ "#somewhere" names a place in a document and not an entry of the
		// container, so it is refused rather than resolved to the referring
		// document: resolving it would make a picture out of the chapter that
		// asked for it, which is exactly the kind of nonsense that reaches a
		// renderer as a corrupt image.
		return "", fmt.Errorf("epub: %s refers to nothing but a fragment", from)
	}
	if i := strings.Index(href, ":"); i > 0 && !strings.ContainsAny(href[:i], "/\\") {
		return "", fmt.Errorf("epub: %s refers to %s, which is not in the publication", from, href)
	}
	if strings.HasPrefix(href, "/") {
		href = strings.TrimPrefix(href, "/")
	} else {
		href = path.Join(path.Dir(from), href)
	}
	name := path.Clean(href)
	if name == ".." || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("epub: %s refers to %s, which climbs out of the publication", from, href)
	}
	return name, nil
}
