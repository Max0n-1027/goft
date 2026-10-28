package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// keysInModule finds every piece of text the module hands to the catalog:
// the first argument of Errorf, Msg and N, and of a Printer's Sprintf and Text.
// It returns each key with where it was first seen.
//
// A key that is not a constant — a variable, a field — cannot be looked up
// here; such text has to be marked with N where it is written, which is the
// call this finds.
func keysInModule(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	keys := make(map[string]string)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "build", "dist", "docs", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		inI18n := file.Name.Name == "i18n"
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 || !isCatalogCall(call, inI18n) {
				return true
			}
			if key, ok := constantString(call.Args[0]); ok {
				if _, seen := keys[key]; !seen {
					keys[key] = fset.Position(call.Pos()).String()
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func isCatalogCall(call *ast.CallExpr, inI18n bool) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, _ := fun.X.(*ast.Ident)
		switch fun.Sel.Name {
		case "Errorf", "Msg", "N":
			return pkg != nil && pkg.Name == "i18n"
		case "Sprintf":
			// fmt's is the only other Sprintf in the module.
			return pkg == nil || pkg.Name != "fmt"
		case "Text":
			return len(call.Args) == 1
		}
	case *ast.Ident:
		switch fun.Name {
		case "Errorf", "Msg", "N":
			return inI18n
		}
	}
	return false
}

// constantString evaluates a string literal, or literals joined with +.
func constantString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, ok := constantString(v.X)
		if !ok {
			return "", false
		}
		r, ok := constantString(v.Y)
		return l + r, ok
	case *ast.ParenExpr:
		return constantString(v.X)
	}
	return "", false
}

func TestEveryMessageHasAJapaneseTranslation(t *testing.T) {
	keys := keysInModule(t)
	if len(keys) < 50 {
		t.Fatalf("found only %d messages; the search is not finding the calls", len(keys))
	}
	var missing []string
	for key, where := range keys {
		if _, ok := japanese[key]; !ok {
			missing = append(missing, fmt.Sprintf("%s: %q", where, key))
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("no Japanese for %s", m)
	}
}

func TestEveryTranslationIsUsed(t *testing.T) {
	keys := keysInModule(t)
	var unused []string
	for key := range japanese {
		if _, ok := keys[key]; !ok {
			unused = append(unused, key)
		}
	}
	sort.Strings(unused)
	for _, key := range unused {
		t.Errorf("the translation of %q is not used: the English changed, or the text is gone", key)
	}
}

// TestTranslationsTakeTheSameArguments renders each translation with the
// arguments its English takes, and wants every one of them used, each where
// its verb can print it.
func TestTranslationsTakeTheSameArguments(t *testing.T) {
	for key, ja := range japanese {
		args, tokens, err := sampleArgs(key)
		if err != nil {
			t.Errorf("%q: %v", key, err)
			continue
		}
		for _, side := range []struct{ name, format string }{{"English", key}, {"Japanese", ja}} {
			got := fmt.Sprintf(withoutW(side.format), args...)
			if strings.Contains(got, "%!") {
				t.Errorf("%s of %q does not fit its arguments: %s", side.name, key, got)
				continue
			}
			for i, tok := range tokens {
				if !strings.Contains(got, tok) {
					t.Errorf("%s of %q leaves argument %d out: %s", side.name, key, i+1, got)
				}
			}
		}
		if strings.TrimSpace(ja) == "" {
			t.Errorf("the translation of %q is empty", key)
		}
	}
}

// sampleArgs makes an argument for each verb in an English format, of a type
// the verb prints, distinct enough to be found again in the output. A verb
// with an explicit index, %[1]s, uses the argument it names.
func sampleArgs(format string) ([]any, []string, error) {
	var (
		args   []any
		tokens []string
	)
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		j := i + 1
		index := 0
		if j < len(format) && format[j] == '[' {
			end := strings.IndexByte(format[j:], ']')
			if end < 0 {
				return nil, nil, fmt.Errorf("an index with no closing bracket")
			}
			n, err := strconv.Atoi(format[j+1 : j+end])
			if err != nil {
				return nil, nil, fmt.Errorf("index %s: %v", format[j:j+end+1], err)
			}
			index, j = n, j+end+1
		}
		for j < len(format) && strings.ContainsRune("+-# 0123456789.", rune(format[j])) {
			j++
		}
		if j >= len(format) {
			return nil, nil, fmt.Errorf("a %% with no verb")
		}
		verb := format[j]
		spec := "%" + format[strings.LastIndexAny(format[i:j], "%]")+i+1:j] + string(verb)
		i = j
		if verb == '%' {
			continue
		}
		if index == 0 {
			index = len(args) + 1
		}
		if index <= len(args) {
			continue // the argument is already there, made by an earlier verb
		}
		if index != len(args)+1 {
			return nil, nil, fmt.Errorf("argument %d is used before argument %d", index, len(args)+1)
		}
		var arg any
		switch verb {
		case 'd', 'o', 'x', 'X', 'U', 'c':
			arg = 1000 + index
		case 'f', 'g', 'e':
			arg = 1000.25 + float64(index)
		case 's', 'v', 'q', 'w':
			arg = fmt.Sprintf("arg%d", index)
		case 't':
			arg = index%2 == 0
		default:
			return nil, nil, fmt.Errorf("verb %%%c is not one the test knows", verb)
		}
		if verb == 'w' {
			spec = spec[:len(spec)-1] + "v"
		}
		args = append(args, arg)
		tokens = append(tokens, fmt.Sprintf(spec, arg))
	}
	return args, tokens, nil
}

func TestSampleArgsReadsExplicitIndexes(t *testing.T) {
	args, tokens, err := sampleArgs("%[1]s and %[2]d, %[1]s again, %04o")
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 || fmt.Sprint(tokens) != "[arg1 1002 1753]" {
		t.Errorf("sampleArgs() = %v, %v; want three arguments", args, tokens)
	}
}

// The search above has to find calls the way they are written, or the tests
// that rely on it pass for want of anything to check.
func TestTheSearchFindsEachKindOfCall(t *testing.T) {
	src := `package x
func f(p *i18n.Printer) {
	_ = i18n.Errorf("errorf %s", 1)
	_ = i18n.Msg("msg")
	_ = i18n.N("marked " + "and joined")
	_ = p.Sprintf("printer sprintf %d", 1)
	_ = p.Text("printer text")
	_ = fmt.Sprintf("not this")
	_ = x.Errorf("nor this")
}`
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > 0 && isCatalogCall(call, false) {
			if key, ok := constantString(call.Args[0]); ok {
				found = append(found, key)
			}
		}
		return true
	})
	want := []string{"errorf %s", "msg", "marked and joined", "printer sprintf %d", "printer text"}
	if fmt.Sprint(found) != fmt.Sprint(want) {
		t.Errorf("found %q, want %q", found, want)
	}
}
