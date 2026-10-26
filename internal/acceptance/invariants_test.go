package acceptance

import (
	"os"
	"strings"
	"testing"
)

func TestGeneralSQLMatchesSource(t *testing.T) {
	for name, embedded := range map[string]string{"a3": a3, "a4": a4} {
		b, e := os.ReadFile("../../tests/" + name + "-reconcile.sql")
		if e != nil {
			t.Fatal(e)
		}
		lines := []string{}
		for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			if !strings.HasPrefix(l, `\`) {
				lines = append(lines, l)
			}
		}
		if strings.TrimSpace(strings.Join(lines, "\n")) != strings.TrimSpace(strings.ReplaceAll(embedded, "\r\n", "\n")) {
			t.Fatalf("%s: invariant source drift", name)
		}
	}
}
