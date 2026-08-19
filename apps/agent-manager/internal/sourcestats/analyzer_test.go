package sourcestats

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestAnalyze(t *testing.T) {
	t.Setenv("MAATGEN_CLOC_HELPER", "1")
	analyzer := New(os.Args[0])
	analyzer.prefixArgs = []string{"-test.run=TestClocHelper", "--"}

	repository := t.TempDir()
	stats, err := analyzer.Analyze(context.Background(), repository)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if stats.Total.Files != 3 || stats.Total.Code != 120 {
		t.Fatalf("total = %#v", stats.Total)
	}
	if len(stats.Languages) != 2 || stats.Languages[0].Language != "Go" || stats.Languages[0].Code != 100 {
		t.Fatalf("languages = %#v", stats.Languages)
	}
	if stats.Languages[1].Language != "TypeScript" || stats.Languages[1].Code != 20 {
		t.Fatalf("languages = %#v", stats.Languages)
	}
}

func TestAnalyzeUnavailable(t *testing.T) {
	analyzer := New("maatgen-cloc-does-not-exist")
	if _, err := analyzer.Analyze(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected an error when cloc is not on PATH")
	}
}

func TestClocHelper(t *testing.T) {
	if os.Getenv("MAATGEN_CLOC_HELPER") != "1" {
		return
	}
	fmt.Print(`{
		"header": {"cloc_version": "test"},
		"Go": {"nFiles": 2, "blank": 5, "comment": 1, "code": 100},
		"TypeScript": {"nFiles": 1, "blank": 2, "comment": 0, "code": 20},
		"SUM": {"nFiles": 3, "blank": 7, "comment": 1, "code": 120}
	}`)
	os.Exit(0)
}
