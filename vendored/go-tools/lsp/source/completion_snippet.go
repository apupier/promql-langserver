// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package source

import (
	"go/ast"

	"github.com/slrtbtfs/promql-lsp/vendored/go-tools/lsp/snippet"
)

// structFieldSnippets calculates the snippet for struct literal field names.
func (c *completer) structFieldSnippet(label, detail string) *snippet.Builder {
	if !c.wantStructFieldCompletions() {
		return nil
	}

	// If we are in a deep completion then we can't be completing a field
	// name (e.g. "Foo{f<>}" completing to "Foo{f.Bar}" should not generate
	// a snippet).
	if c.inDeepCompletion() {
		return nil
	}

	clInfo := c.enclosingCompositeLiteral

	// If we are already in a key-value expression, we don't want a snippet.
	if clInfo.kv != nil {
		return nil
	}

	snip := &snippet.Builder{}

	// A plain snippet turns "Foo{Ba<>" into "Foo{Bar: <>".
	snip.WriteText(label + ": ")
	snip.WritePlaceholder(func(b *snippet.Builder) {
		// A placeholder snippet turns "Foo{Ba<>" into "Foo{Bar: <*int*>".
		if c.opts.Placeholders {
			b.WriteText(detail)
		}
	})

	fset := c.view.Session().Cache().FileSet()

	// If the cursor position is on a different line from the literal's opening brace,
	// we are in a multiline literal.
	if fset.Position(c.pos).Line != fset.Position(clInfo.cl.Lbrace).Line {
		snip.WriteText(",")
	}

	return snip
}

// functionCallSnippets calculates the snippet for function calls.
func (c *completer) functionCallSnippet(name string, params []string) *snippet.Builder {
	// If there is no suffix then we need to reuse existing call parens
	// "()" if present. If there is an identifer suffix then we always
	// need to include "()" since we don't overwrite the suffix.
	if c.surrounding != nil && c.surrounding.Suffix() == "" && len(c.path) > 1 {
		// If we are the left side (i.e. "Fun") part of a call expression,
		// we don't want a snippet since there are already parens present.
		switch n := c.path[1].(type) {
		case *ast.CallExpr:
			// The Lparen != Rparen check detects fudged CallExprs we
			// inserted when fixing the AST. In this case, we do still need
			// to insert the calling "()" parens.
			if n.Fun == c.path[0] && n.Lparen != n.Rparen {
				return nil
			}
		case *ast.SelectorExpr:
			if len(c.path) > 2 {
				if call, ok := c.path[2].(*ast.CallExpr); ok && call.Fun == c.path[1] && call.Lparen != call.Rparen {
					return nil
				}
			}
		}
	}
	snip := &snippet.Builder{}
	snip.WriteText(name + "(")

	if c.opts.Placeholders {
		// A placeholder snippet turns "someFun<>" into "someFunc(<*i int*>, *s string*)".
		for i, p := range params {
			if i > 0 {
				snip.WriteText(", ")
			}
			snip.WritePlaceholder(func(b *snippet.Builder) {
				b.WriteText(p)
			})
		}
	} else {
		// A plain snippet turns "someFun<>" into "someFunc(<>)".
		if len(params) > 0 {
			snip.WritePlaceholder(nil)
		}
	}

	snip.WriteText(")")

	return snip
}
