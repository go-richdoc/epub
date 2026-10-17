package epub

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// containerPath is the one path in an EPUB that IS fixed. Everything else is
// named by something that was read.
const containerPath = "META-INF/container.xml"

// a container is META-INF/container.xml: it names the package document.
type container struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

// rootfile is the path of the package document.
//
// ⛔ The FIRST rootfile, and the specification allows several: a container may
// offer the same publication as more than one rendition (a fixed-layout one
// beside a reflowable one). Taking the first is what reading systems do, and
// taking the last would quietly prefer whichever the producer listed last.
func rootfile(a *archive) (string, error) {
	b, err := a.read(containerPath)
	if err != nil {
		return "", err
	}
	var c container
	if err := xml.Unmarshal(b, &c); err != nil {
		return "", fmt.Errorf("epub: %s does not parse: %w", containerPath, err)
	}
	for _, r := range c.Rootfiles {
		if r.FullPath != "" {
			return strings.TrimPrefix(r.FullPath, "/"), nil
		}
	}
	return "", fmt.Errorf("epub: %s names no package document", containerPath)
}

// an opf is the package document: what the book contains, and in what order to
// read it.
//
// ⛔ Matched by LOCAL NAME with no namespace, as go-richdoc/fb2 does and for
// the same measured reason: producers write the OPF namespace, mis-write it,
// and leave it off their Dublin Core elements. The structure disambiguates what
// a namespace would — `title` only appears inside metadata, `item` only inside
// manifest — which is why this is safe here and would not be in OOXML, where
// one container holds three vocabularies.
type opf struct {
	Metadata struct {
		Title      []string `xml:"title"`
		Creator    []string `xml:"creator"`
		Language   []string `xml:"language"`
		Date       []string `xml:"date"`
		Identifier []string `xml:"identifier"`
		Publisher  []string `xml:"publisher"`
		Meta       []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []item `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		ItemRefs []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// an item is one entry of the manifest.
type item struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

// readOPF reads the package document named by the container.
func readOPF(a *archive, name string) (*opf, error) {
	b, err := a.read(name)
	if err != nil {
		return nil, err
	}
	var p opf
	if err := xml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("epub: %s does not parse: %w", name, err)
	}
	return &p, nil
}

// meta is what the book says about itself, in the keys richdoc's other readers
// use.
//
// ⛔ EPUB 3 moved the author's name: EPUB 2 wrote <dc:creator>, EPUB 3 writes
// it there too but a refinement may give the name to display. Both spellings
// are read, the simple one first, because a book that has only the old one is
// the common case and a book that has both agrees with itself.
func (p *opf) meta() map[string]string {
	out := map[string]string{}
	set := func(key string, values []string) {
		for _, v := range values {
			if s := strings.TrimSpace(v); s != "" {
				out[key] = s
				return
			}
		}
	}
	set("title", p.Metadata.Title)
	set("author", p.Metadata.Creator)
	set("language", p.Metadata.Language)
	set("date", p.Metadata.Date)
	set("publisher", p.Metadata.Publisher)
	set("identifier", p.Metadata.Identifier)
	for _, m := range p.Metadata.Meta {
		switch {
		case strings.EqualFold(m.Name, "calibre:series") && m.Content != "":
			out["series"] = m.Content
		case strings.EqualFold(m.Property, "dcterms:modified") && strings.TrimSpace(m.Value) != "":
			if out["date"] == "" {
				out["date"] = strings.TrimSpace(m.Value)
			}
		}
	}
	if len(out) == 0 {
		// A publication that says nothing about itself has no metadata, not an
		// empty map.
		return nil
	}
	return out
}

// readingOrder is the spine, resolved against the manifest: the content
// documents of the book, in the order the book says to read them.
//
// ⛔ An itemref naming an id the manifest does not have is SKIPPED rather than
// refused. It is a producer's defect, it is common, and refusing the book over
// one dangling reference would lose the other three hundred chapters.
func (p *opf) readingOrder(opfName string) ([]string, error) {
	byID := make(map[string]item, len(p.Manifest.Items))
	for _, it := range p.Manifest.Items {
		if it.ID != "" {
			byID[it.ID] = it
		}
	}
	var out []string
	for _, ref := range p.Spine.ItemRefs {
		it, ok := byID[ref.IDRef]
		if !ok || it.Href == "" {
			continue
		}
		if !isContent(it.MediaType) {
			// An image or a stylesheet in the spine is a producer's mistake;
			// only a content document can be read as one.
			continue
		}
		name, err := resolve(opfName, it.Href)
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("epub: the spine names no content document, so there is no reading order")
	}
	return out, nil
}

// isContent reports whether a media type is one of EPUB's content documents.
//
// ⛔ An EMPTY media type counts as content, because the attribute is required
// and producers leave it out anyway: a spine item with no media-type is
// overwhelmingly an XHTML chapter, and the alternative is dropping it.
func isContent(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "", "application/xhtml+xml", "text/html", "application/x-dtbook+xml":
		return true
	}
	return false
}
