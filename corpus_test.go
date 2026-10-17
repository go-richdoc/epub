package epub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-richdoc/richdoc"
)

// TestARealCorpus reads every .epub in a directory and says what came out.
//
// ⛔ THERE IS NO BOOK IN testdata, and that is deliberate rather than lazy. The
// books this was measured against are the machine owner's own library —
// copyrighted, personal, and nobody's business in a public repository. A
// fixture a test ships has to be one the repository may carry, so the ones
// here are built by hand, and the real population is read from wherever the
// person running the test keeps theirs:
//
//	EPUB_CORPUS=~/Books go test -run TestARealCorpus -v ./...
//
// Without the variable the test SKIPS and says so, because a test that passes
// by doing nothing is worse than one that is not there.
//
// What it asserts is what a corpus can assert without knowing the books: that
// every one of them opens, that what comes out has prose in it, and that none
// of them panics or hangs. The counts are printed rather than asserted — a
// population measures the reader, and an expected number would measure the
// library.
func TestARealCorpus(t *testing.T) {
	dir := os.Getenv("EPUB_CORPUS")
	if dir == "" {
		t.Skip("EPUB_CORPUS is not set: no real books to read. Set it to a directory of .epub files.")
	}
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		dir = filepath.Join(home, dir[2:])
	}
	var books []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".epub") {
			books = append(books, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(books) == 0 {
		t.Fatalf("%s holds no .epub file: a corpus of nothing reports nothing", dir)
	}

	var opened, refused int
	for _, b := range books {
		src, err := os.ReadFile(b)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(b), err)
			continue
		}
		doc, err := Parse(src, Limits{})
		if err != nil {
			refused++
			t.Logf("REFUSED %-50.50s %v", filepath.Base(b), err)
			continue
		}
		opened++
		words := len(strings.Fields(richdoc.PlainText(doc)))
		var headings, images int
		richdoc.Walk(doc, visitor(func(n any) {
			switch n.(type) {
			case richdoc.Heading:
				headings++
			case richdoc.Image:
				images++
			}
		}))
		t.Logf("read    %-50.50s %6d blocks %7d words %3d headings %3d images",
			filepath.Base(b), len(doc.Blocks), words, headings, images)
		if words < 100 {
			t.Errorf("%s came back with %d words: a book that opens and says nothing is a defect, not a short book",
				filepath.Base(b), words)
		}
	}
	t.Logf("%d book(s): %d read, %d refused", len(books), opened, refused)
}
