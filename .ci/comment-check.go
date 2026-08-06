//go:build ignore

// SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
//
// SPDX-License-Identifier: AGPL-3.0-only
//
// Warns about long non-documentation comment runs. Always exits 0 — it never blocks a commit.
// Lives under .ci/ so the go tool ignores it: dot-directories are excluded from ./... entirely.

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
)

// Flag a run of more than this many consecutive // lines.
const maxLines = 3

func main() {
	var findings int
	fset := token.NewFileSet()

	for _, path := range os.Args[1:] {
		if path == "--" {
			continue // go run swallows trailing .go arguments as sources without this separator
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			continue // a file that does not parse is the compiler's problem, not ours
		}

		// The parser attaches a comment group as Doc using the same rule godoc does, so
		// documentation identifies itself — we never have to guess.
		documented := map[*ast.CommentGroup]bool{}
		if file.Doc != nil {
			documented[file.Doc] = true
		}
		var bodies [][2]token.Pos
		ast.Inspect(file, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.FuncDecl:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
				if d.Body != nil {
					bodies = append(bodies, [2]token.Pos{d.Body.Lbrace, d.Body.Rbrace})
				}
			case *ast.FuncLit:
				if d.Body != nil {
					bodies = append(bodies, [2]token.Pos{d.Body.Lbrace, d.Body.Rbrace})
				}
			case *ast.GenDecl:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
			case *ast.TypeSpec:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
			case *ast.ValueSpec:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
			case *ast.ImportSpec:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
			case *ast.Field:
				if d.Doc != nil {
					documented[d.Doc] = true
				}
			}
			return true
		})

		// Boundary for "file-level overview": the first NON-import declaration. Using the
		// first declaration outright would make the import block the boundary, so a file
		// overview written below the imports would read as free-floating.
		firstDecl := token.Pos(1 << 30)
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
				continue
			}
			firstDecl = decl.Pos()
			break
		}

		for _, group := range file.Comments {
			// len(List) counts // lines; a /* */ block is a single entry and is never flagged,
			// which is the point — that is the form long prose should use.
			lines := len(group.List)
			if lines <= maxLines || documented[group] || group.Pos() < firstDecl {
				continue
			}
			var where string
			for _, b := range bodies {
				if group.Pos() > b[0] && group.Pos() < b[1] {
					where = " (inside a function body)"
					break
				}
			}
			pos := fset.Position(group.Pos())
			fmt.Printf("  %s:%d  %d-line // comment%s\n", pos.Filename, pos.Line, lines, where)
			findings++
		}
	}

	if findings > 0 {
		fmt.Printf("WARN %d long non-documentation comment(s), max %d lines.\n", findings, maxLines)
		fmt.Println("     Shorten them, attach them to a declaration as a doc comment, or use /* */.")
	}
}
