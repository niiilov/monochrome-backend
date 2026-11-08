package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestTestMandatoryHelp(t *testing.T) {
	var buf bytes.Buffer
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = prev }()
	code := runTestMandatory(context.Background(), []string{"--help"})
	w.Close()
	_, _ = io.Copy(&buf, r)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(buf.String(), "test-mandatory") || !strings.Contains(buf.String(), "--live") {
		t.Fatalf("help: %s", buf.String())
	}
}

func TestTestMandatoryUnknownFlag(t *testing.T) {
	var code int
	errOut := captureStderr(t, func() {
		code = runTestMandatory(context.Background(), []string{"--nope"})
	})
	if code != 2 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errOut, "неизвестный аргумент") {
		t.Fatalf("%s", errOut)
	}
}
