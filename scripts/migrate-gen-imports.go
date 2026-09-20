// Command migrate-gen-imports rewrites Go files so that imports of
// github.com/qomos-w/sporemind/pkg/domain/gen/<subpkg> become a single
// gen "github.com/qomos-w/sporemind/pkg/domain/gen" import and all
// qualified references to those subpackages become gen.<Name>.
//
// It is AST-aware: field accesses on local variables whose names happen to
// match an imported package name are left untouched.
package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const genPath = "github.com/qomos-w/sporemind/pkg/domain/gen"
const genPathPrefix = genPath + "/"

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate-gen-imports <dir>")
		os.Exit(2)
	}
	root := os.Args[1]

	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(1)
	}

	sort.Strings(files)
	for _, f := range files {
		changed, err := rewriteFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", f, err)
			os.Exit(1)
		}
		if changed {
			rel, _ := filepath.Rel(root, f)
			fmt.Println(rel)
		}
	}
}

func rewriteFile(path string) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return false, fmt.Errorf("parse: %w", err)
	}

	// Determine which import names refer to old gen subpackages.
	oldNames := make(map[string]bool)
	for _, imp := range f.Imports {
		p, err := strconvUnquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(p, genPathPrefix) {
			continue
		}
		name := importName(imp, p)
		if name == "_" || name == "." {
			continue
		}
		oldNames[name] = true
	}
	if len(oldNames) == 0 {
		return false, nil
	}

	// Rewrite selector expressions: oldName.Ident -> gen.Ident
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if !oldNames[ident.Name] {
			return true
		}
		ident.Name = "gen"
		return true
	})

	// Rebuild imports: drop all gen subpackage imports, ensure gen import exists.
	var newImports []*ast.ImportSpec
	hasGen := false
	for _, imp := range f.Imports {
		p, err := strconvUnquote(imp.Path.Value)
		if err != nil {
			newImports = append(newImports, imp)
			continue
		}
		if strings.HasPrefix(p, genPathPrefix) {
			continue
		}
		if p == genPath {
			hasGen = true
			if imp.Name == nil || imp.Name.Name == "" {
				imp.Name = &ast.Ident{Name: "gen"}
			}
		}
		newImports = append(newImports, imp)
	}
	if !hasGen {
		newImports = append(newImports, &ast.ImportSpec{
			Name: &ast.Ident{Name: "gen"},
			Path: &ast.BasicLit{Kind: token.STRING, Value: `"` + genPath + `"`},
		})
	}

	// Sort imports by import path (same order gofmt uses).
	sort.SliceStable(newImports, func(i, j int) bool {
		pi, _ := strconvUnquote(newImports[i].Path.Value)
		pj, _ := strconvUnquote(newImports[j].Path.Value)
		return pi < pj
	})

	f.Imports = newImports

	// Replace the import declaration node in the file.
	for i, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}
		genDecl.Specs = nil
		for _, imp := range newImports {
			genDecl.Specs = append(genDecl.Specs, imp)
		}
		if len(genDecl.Specs) == 0 {
			f.Decls = append(f.Decls[:i], f.Decls[i+1:]...)
		}
		break
	}

	var buf strings.Builder
	if err := format.Node(&buf, fset, f); err != nil {
		return false, fmt.Errorf("format: %w", err)
	}
	out := buf.String()

	// Preserve original line endings (CRLF vs LF) by matching input.
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read: %w", err)
	}
	if strings.Contains(string(srcBytes), "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}

	if out == string(srcBytes) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return false, fmt.Errorf("write: %w", err)
	}
	return true, nil
}

func strconvUnquote(s string) (string, error) {
	return strings.Trim(s, `"`), nil
}

func importName(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil && imp.Name.Name != "" {
		return imp.Name.Name
	}
	i := strings.LastIndex(path, "/")
	if i >= 0 {
		return path[i+1:]
	}
	return path
}
