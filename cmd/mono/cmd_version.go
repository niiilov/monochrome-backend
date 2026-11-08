package main

import (
	"context"
	"fmt"
)

const version = "0.1.0-s0"

func init() {
	Register("version", "показать версию сборки", runVersion)
}

func runVersion(ctx context.Context, args []string) int {
	fmt.Printf("mono %s\n", version)
	return 0
}
