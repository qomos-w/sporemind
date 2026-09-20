package flagparse

import (
	"reflect"
	"testing"
)

type RmFlags struct {
	Recursive bool   `flag:"r,recursive"`
	Force     bool   `flag:"f,force"`
	Confirm   bool   `flag:"confirm"`
	Path      string // positional
}

type GrepFlags struct {
	Recursive  bool   `flag:"r,recursive"`
	IgnoreCase bool   `flag:"i,ignore-case"`
	Context    int    `flag:"C,context"`
	BeforeCtx  int    `flag:"B,before-context"`
	AfterCtx   int    `flag:"A,after-context"`
	Glob       string `flag:"glob,include"`
	HeadLimit  int    `flag:"head-limit"`
	OutputMode string `flag:"output-mode"`
	Multiline  bool   `flag:"multiline"`
	Pattern    string // positional 1
	Path       string // positional 2
}

type GlobFlags struct {
	Type     string `flag:"type"`
	MaxDepth int    `flag:"maxdepth"`
	Pattern  string // positional 1
	Path     string // positional 2
}

func TestParseBasicFlags(t *testing.T) {
	var f RmFlags
	res, err := Parse(&f, []string{"-r", "-f", "build/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive {
		t.Error("expected Recursive=true")
	}
	if !f.Force {
		t.Error("expected Force=true")
	}
	if f.Path != "build/" {
		t.Errorf("expected Path=build/, got %q", f.Path)
	}
	if len(res.Warnings) > 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}
}

func TestParseCombinedShortFlags(t *testing.T) {
	var f RmFlags
	_, err := Parse(&f, []string{"-rf", "build/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive || !f.Force {
		t.Error("expected both -r and -f")
	}
	if f.Path != "build/" {
		t.Errorf("expected Path=build/, got %q", f.Path)
	}
}

func TestParseLongFlags(t *testing.T) {
	var f RmFlags
	_, err := Parse(&f, []string{"--recursive", "--force", "build/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive || !f.Force {
		t.Error("expected both --recursive and --force")
	}
}

func TestParseFlagValueEquals(t *testing.T) {
	var g GrepFlags
	_, err := Parse(&g, []string{"--output-mode=files", "pattern", "."})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if g.OutputMode != "files" {
		t.Errorf("expected OutputMode=files, got %q", g.OutputMode)
	}
}

func TestParsePositionalArgs(t *testing.T) {
	var g GrepFlags
	_, err := Parse(&g, []string{"-r", "func main", "src/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !g.Recursive {
		t.Error("expected Recursive=true")
	}
	if g.Pattern != "func main" {
		t.Errorf("expected Pattern='func main', got %q", g.Pattern)
	}
	if g.Path != "src/" {
		t.Errorf("expected Path=src/, got %q", g.Path)
	}
}

func TestParseFlagsAfterPositionalWarning(t *testing.T) {
	var f RmFlags
	res, err := Parse(&f, []string{"build/", "-r"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive {
		t.Error("expected Recursive=true even after positional")
	}
	if f.Path != "build/" {
		t.Errorf("expected Path=build/, got %q", f.Path)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(res.Warnings), res.Warnings)
	}
}

func TestParseUnknownFlagWarning(t *testing.T) {
	var f RmFlags
	res, err := Parse(&f, []string{"-x", "build/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(res.Warnings), res.Warnings)
	}
	if res.Warnings[0] != "unknown flag -x" {
		t.Errorf("unexpected warning: %q", res.Warnings[0])
	}
}

func TestParseDoubleDashTermination(t *testing.T) {
	var f RmFlags
	_, err := Parse(&f, []string{"-r", "--", "-f", "build/"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive {
		t.Error("expected Recursive=true")
	}
	if f.Force {
		t.Error("expected Force=false (after --)")
	}
	if f.Path != "-f" {
		t.Errorf("expected first positional=-f (after --), got %q", f.Path)
	}
}

func TestParseGrepComplex(t *testing.T) {
	var g GrepFlags
	_, err := Parse(&g, []string{"-riC", "5", "--glob=*.go", "func main", "."})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !g.Recursive {
		t.Error("expected Recursive=true")
	}
	if !g.IgnoreCase {
		t.Error("expected IgnoreCase=true")
	}
	if g.Context != 5 {
		t.Errorf("expected Context=5, got %d", g.Context)
	}
	if g.Glob != "*.go" {
		t.Errorf("expected Glob=*.go, got %q", g.Glob)
	}
	if g.Pattern != "func main" {
		t.Errorf("expected Pattern='func main', got %q", g.Pattern)
	}
	if g.Path != "." {
		t.Errorf("expected Path=., got %q", g.Path)
	}
}

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"rm -rf build/", []string{"rm", "-rf", "build/"}},
		{"grep -rn 'func main' .", []string{"grep", "-rn", "func main", "."}},
		{`grep -rn "func main" .`, []string{"grep", "-rn", "func main", "."}},
		{"rm --confirm build/", []string{"rm", "--confirm", "build/"}},
		{"", nil},
		{"  ", nil},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := SplitArgs(tc.input)
			if err != nil {
				t.Fatalf("split error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("expected %v, got %v", tc.expected, got)
			}
		})
	}
}

func TestSplitArgsUnbalancedQuotes(t *testing.T) {
	_, err := SplitArgs("grep 'unclosed")
	if err == nil {
		t.Error("expected error for unbalanced quotes")
	}
}

func TestParseCommand(t *testing.T) {
	var f RmFlags
	_, err := ParseCommand(&f, "rm -rf build/")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive || !f.Force {
		t.Error("expected -r and -f")
	}
	if f.Path != "build/" {
		t.Errorf("expected Path=build/, got %q", f.Path)
	}
}

func TestParseSkipFirstArg(t *testing.T) {
	// When the command string includes the command name (e.g., "rm -rf build/"),
	// we should skip the first arg.
	var f RmFlags
	args, _ := SplitArgs("rm -rf build/")
	// Skip the command name itself.
	_, err := Parse(&f, args[1:])
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if !f.Recursive || !f.Force {
		t.Error("expected -r and -f")
	}
	if f.Path != "build/" {
		t.Errorf("expected Path=build/, got %q", f.Path)
	}
}

func TestParseIntFlags(t *testing.T) {
	var g GrepFlags
	_, err := Parse(&g, []string{"--head-limit", "250", "pattern"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if g.HeadLimit != 250 {
		t.Errorf("expected HeadLimit=250, got %d", g.HeadLimit)
	}
}

func TestParseDefaultPath(t *testing.T) {
	// When only one positional arg given, Path should remain empty.
	var g GrepFlags
	_, err := Parse(&g, []string{"pattern"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if g.Pattern != "pattern" {
		t.Errorf("expected Pattern=pattern, got %q", g.Pattern)
	}
	if g.Path != "" {
		t.Errorf("expected Path empty (default), got %q", g.Path)
	}
}

func TestParseQuoteStripping(t *testing.T) {
	// --key="value" should have quotes stripped from the string value.
	var g GrepFlags
	_, err := Parse(&g, []string{"--glob=\"*.go\"", "pattern"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if g.Glob != "*.go" {
		t.Errorf("expected Glob='*.go' (quotes stripped), got %q", g.Glob)
	}
}

func TestParseQuoteStrippingSingleQuotes(t *testing.T) {
	var g GrepFlags
	_, err := Parse(&g, []string{"--glob='*.go'", "pattern"})
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if g.Glob != "*.go" {
		t.Errorf("expected Glob='*.go' (quotes stripped), got %q", g.Glob)
	}
}
