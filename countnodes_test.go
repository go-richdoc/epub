// Copyright (c) the go-richdoc authors.
// SPDX-License-Identifier: BSD-3-Clause

package epub

import (
	"testing"

	"github.com/go-richdoc/richdoc"
)

// TestCountNodesLooksInsideEveryContainer.
//
// ⛔ A COUNTER THAT DOES NOT RECURSE IS A CEILING SOMEBODY ROUTES AROUND. A
// thousand nested lists holding a thousand paragraphs each is two nodes to a
// shallow count, and that is precisely the document a node ceiling exists for.
// So every container richdoc has gets a case here with its own expected number,
// and a container added to the model without a case here will show up as a
// count that did not change.
func TestCountNodesLooksInsideEveryContainer(t *testing.T) {
	text := []richdoc.Inline{richdoc.Text{Value: "x"}}

	cases := []struct {
		name   string
		blocks []richdoc.Block
		want   int
	}{
		{"nothing", nil, 0},
		{"one paragraph and its text", []richdoc.Block{richdoc.Paragraph{Inlines: text}}, 2},
		{"a heading and its text", []richdoc.Block{richdoc.Heading{Level: 1, Inlines: text}}, 2},
		{"a code block has no children", []richdoc.Block{richdoc.CodeBlock{Text: "x"}}, 1},
		{"a raw block has no children", []richdoc.Block{richdoc.RawBlock{Text: "x"}}, 1},
		{
			"a quote and the paragraph inside it",
			[]richdoc.Block{richdoc.BlockQuote{Blocks: []richdoc.Block{
				richdoc.Paragraph{Inlines: text},
			}}},
			3, // the quote, the paragraph, the text
		},
		{
			"a list, its items, their blocks and their text",
			[]richdoc.Block{richdoc.List{Items: []richdoc.ListItem{
				{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: text}}},
				{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: text}}},
			}}},
			7, // the list, two items, two paragraphs, two texts
		},
		{
			"a table, its cells, and their inlines and blocks alike",
			[]richdoc.Block{richdoc.Table{
				Header: []richdoc.Cell{{Inlines: text}},
				Rows: [][]richdoc.Cell{
					{{Inlines: text}},
					{{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: text}}}},
				},
			}},
			8, // the table, three cells, two texts, then a paragraph and its text
		},
		{
			// ⛔ The hole that counting cells closes: the cheapest markup there
			// is. Twenty empty cells hold nothing to recurse into, so a counter
			// that only looked at their contents called this ONE node.
			"a table of empty cells is not one node",
			[]richdoc.Block{richdoc.Table{Rows: [][]richdoc.Cell{
				make([]richdoc.Cell, 10), make([]richdoc.Cell, 10),
			}}},
			21, // the table and twenty cells
		},
		{
			"a list of empty items is not one node",
			[]richdoc.Block{richdoc.List{Items: make([]richdoc.ListItem, 10)}},
			11, // the list and ten items
		},
		{
			"a link's inlines",
			[]richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{
				richdoc.Link{URL: "u", Inlines: text},
			}}},
			3, // the paragraph, the link, its text
		},
		{
			"emphasis, strength and an anchor all recurse",
			[]richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{
				richdoc.Emph{Inlines: text},
				richdoc.Strong{Inlines: text},
				richdoc.Anchor{Inlines: text},
			}}},
			7, // the paragraph, three wrappers, three texts
		},
		{
			"a footnote holds BLOCKS, not inlines",
			[]richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{
				richdoc.Footnote{Blocks: []richdoc.Block{richdoc.Paragraph{Inlines: text}}},
			}}},
			4, // the paragraph, the footnote, the inner paragraph, its text
		},
		{
			"an image is one inline and has no children",
			[]richdoc.Block{richdoc.Paragraph{Inlines: []richdoc.Inline{
				richdoc.Image{URL: "u", Alt: "a"},
			}}},
			2,
		},
		{
			// ⛔ The planted positive for the whole idea: deep nesting must not
			// read as shallow.
			"ten nested quotes",
			[]richdoc.Block{nestQuotes(10)},
			11, // ten quotes and the paragraph at the bottom, which has no text
		},
	}

	for _, c := range cases {
		if got := countNodes(c.blocks); got != c.want {
			t.Errorf("%s: counted %d, want %d", c.name, got, c.want)
		}
	}
}

// nestQuotes is n quotes inside one another with an empty paragraph at the
// bottom.
func nestQuotes(n int) richdoc.Block {
	var b richdoc.Block = richdoc.Paragraph{}
	for range n {
		b = richdoc.BlockQuote{Blocks: []richdoc.Block{b}}
	}
	return b
}
