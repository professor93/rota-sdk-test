// Command matrix prints, as Markdown, which rota symbols the suite pins and
// under how many conditions. It reads test names, so it needs no build tag
// and no network; a test skipped by a finding still counts as a condition.
//
// Usage: go run ./cmd/matrix
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// patterns are the test packages, in the order the matrix lists them.
var patterns = []string{"./sdk/...", "./rotation/", "./store/", "./serve/", "./live/"}

func main() {
	root, err := goOutput("", "list", "-m", "-f", "{{.Dir}}")
	if err != nil {
		fmt.Fprintln(os.Stderr, "matrix: not inside a module:", err)
		os.Exit(2)
	}
	root = strings.TrimSpace(root)

	failed := false
	var pkgs []string
	for _, p := range patterns {
		// A pattern a task has not written yet is a gap, not a reason to
		// print nothing for the others.
		out, err := goOutput(root, "list", p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "matrix: %s: %v\n", p, err)
			failed = true
			continue
		}
		pkgs = append(pkgs, strings.Fields(out)...)
	}

	fmt.Println("# Coverage matrix")
	for _, pkg := range pkgs {
		out, err := goOutput(root, "test", "-list", ".*", pkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "matrix: %s: %v\n", pkg, err)
			failed = true
			continue
		}
		fmt.Printf("\n## %s\n\n", pkg)
		printTable(group(out))
	}
	if failed {
		os.Exit(1)
	}
}

// group buckets the Test names in a -list output by the symbol between
// "Test" and the first underscore, which is the naming rule the suite keeps.
func group(list string) map[string][]string {
	bySymbol := map[string][]string{}
	for _, line := range strings.Split(list, "\n") {
		name := strings.TrimSpace(line)
		if !strings.HasPrefix(name, "Test") {
			continue // the trailing "ok" line, benchmarks, examples
		}
		symbol, _, _ := strings.Cut(strings.TrimPrefix(name, "Test"), "_")
		bySymbol[symbol] = append(bySymbol[symbol], name)
	}
	return bySymbol
}

func printTable(bySymbol map[string][]string) {
	symbols := make([]string, 0, len(bySymbol))
	for s := range bySymbol {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	fmt.Println("| Symbol | Conditions | Tests |")
	fmt.Println("|---|---:|---|")
	for _, s := range symbols {
		tests := bySymbol[s]
		sort.Strings(tests)
		fmt.Printf("| %s | %d | %s |\n", s, len(tests), strings.Join(tests, ", "))
	}
}

// goOutput runs the go tool in dir ("" for here) and returns its stdout;
// stderr is folded into the error so a build failure is readable.
func goOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("go %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}
