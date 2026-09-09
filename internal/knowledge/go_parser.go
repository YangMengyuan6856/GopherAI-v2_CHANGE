package knowledge

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
)

func parseGoSourceBlocks(filename string, content []byte) ([]sourceBlock, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filename, content, parser.ParseComments|parser.AllErrors)
	if err != nil {
		return nil, fmt.Errorf("invalid Go source: %w", err)
	}
	lines := sourceLines(string(content))
	packageName := file.Name.Name
	blocks := make([]sourceBlock, 0, len(file.Decls)+1)
	packageLine := fileSet.Position(file.Package).Line
	if packageLine > 0 && packageLine <= len(lines) {
		block := newSourceBlock("package "+packageName, packageLine, packageLine, lines[packageLine-1], true)
		block.lineAware = true
		blocks = append(blocks, block)
	}
	for _, declaration := range file.Decls {
		lineStart, lineEnd := declarationLines(fileSet, declaration)
		if lineStart < 1 || lineEnd < lineStart || lineStart > len(lines) {
			continue
		}
		lineEnd = min(lineEnd, len(lines))
		content := strings.Join(lines[lineStart-1:lineEnd], "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		block := newSourceBlock(goDeclarationSection(packageName, declaration), lineStart, lineEnd, content, true)
		block.lineAware = true
		blocks = append(blocks, block)
	}
	return blocks, nil
}

func sourceLines(content string) []string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func declarationLines(fileSet *token.FileSet, declaration ast.Decl) (int, int) {
	start := declaration.Pos()
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		if typed.Doc != nil {
			start = typed.Doc.Pos()
		}
	case *ast.GenDecl:
		if typed.Doc != nil {
			start = typed.Doc.Pos()
		}
	}
	return fileSet.Position(start).Line, fileSet.Position(declaration.End()).Line
}

func goDeclarationSection(packageName string, declaration ast.Decl) string {
	prefix := "package " + packageName + " > "
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		return prefix + goFunctionName(typed)
	case *ast.GenDecl:
		kind := strings.ToLower(typed.Tok.String())
		if typed.Tok == token.IMPORT {
			return prefix + "imports"
		}
		names := make([]string, 0)
		for _, specification := range typed.Specs {
			switch spec := specification.(type) {
			case *ast.TypeSpec:
				names = append(names, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
			}
		}
		if len(names) == 0 {
			return prefix + kind
		}
		return prefix + kind + " " + strings.Join(names, ",")
	default:
		return prefix + "declaration"
	}
}

func goFunctionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return "func " + function.Name.Name
	}
	return "method " + receiverTypeName(function.Recv.List[0].Type) + "." + function.Name.Name
}

func receiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	case *ast.SelectorExpr:
		return receiverTypeName(typed.X) + "." + typed.Sel.Name
	default:
		var output bytes.Buffer
		if err := printer.Fprint(&output, token.NewFileSet(), expression); err == nil && output.Len() > 0 {
			return output.String()
		}
		return "receiver"
	}
}
