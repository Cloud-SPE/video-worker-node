package logger

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildLevels(t *testing.T) {
	t.Parallel()
	for _, lvl := range []string{"debug", "info", "warn", "error"} {
		var buf bytes.Buffer
		l, err := Build(lvl, "text", &buf)
		if err != nil {
			t.Errorf("Build(%q,text): %v", lvl, err)
			continue
		}
		l.Info("hello")
	}
}

func TestBuildJSON(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l, err := Build("info", "json", &buf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	l.Info("hi", "k", "v")
	if !strings.Contains(buf.String(), `"k":"v"`) {
		t.Fatalf("expected JSON output, got %q", buf.String())
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	if _, err := Build("loud", "text", &bytes.Buffer{}); err == nil {
		t.Fatal("expected level error")
	}
	if _, err := Build("info", "yaml", &bytes.Buffer{}); err == nil {
		t.Fatal("expected format error")
	}
}

func TestDiscard(t *testing.T) {
	t.Parallel()
	l := Discard()
	l.Info("hello")
	l.Error("hello")
}
